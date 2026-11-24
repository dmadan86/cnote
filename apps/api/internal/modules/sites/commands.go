// Package sites is the AI website builder (docs/design/site-builder.md). It imports no other module: the model,
// the daily budget and the company profile are reached through registry commands.
package sites

import (
	"encoding/json"
	"errors"
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
	Rev             int       `json:"rev"`
	ParentVersionID *string   `json:"parent_version_id"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`

	Subdomain          *string    `json:"subdomain"`
	URL                *string    `json:"url"`
	PublishedVersionID *string    `json:"published_version_id"`
	PublishedAt        *time.Time `json:"published_at"`
	TakenDown          *struct {
		Reason string `json:"reason"`
	} `json:"taken_down"`
	Pro        bool `json:"pro"`
	FormCount  int  `json:"form_count"`
	FormPaused bool `json:"form_paused"`

	down   bool
	reason *string
}

// siteFields and siteFrom are the columns every site read shares; dest lists the scan targets in that order.
const siteFields = `s.id::text, s.name, s.template, s.draft_version_id::text, v.rev, v.parent_id::text, s.created_at, s.updated_at,
	s.subdomain, s.published_version_id::text, s.published_at, s.form_count, s.form_paused, coalesce(p.pro, false), p.taken_down_at is not null, p.taken_down_reason`
const siteFrom = ` from sites.sites s join sites.site_versions v on v.workspace_id = s.workspace_id and v.site_id = s.id and v.id = s.draft_version_id
	left join sites.site_platform p on p.site_id = s.id `

func (s *siteOut) dest(extra ...any) []any {
	return append([]any{&s.ID, &s.Name, &s.Template, &s.DraftVersionID, &s.Rev, &s.ParentVersionID, &s.CreatedAt, &s.UpdatedAt,
		&s.Subdomain, &s.PublishedVersionID, &s.PublishedAt, &s.FormCount, &s.FormPaused, &s.Pro, &s.down, &s.reason}, extra...)
}

