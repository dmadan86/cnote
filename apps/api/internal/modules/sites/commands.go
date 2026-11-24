// Package sites is the AI website builder (docs/design/site-builder.md). It imports no other module: the model,
// the daily budget and the company profile are reached through registry commands.
package sites

import (
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
)

const (
	conflictMsg   = "The site changed. Reload to see the latest."
	busyMsg       = "A change is already being made. Wait for it to finish."
	dailyTurns    = 200
	fallbackName  = "My website"
	templateKey   = "services"
	maxEditOps    = 20
	maxTurnsShown = 50
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type props = map[string]any

type siteOut struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Template        string    `json:"template"`
	DraftVersionID  string    `json:"draft_version_id"`
	ParentVersionID *string   `json:"parent_version_id"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type turnOut struct {
	ID            string          `json:"id"`
	Message       string          `json:"message"`
	BlockID       *string         `json:"block_id"`
	Status        string          `json:"status"`
	Reply         *string         `json:"reply"`
	Error         *string         `json:"error"`
	Summary       *string         `json:"summary"`
	BaseVersionID string          `json:"base_version_id"`
	VersionID     *string         `json:"version_id"`
	Steps         json.RawMessage `json:"steps"`
	CreatedAt     time.Time       `json:"created_at"`
}

// Commands are the site.* commands (design section 4, revised by section 14).
func Commands() []command.Command {
	mk := func(name string, g command.Group, role command.Role, person bool, desc string, schema json.RawMessage, h command.Handler) command.Command {
		c := command.NewCommand(name, g, h)
		c.MinRole, c.Description, c.Schema = role, desc, schema
		if person { // a machine must not drive a paid model loop
			inner := h
			c.Handler = func(c *command.Ctx, raw json.RawMessage) (any, error) {
				if c.Principal.Kind != command.KindUser {
					return nil, command.ErrForbidden("this is for people only; agents and API keys are refused")
				}
				return inner(c, raw)
			}
		}
		return c
	}
	R, W, M := command.GroupRead, command.GroupWriteItems, command.Member
	str := func(d string) props { return props{"type": "string", "description": d} }
	obj := func(req []string, p props) json.RawMessage {
		b, _ := json.Marshal(props{"type": "object", "required": req, "properties": p, "additionalProperties": false})
		return b
	}
	id, ver := str("site id"), str("the draft version the person is looking at; a stale one is a conflict")
	return []command.Command{
		mk("site.templates", R, M, false, "List the website templates, each filled from the company profile. too_long names profile fields left empty because they do not fit the site.", obj(nil, props{}), templates),
		mk("site.list", R, M, false, "List the workspace's websites (at most one in this version).", obj(nil, props{}), list),
		mk("site.create", W, command.Admin, false, "Create the workspace's website from a template (admins only; one per workspace). brief queues a first AI edit.",
			obj([]string{"template"}, props{"template": str("template key: services"), "name": str("site name"), "brief": str("optional first request for the AI")}), create),
		mk("site.get", R, M, false, "Open a website: the site, its draft (or one version's) page data and the recent chat turns, oldest first.",
			obj([]string{"id"}, props{"id": id, "version_id": str("a version of this site; default the draft")}), get),
		mk("site.chat", W, M, true, "Ask the AI to change the page. Starts a turn that runs in the background; poll site.turn_get.",
			obj([]string{"id", "expected_version_id", "message"}, props{"id": id, "expected_version_id": ver, "message": str("what to change, up to 2000 characters"), "block_id": str("the selected block")}), chat),
		mk("site.turn_get", R, M, false, "One chat turn; data is the new page once it is done and changed the page.", obj([]string{"id"}, props{"id": str("turn id")}), turnGet),
		mk("site.turn_cancel", W, M, false, "Stop a chat turn that is queued or running.", obj([]string{"id"}, props{"id": str("turn id")}), turnCancel),
		mk("site.edit", W, M, false, "Edit the page without the AI (move, duplicate or delete blocks) as a new version. ops are {tool, input}: update_block_props, add_block, remove_block, move_block, set_theme, duplicate_block {id}.",
			obj([]string{"id", "expected_version_id", "ops"}, props{"id": id, "expected_version_id": ver, "ops": props{"type": "array", "items": props{"type": "object"}}}), edit),
		mk("site.restore", W, M, false, "Make an older version of this site the draft (undo, redo, restore to here). Moves the draft pointer; nothing is deleted.",
			obj([]string{"id", "expected_version_id", "version_id"}, props{"id": id, "expected_version_id": ver, "version_id": str("the version to make the draft")}), restore),
	}
}

func uid(what, s string) error {
	if !uuidRe.MatchString(s) {
		return command.ErrInvalid(what + " must be an id")
	}
	return nil
}

func decode(raw json.RawMessage, in any) error {
	if err := json.Unmarshal(raw, in); err != nil {
		return command.ErrInvalid("invalid input")
	}
	return nil
}

func userID(c *command.Ctx) (string, error) {
	if c.Principal.UserID == nil {
		return "", command.ErrForbidden("a signed-in person is required")
	}
	return *c.Principal.UserID, nil
}

const siteSQL = `select s.id::text, s.name, s.template, s.draft_version_id::text, v.parent_id::text, s.created_at, s.updated_at, v.data
	from sites.sites s join sites.site_versions v on v.workspace_id = s.workspace_id and v.site_id = s.id and v.id = s.draft_version_id
	where s.workspace_id = $1::uuid and s.id = $2::uuid`

// loadSite reads the site and its draft; lock holds the site row until the transaction ends.
func loadSite(c *command.Ctx, id string, lock bool) (siteOut, json.RawMessage, error) {
	if err := uid("id", id); err != nil {
		return siteOut{}, nil, err
	}
	q := siteSQL
	if lock {
		q += " for update of s"
	}
	var s siteOut
	var data []byte
	err := c.Tx.QueryRow(c.Context, q, c.WorkspaceID(), id).Scan(&s.ID, &s.Name, &s.Template, &s.DraftVersionID, &s.ParentVersionID, &s.CreatedAt, &s.UpdatedAt, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil, command.ErrNotFound("site")
	}
	return s, data, command.DB(err)
}

func versionData(c *command.Ctx, siteID, versionID string) (json.RawMessage, error) {
	if err := uid("version_id", versionID); err != nil {
		return nil, err
	}
	var data []byte
	err := c.Tx.QueryRow(c.Context, "select data from sites.site_versions where workspace_id = $1::uuid and site_id = $2::uuid and id = $3::uuid",
		c.WorkspaceID(), siteID, versionID).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("version")
	}
	return data, command.DB(err)
}

// addVersion inserts an immutable version and moves the draft to it only if the draft is still `parent`.
func addVersion(c *command.Ctx, siteID string, parent, turnID *string, source, summary string, data json.RawMessage) (string, error) {
	if err := Validate(data); err != nil {
		return "", command.ErrInvalid(err.Error())
	}
	user, err := userID(c)
	if err != nil {
		return "", err
	}
	var id string
	err = c.Tx.QueryRow(c.Context,
		`insert into sites.site_versions (workspace_id, site_id, seq, parent_id, source, turn_id, summary, data, created_by)
		 values ($1::uuid, $2::uuid, (select coalesce(max(seq), 0) + 1 from sites.site_versions where site_id = $2::uuid), $3::uuid, $4, $5::uuid, $6, $7, $8::uuid)
		 returning id::text`, c.WorkspaceID(), siteID, parent, source, turnID, summary, []byte(data), user).Scan(&id)
	if err != nil {
		return "", command.DB(err)
	}
	if err := moveDraft(c, siteID, parent, id); err != nil {
		return "", err
	}
	_, err = c.Emit("site.version_created", nil, props{"site_id": siteID, "version_id": id, "source": source})
	return id, err
}

// moveDraft is the compare-and-set on the draft pointer.
func moveDraft(c *command.Ctx, siteID string, from *string, to string) error {
	tag, err := c.Tx.Exec(c.Context,
		"update sites.sites set draft_version_id = $3::uuid, updated_at = now() where workspace_id = $1::uuid and id = $2::uuid and draft_version_id is not distinct from $4::uuid",
		c.WorkspaceID(), siteID, to, from)
	if err != nil {
		return command.DB(err)
	}
	if tag.RowsAffected() != 1 {
		return command.ErrConflict(conflictMsg)
	}
	return nil
}

func activeTurn(c *command.Ctx, siteID string) (bool, error) {
	var busy bool
	err := c.Tx.QueryRow(c.Context, "select exists (select 1 from sites.site_turns where workspace_id = $1::uuid and site_id = $2::uuid and status in ('queued','running'))",
		c.WorkspaceID(), siteID).Scan(&busy)
	return busy, command.DB(err)
}

// ---------- sites ----------

func list(c *command.Ctx, raw json.RawMessage) (any, error) {
	rows, err := c.Tx.Query(c.Context, `select s.id::text, s.name, s.template, s.draft_version_id::text, v.parent_id::text, s.created_at, s.updated_at
		from sites.sites s join sites.site_versions v on v.workspace_id = s.workspace_id and v.site_id = s.id and v.id = s.draft_version_id
		where s.workspace_id = $1::uuid order by s.created_at desc`, c.WorkspaceID())
	if err != nil {
		return nil, command.DB(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (s siteOut, err error) {
		err = r.Scan(&s.ID, &s.Name, &s.Template, &s.DraftVersionID, &s.ParentVersionID, &s.CreatedAt, &s.UpdatedAt)
		return
	})
	if out == nil {
		out = []siteOut{}
	}
	return out, command.DB(err)
}

func create(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		Template, Name, Brief string
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if !hasTemplate(in.Template) {
		return nil, command.ErrInvalid("unknown template")
	}
	user, err := userID(c)
	if err != nil {
		return nil, err
	}
	brief := strings.TrimSpace(in.Brief)
	if utf8.RuneCountInString(brief) > 2000 {
		return nil, command.ErrInvalid("brief is too long")
	}
	b, err := business(c)
	if err != nil {
		return nil, err
	}
	data, tooLong, err := fillTemplate(b, in.Template)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = b["name"]
	}
	if n := utf8.RuneCountInString(name); n < 1 || n > 120 {
		name = fallbackName
	}
	var siteID string
	err = c.Tx.QueryRow(c.Context, `insert into sites.sites (workspace_id, name, template, created_by) values ($1::uuid, $2, $3, $4::uuid)
		on conflict do nothing returning id::text`, c.WorkspaceID(), name, in.Template, user).Scan(&siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrConflict("This workspace already has a website.")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	version, err := addVersion(c, siteID, nil, nil, "template", "Started from the template", data)
	if err != nil {
		return nil, err
	}
	if _, err = c.Emit("site.created", nil, props{"site_id": siteID}); err != nil {
		return nil, err
	}
	out := props{"too_long": tooLong}
	if brief != "" {
		turn, err := queueTurn(c, siteID, version, brief, nil, "reasoning")
		if err != nil {
			return nil, err
		}
		out["turn_id"] = turn
	}
	site, _, err := loadSite(c, siteID, false)
	out["site"], out["data"] = site, data
	return out, err
}

const turnSelect = `select t.id::text, t.message, t.block_id, t.status, t.reply, t.error, v.summary, t.base_version_id::text, t.version_id::text, t.steps, t.created_at
	from sites.site_turns t left join sites.site_versions v on v.workspace_id = t.workspace_id and v.site_id = t.site_id and v.id = t.version_id `

func scanTurn(r pgx.CollectableRow) (t turnOut, err error) {
	err = r.Scan(&t.ID, &t.Message, &t.BlockID, &t.Status, &t.Reply, &t.Error, &t.Summary, &t.BaseVersionID, &t.VersionID, &t.Steps, &t.CreatedAt)
	return
}

func get(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID        string `json:"id"`
		VersionID string `json:"version_id"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	site, data, err := loadSite(c, in.ID, false)
	if err != nil {
		return nil, err
	}
	if in.VersionID != "" && in.VersionID != site.DraftVersionID {
		if data, err = versionData(c, site.ID, in.VersionID); err != nil {
			return nil, err
		}
	}
	rows, err := c.Tx.Query(c.Context, "select * from ("+turnSelect+` where t.workspace_id = $1::uuid and t.site_id = $2::uuid order by t.created_at desc limit $3) x order by created_at`,
		c.WorkspaceID(), site.ID, maxTurnsShown)
	if err != nil {
		return nil, command.DB(err)
	}
	turns, err := pgx.CollectRows(rows, scanTurn)
	if turns == nil {
		turns = []turnOut{}
	}
	return props{"site": site, "data": data, "turns": turns}, command.DB(err)
}

// queueTurn inserts a turn and the long-lane job that runs it.
func queueTurn(c *command.Ctx, siteID, base, message string, blockID *string, tier string) (string, error) {
	user, err := userID(c)
	if err != nil {
		return "", err
	}
	var id string
	err = c.Tx.QueryRow(c.Context, `insert into sites.site_turns (workspace_id, site_id, created_by, message, block_id, tier, base_version_id)
		values ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7::uuid) returning id::text`, c.WorkspaceID(), siteID, user, message, blockID, tier, base).Scan(&id)
	if err != nil {
		return "", command.DB(err)
	}
	payload, _ := command.Marshal(map[string]string{"turn_id": id})
	_, err = c.Tx.Exec(c.Context, "insert into core.jobs (workspace_id, kind, payload, max_attempts) values ($1::uuid, $2, $3, 2)", c.WorkspaceID(), jobKind, json.RawMessage(payload))
	return id, command.DB(err)
}

func chat(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID                string
		ExpectedVersionID string  `json:"expected_version_id"`
		Message           string  `json:"message"`
		BlockID           *string `json:"block_id"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	msg := strings.TrimSpace(in.Message)
	if n := utf8.RuneCountInString(msg); n < 1 || n > 2000 {
		return nil, command.ErrInvalid("message must be 1 to 2000 characters")
	}
	site, data, err := loadSite(c, in.ID, true)
	if err != nil {
		return nil, err
	}
	if in.ExpectedVersionID != site.DraftVersionID {
		return nil, command.ErrConflict(conflictMsg)
	}
	if in.BlockID != nil && !hasBlock(data, *in.BlockID) {
		return nil, command.ErrInvalid("that block is not on the page")
	}
	if busy, err := activeTurn(c, site.ID); err != nil {
		return nil, err
	} else if busy {
		return nil, command.ErrConflict(busyMsg)
	}
	var n int
	if err := c.Tx.QueryRow(c.Context, "select count(*) from sites.site_turns where workspace_id = $1::uuid and created_at > now() - interval '24 hours'", c.WorkspaceID()).Scan(&n); err != nil {
		return nil, command.DB(err)
	}
	if n >= dailyTurns {
		return nil, command.NewError(command.PlanLimit, "This workspace has used its 200 website changes by chat for today. Try again tomorrow.")
	}
	turn, err := queueTurn(c, site.ID, site.DraftVersionID, msg, in.BlockID, "routine")
	return props{"turn_id": turn}, err
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func hasBlock(data json.RawMessage, id string) bool {
	var d struct {
		Content []struct {
			Props struct {
				ID string `json:"id"`
			} `json:"props"`
		} `json:"content"`
	}
	_ = json.Unmarshal(data, &d)
	for _, b := range d.Content {
		if b.Props.ID == id {
			return true
		}
	}
	return false
}

func turnGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := uid("id", in.ID); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, turnSelect+"where t.workspace_id = $1::uuid and t.id = $2::uuid", c.WorkspaceID(), in.ID)
	if err != nil {
		return nil, command.DB(err)
	}
	turns, err := pgx.CollectRows(rows, scanTurn)
	if err != nil {
		return nil, command.DB(err)
	}
	if len(turns) == 0 {
		return nil, command.ErrNotFound("turn")
	}
	out := props{"turn": turns[0]}
	if t := turns[0]; t.Status == "done" && t.VersionID != nil {
		var data []byte
		if err := c.Tx.QueryRow(c.Context, `select v.data from sites.site_versions v join sites.site_turns t on t.workspace_id = v.workspace_id and t.site_id = v.site_id and t.version_id = v.id
			where t.workspace_id = $1::uuid and t.id = $2::uuid`, c.WorkspaceID(), t.ID).Scan(&data); err != nil {
			return nil, command.DB(err)
		}
		out["data"] = json.RawMessage(data)
	}
	return out, nil
}

func turnCancel(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := uid("id", in.ID); err != nil {
		return nil, err
	}
	var status string
	err := c.Tx.QueryRow(c.Context, `update sites.site_turns set status = 'cancelled', finished_at = now(), updated_at = now()
		where workspace_id = $1::uuid and id = $2::uuid and status in ('queued','running') returning status`, c.WorkspaceID(), in.ID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) { // already finished: say how
		err = c.Tx.QueryRow(c.Context, "select status from sites.site_turns where workspace_id = $1::uuid and id = $2::uuid", c.WorkspaceID(), in.ID).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, command.ErrNotFound("turn")
		}
	}
	return props{"status": status}, command.DB(err)
}

// ---------- edit and restore ----------

var editVerb = map[string]string{"update_block_props": "Updated", "add_block": "Added", "remove_block": "Deleted", "move_block": "Moved", "duplicate_block": "Duplicated", "set_theme": "Changed the theme"}

func edit(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID                string
		ExpectedVersionID string `json:"expected_version_id"`
		Ops               []struct {
			Tool  string
			Input json.RawMessage
		}
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	expected := in.ExpectedVersionID
	if len(in.Ops) == 0 || len(in.Ops) > maxEditOps {
		return nil, command.ErrInvalid("send between 1 and 20 operations")
	}
	site, data, err := loadSite(c, in.ID, true)
	if err != nil {
		return nil, err
	}
	if err := checkDraft(c, site, expected); err != nil {
		return nil, err
	}
	working := data
	var summaries []string
	for _, op := range in.Ops {
		tool, input := op.Tool, op.Input
		if tool == "duplicate_block" {
			if tool, input, err = duplicateOp(working, input); err != nil {
				return nil, command.ErrInvalid(err.Error())
			}
		}
		next, err := Apply(working, tool, input)
		if err != nil {
			return nil, command.ErrInvalid(op.Tool + ": " + err.Error())
		}
		working = next
		var a struct{ ID, Type string }
		_ = json.Unmarshal(op.Input, &a)
		label := firstNonEmpty(a.Type, strings.SplitN(a.ID, "-", 2)[0])
		summaries = append(summaries, strings.TrimSpace(editVerb[op.Tool]+" "+label))
	}
	if sameJSON(data, working) {
		return props{"site": site, "data": data}, nil
	}
	if _, err := addVersion(c, site.ID, &site.DraftVersionID, nil, "edit", strings.Join(summaries, ", "), working); err != nil {
		return nil, err
	}
	site, working, err = loadSite(c, site.ID, false)
	return props{"site": site, "data": working}, err
}

// checkDraft refuses a stale view of the draft and any change while a chat turn is in flight.
func checkDraft(c *command.Ctx, site siteOut, expected string) error {
	if expected != site.DraftVersionID {
		return command.ErrConflict(conflictMsg)
	}
	if busy, err := activeTurn(c, site.ID); err != nil {
		return err
	} else if busy {
		return command.ErrConflict(busyMsg)
	}
	return nil
}

// duplicateOp turns duplicate_block {id} into an add_block just after the original, with a fresh id.
func duplicateOp(data, input json.RawMessage) (string, json.RawMessage, error) {
	var in struct{ ID string }
	var d struct {
		Content []struct {
			Type  string         `json:"type"`
			Props map[string]any `json:"props"`
		} `json:"content"`
	}
	if json.Unmarshal(input, &in) != nil || json.Unmarshal(data, &d) != nil {
		return "", nil, errors.New("invalid input")
	}
	for i, b := range d.Content {
		if b.Props["id"] == in.ID {
			p := props{}
			for k, v := range b.Props {
				if k != "id" {
					p[k] = v
				}
			}
			out, _ := json.Marshal(props{"type": b.Type, "props": p, "index": i + 1})
			return "add_block", out, nil
		}
	}
	return "", nil, errors.New("unknown block id")
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

func restore(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID                string
		ExpectedVersionID string `json:"expected_version_id"`
		VersionID         string `json:"version_id"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	site, data, err := loadSite(c, in.ID, true)
	if err != nil {
		return nil, err
	}
	if err := checkDraft(c, site, in.ExpectedVersionID); err != nil {
		return nil, err
	}
	if in.VersionID == site.DraftVersionID {
		return props{"site": site, "data": data}, nil
	}
	if data, err = versionData(c, site.ID, in.VersionID); err != nil {
		return nil, err
	}
	if err := moveDraft(c, site.ID, &site.DraftVersionID, in.VersionID); err != nil {
		return nil, err
	}
	site, _, err = loadSite(c, site.ID, false)
	return props{"site": site, "data": data}, err
}