// done fills the fields derived from what was scanned.
func (s *siteOut) done() {
	if s.Subdomain != nil && SitesDomain() != "" {
		u := SiteURL(*s.Subdomain)
		s.URL = &u
	}
	if s.down {
		s.TakenDown = &struct {
			Reason string `json:"reason"`
		}{Reason: firstNonEmpty(deref(s.reason), "This site was taken offline.")}
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
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
	Kind          string          `json:"kind"`
	PostID        *string         `json:"post_id"`
}

func mk(name string, g command.Group, role command.Role, person bool, desc string, schema json.RawMessage, h command.Handler) command.Command {
	c := command.NewCommand(name, g, h)
	c.MinRole, c.Description, c.Schema = role, desc, schema
	if person { // a machine must not drive a paid model loop or publish to the internet
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

func str(d string) props { return props{"type": "string", "description": d} }
func obj(req []string, p props) json.RawMessage {
	b, _ := json.Marshal(props{"type": "object", "required": req, "properties": p, "additionalProperties": false})
	return b
}

// Commands are the site.* commands (design section 4, revised by section 14).
func Commands() []command.Command {
	R, W, M := command.GroupRead, command.GroupWriteItems, command.Member
	id, ver := str("site id"), str("the draft version the person is looking at; a stale one is a conflict")
	commands := append([]command.Command{
		mk("site.templates", R, M, false, "List the website templates, each filled from the company profile. too_long names profile fields left empty because they do not fit the site.", obj(nil, props{}), templates),
		mk("site.list", R, M, false, "List the workspace's websites (at most one in this version).", obj(nil, props{}), list),
		mk("site.create", W, command.Admin, false, "Create the workspace's website from a template (admins only; one per workspace). brief queues a first AI edit.",
			obj([]string{"template"}, props{"template": str("template key: services"), "name": str("site name"), "brief": str("optional first request for the AI")}), create),
		mk("site.get", R, M, false, "Open a website: the site, its draft (or one version's) page data and the recent chat turns, oldest first.",
			obj([]string{"id"}, props{"id": id, "version_id": str("a version of this site; default the draft")}), get),
		mk("site.chat", W, M, true, "Ask the AI to change the page. Starts a turn that runs in the background; poll site.turn_get.",
			obj([]string{"id", "expected_version_id", "message"}, props{"id": id, "expected_version_id": ver, "message": str("what to change, up to 2000 characters"), "block_id": str("the selected block"), "page": str("the open page's address; omit for Home"), "post": str("a blog post id to edit, or \"new\" to draft a new post; the turn then works on that post only"), "image_ids": props{"type": "array", "items": props{"type": "string"}, "description": "up to 4 photos just uploaded to this site"}}), chat),
		mk("site.turn_get", R, M, false, "One chat turn; data is the new page once it is done and changed the page.", obj([]string{"id"}, props{"id": str("turn id")}), turnGet),
		mk("site.turn_cancel", W, M, false, "Stop a chat turn that is queued or running.", obj([]string{"id"}, props{"id": str("turn id")}), turnCancel),
		mk("site.edit", W, M, false, "Edit the site without the AI as a new version. ops are {tool, input}: update_block_props, add_block (page?), remove_block, move_block, set_theme, duplicate_block {id}, add_page {title, slug?, template?}, remove_page {page}, set_page {page?, title?, description?, slug?}, set_content {page, content} (the editor). source is edit (toolbar, default) or editor.",
			obj([]string{"id", "expected_version_id", "ops"}, props{"id": id, "expected_version_id": ver, "expected_rev": props{"type": "integer"}, "source": props{"type": "string", "enum": []string{"edit", "editor"}}, "ops": props{"type": "array", "items": props{"type": "object"}}}), edit),
		mk("site.restore", W, M, false, "Make an older version of this site the draft. By default moves the draft pointer (undo, redo); with as_new adds a new version on top with that content.",
			obj([]string{"id", "expected_version_id", "version_id"}, props{"id": id, "expected_version_id": ver, "version_id": str("the version to make the draft"), "as_new": props{"type": "boolean"}}), restore),
	}, publishCommands(id, ver)...)
	for i := range commands {
		if commands[i].Name == "site.publish" {
			commands[i].Handler = guardPublish(commands[i].Handler)
		}
	}
	for _, group := range [][]command.Command{CatalogueCommands(), BlogCommands(), PagesCommands(), EditorCommands(), DomainsCommands(), StatsCommands(), ExportCommands()} {
		commands = append(commands, group...)
	}
	return commands
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

const siteSQL = `select ` + siteFields + `, v.data` + siteFrom + `where s.workspace_id = $1::uuid and s.id = $2::uuid`

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
	err := c.Tx.QueryRow(c.Context, q, c.WorkspaceID(), id).Scan(s.dest(&data)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil, command.ErrNotFound("site")
	}
	s.done()
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
	if err := checkImages(c, siteID, data); err != nil {
		return "", err
	}
	if err := checkVersionPages(c, siteID, parent, data); err != nil {
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
	rows, err := c.Tx.Query(c.Context, `select `+siteFields+siteFrom+`where s.workspace_id = $1::uuid order by s.created_at desc`, c.WorkspaceID())
	if err != nil {
		return nil, command.DB(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (s siteOut, err error) {
		if err = r.Scan(s.dest()...); err == nil {
			s.done()
		}
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
		turn, err := queueTurn(c, siteID, version, brief, nil, "reasoning", nil)
		if err != nil {
			return nil, err
		}
		out["turn_id"] = turn
	}
	site, _, err := loadSite(c, siteID, false)
	out["site"], out["data"] = site, data
	return out, err
}

const turnSelect = `select t.id::text, t.message, t.block_id, t.status, t.reply, t.error, v.summary, t.base_version_id::text, t.version_id::text, t.steps, t.created_at, t.kind, t.post_id::text
	from sites.site_turns t left join sites.site_versions v on v.workspace_id = t.workspace_id and v.site_id = t.site_id and v.id = t.version_id `

func scanTurn(r pgx.CollectableRow) (t turnOut, err error) {
	err = r.Scan(&t.ID, &t.Message, &t.BlockID, &t.Status, &t.Reply, &t.Error, &t.Summary, &t.BaseVersionID, &t.VersionID, &t.Steps, &t.CreatedAt, &t.Kind, &t.PostID)
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
	if err != nil {
		return nil, command.DB(err)
	}
	out := props{"site": site, "data": data, "turns": turns}
	return out, getExtras(c, site, out)
}

// queueTurn inserts a turn and the long-lane job that runs it.
func queueTurn(c *command.Ctx, siteID, base, message string, blockID *string, tier string, images []string) (string, error) {
	user, err := userID(c)
	if err != nil {
		return "", err
	}
	var id string
	if images == nil {
		images = []string{}
	}
	err = c.Tx.QueryRow(c.Context, `insert into sites.site_turns (workspace_id, site_id, created_by, message, block_id, tier, base_version_id, image_ids)
		values ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7::uuid, $8::text[]::uuid[]) returning id::text`, c.WorkspaceID(), siteID, user, message, blockID, tier, base, images).Scan(&id)
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
		ExpectedVersionID string   `json:"expected_version_id"`
		Message           string   `json:"message"`
		BlockID           *string  `json:"block_id"`
		ImageIDs          []string `json:"image_ids"`
		Page              string   `json:"page"`
		Post              string   `json:"post"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if len(in.ImageIDs) > 4 {
		return nil, command.ErrInvalid("attach at most 4 photos")
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
	if in.Page != "" {
		var d map[string]any
		if json.Unmarshal(data, &d) != nil || findPage(d, in.Page) == nil {
			return nil, command.ErrInvalid("that page is not on the site")
		}
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
	if err := checkAllowance(c, site); err != nil {
		return nil, err
	}
	owned, err := siteImages(c, site.ID)
	if err != nil {
		return nil, err
	}
	for _, id := range in.ImageIDs {
		if !uuidRe.MatchString(id) || !owned[strings.ToLower(id)] {
			return nil, command.ErrInvalid("that photo is not on this website")
		}
	}
	for i := range in.ImageIDs {
		in.ImageIDs[i] = strings.ToLower(in.ImageIDs[i])
	}
	if in.Post != "" { // a blog turn: the post must be this site's own
		postID := ""
		if in.Post != "new" {
			var ok bool
			if !uuidRe.MatchString(in.Post) {
				return nil, command.ErrInvalid("that post is not on this website")
			}
			if err := c.Tx.QueryRow(c.Context, "select exists(select 1 from sites.posts where workspace_id = $1::uuid and site_id = $2::uuid and id = $3::uuid)",
				c.WorkspaceID(), site.ID, in.Post).Scan(&ok); err != nil {
				return nil, command.DB(err)
			}
			if !ok {
				return nil, command.ErrInvalid("that post is not on this website")
			}
			postID = strings.ToLower(in.Post)
		}
		turn, err := queuePostTurn(c, site.ID, site.DraftVersionID, msg, postID)
		return props{"turn_id": turn}, err
	}
	turn, err := queueTurn(c, site.ID, site.DraftVersionID, msg, in.BlockID, "routine", in.ImageIDs)
	if err == nil && in.Page != "" {
		_, err = c.Tx.Exec(c.Context, "update sites.site_turns set page = $3 where workspace_id = $1::uuid and id = $2::uuid", c.WorkspaceID(), turn, in.Page)
		err = command.DB(err)
	}
	return props{"turn_id": turn}, err
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func hasBlock(data json.RawMessage, id string) bool {
	for _, b := range allBlocks(data) {
		if b.Props["id"] == id {
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
