// Package projects ports the project item and workflow commands of crates/cnote-projects (items.rs, wf.rs,
// workflow.rs, cnql.rs, evidence.rs): item.*, triage.*, workflow.*, space.drift/realign and evidence.record.
// Shared helpers (LoadItem, TransitionItem, SpaceWF, ParseCNQL, ItemMatches...) are exported so the planning and
// automation ports can reuse them.
package projects

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

var Priorities = []string{"urgent", "high", "medium", "low", "none"}

// Item is Rust ItemOut.
type Item struct {
	ID              string          `json:"id"`
	SpaceID         string          `json:"space_id"`
	Key             string          `json:"key"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	ItemType        string          `json:"item_type"`
	Status          string          `json:"status"`
	StatusCategory  string          `json:"status_category"`
	Priority        string          `json:"priority"`
	AssigneeID      *string         `json:"assignee_id"`
	AssigneeAgentID *string         `json:"assignee_agent_id"`
	ReporterID      *string         `json:"reporter_id"`
	ParentID        *string         `json:"parent_id"`
	SprintID        *string         `json:"sprint_id"`
	FixVersionID    *string         `json:"fix_version_id"`
	ComponentID     *string         `json:"component_id"`
	Estimate        *notes.F64      `json:"estimate"`
	DueDate         *string         `json:"due_date"`
	Labels          []string        `json:"labels"`
	Custom          json.RawMessage `json:"custom"`
	Rank            string          `json:"rank"`
	WorkflowVersion int32           `json:"workflow_version"`
	Triage          bool            `json:"triage"`
	ResolvedAt      *command.Ts     `json:"resolved_at"`
	CreatedAt       command.Ts      `json:"created_at"`
	UpdatedAt       command.Ts      `json:"updated_at"`
}

const itemSelect = `select i.note_id::text as id, i.space_id::text, i.key, n.title, n.content_md as description, i.item_type, i.status,
    i.status_category, i.priority, i.assignee_id::text, i.assignee_agent_id::text, i.reporter_id::text, n.parent_id::text, i.sprint_id::text,
    i.fix_version_id::text, i.component_id::text, i.estimate::float8 as estimate, i.due_date::text, i.labels, i.custom, i.rank,
    i.workflow_version, i.triage, i.resolved_at, n.created_at, n.updated_at
    from projects.items i join core.notes n on n.id = i.note_id join core.spaces sp on sp.id = i.space_id`

func scanItem(row pgx.Row) (*Item, error) {
	var it Item
	var est *float64
	var custom []byte
	var resolved *time.Time
	var created, updated time.Time
	if err := row.Scan(&it.ID, &it.SpaceID, &it.Key, &it.Title, &it.Description, &it.ItemType, &it.Status, &it.StatusCategory,
		&it.Priority, &it.AssigneeID, &it.AssigneeAgentID, &it.ReporterID, &it.ParentID, &it.SprintID, &it.FixVersionID,
		&it.ComponentID, &est, &it.DueDate, &it.Labels, &custom, &it.Rank, &it.WorkflowVersion, &it.Triage, &resolved,
		&created, &updated); err != nil {
		return nil, err
	}
	if est != nil {
		f := notes.F64(*est)
		it.Estimate = &f
	}
	if it.Labels == nil {
		it.Labels = []string{}
	}
	var err error
	if it.Custom, err = notes.NormValue(custom); err != nil {
		return nil, err
	}
	it.ResolvedAt = command.TsPtr(resolved)
	it.CreatedAt, it.UpdatedAt = command.Ts(created), command.Ts(updated)
	return &it, nil
}

// ItemRef is the `id` or `key` pair most item commands take.
type ItemRef struct {
	ID  *string
	Key *string
}

func refOf(o jobj) (ItemRef, error) {
	id, err := o.optUUID("id")
	if err != nil {
		return ItemRef{}, err
	}
	key, err := o.optStr("key")
	return ItemRef{id, key}, err
}

func idRef(id string) ItemRef { return ItemRef{ID: &id} }

// LoadItem reads an item by id or key and checks page-level access (items can be restricted like any note).
func LoadItem(c *command.Ctx, r ItemRef, write bool) (*Item, error) {
	if r.ID == nil && r.Key == nil {
		return nil, command.ErrInvalid("id or key is required")
	}
	it, err := scanItem(c.Tx.QueryRow(c.Context,
		itemSelect+" where n.deleted_at is null and (i.note_id = $1::uuid or i.key = upper($2::text))", r.ID, r.Key))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("item")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if _, err := access.AssertNoteAccess(c, it.ID, write); err != nil {
		return nil, err
	}
	return it, nil
}

// FilledFields are the fields a required_fields guard can ask for.
func FilledFields(i *Item) map[string]bool {
	s := map[string]bool{}
	add := func(name string, ok bool) {
		if ok {
			s[name] = true
		}
	}
	add("assignee", i.AssigneeID != nil || i.AssigneeAgentID != nil)
	add("estimate", i.Estimate != nil)
	add("due_date", i.DueDate != nil)
	add("labels", len(i.Labels) > 0)
	add("sprint", i.SprintID != nil)
	add("fix_version", i.FixVersionID != nil)
	add("component", i.ComponentID != nil)
	add("priority", i.Priority != "none")
	add("description", strings.TrimSpace(i.Description) != "")
	add("parent", i.ParentID != nil)
	var custom map[string]json.RawMessage
	if json.Unmarshal(i.Custom, &custom) == nil {
		for k, v := range custom {
			if !isNull(v) && string(v) != `""` {
				s[k] = true
			}
		}
	}
	return s
}

func checkPriority(p string) error {
	if slices.Contains(Priorities, p) {
		return nil
	}
	return command.ErrInvalid("priority must be one of " + strings.Join(Priorities, ", "))
}

func validateAssignee(c *command.Ctx, user, agent *string) error {
	if user != nil && agent != nil {
		return command.ErrInvalid("assign to a user or an agent, not both")
	}
	if user != nil {
		var ok bool
		if err := c.Tx.QueryRow(c.Context, "select exists(select 1 from core.memberships where user_id = $1::uuid)", *user).Scan(&ok); err != nil {
			return command.DB(err)
		}
		if !ok {
			return command.ErrInvalid("assignee is not a member of this workspace")
		}
	}
	if agent != nil {
		var ok bool
		if err := c.Tx.QueryRow(c.Context, "select exists(select 1 from core.agents where id = $1::uuid)", *agent).Scan(&ok); err != nil {
			return command.DB(err)
		}
		if !ok {
			return command.ErrInvalid("agent not found")
		}
	}
	return nil
}

// assertInSpace: the sprint / version / component must belong to the item's Space.
func assertInSpace(c *command.Ctx, table string, id *string, space string) error {
	if id == nil {
		return nil
	}
	var ok bool
	if err := c.Tx.QueryRow(c.Context, "select exists(select 1 from "+table+" where id = $1::uuid and space_id = $2::uuid)", *id, space).Scan(&ok); err != nil {
		return command.DB(err)
	}
	if !ok {
		return command.ErrInvalid(fmt.Sprintf("%s %s is not in this space", table, *id))
	}
	return nil
}

// ---------- rank (lexicographic fractional indexing over a-z) ----------

func digit(c byte) uint32 {
	if c < 'a' {
		return 0
	}
	return min(uint32(c-'a'), 25)
}

func mid(a []byte, b []byte, hasB bool) []byte {
	if hasB {
		n := 0
		for n < len(b) {
			ac := byte('a')
			if n < len(a) {
				ac = a[n]
			}
			if ac != b[n] {
				break
			}
			n++
		}
		if n > 0 {
			r := append([]byte{}, b[:n]...)
			return append(r, mid(a[min(n, len(a)):], b[n:], true)...)
		}
	}
	var da uint32
	if len(a) > 0 {
		da = digit(a[0])
	}
	db := uint32(26)
	if hasB && len(b) > 0 {
		db = digit(b[0])
	}
	switch {
	case db > da+1:
		return []byte{'a' + byte((da+db+1)/2)}
	case hasB && len(b) > 1:
		return []byte{b[0]}
	}
	r := []byte{'a' + byte(da)}
	var rest []byte
	if len(a) > 1 {
		rest = a[1:]
	}
	return append(r, mid(rest, nil, false)...)
}

// RankBetween is a rank strictly between lo and hi (nil = open end). Equal neighbours get a suffix, which keeps
// order stable through the `number` tie-break.
func RankBetween(lo, hi *string) string {
	l := ""
	if lo != nil {
		l = *lo
	}
	if hi != nil && l >= *hi {
		return l + "n"
	}
	var hb []byte
	if hi != nil {
		hb = []byte(*hi)
	}
	r := mid([]byte(l), hb, hi != nil)
	if !utf8Valid(r) {
		return "n"
	}
	return string(r)
}

func utf8Valid(b []byte) bool { return strings.ToValidUTF8(string(b), "\x00") == string(b) }

// ---------- create ----------

func spaceKeyUpper(c *command.Ctx, key string) (string, error) {
	var id string
	err := c.Tx.QueryRow(c.Context, "select id::text from core.spaces where key = upper($1::text)", strings.TrimSpace(key)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", command.ErrNotFound("space")
	}
	return id, command.DB(err)
}

func itemCreate(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	var in struct {
		spaceID, spaceKey, itemType, description, priority    *string
		assigneeID, assigneeAgentID, parentID, sprint, fixVer *string
		component                                             *string
		estimate                                              *float64
		dueDate                                               *string
		custom                                                json.RawMessage
		triage                                                *bool
		labels                                                []string
	}
	title, err := o.reqStr("title")
	if err != nil {
		return nil, err
	}
	for _, f := range []struct {
		k string
		p **string
	}{{"space_id", &in.spaceID}, {"assignee_id", &in.assigneeID}, {"assignee_agent_id", &in.assigneeAgentID},
		{"parent_id", &in.parentID}, {"sprint_id", &in.sprint}, {"fix_version_id", &in.fixVer}, {"component_id", &in.component}} {
		if *f.p, err = o.optUUID(f.k); err != nil {
			return nil, err
		}
	}
	for _, f := range []struct {
		k string
		p **string
	}{{"space_key", &in.spaceKey}, {"item_type", &in.itemType}, {"description", &in.description}, {"priority", &in.priority}} {
		if *f.p, err = o.optStr(f.k); err != nil {
			return nil, err
		}
	}
	if in.labels, err = o.strVecDef("labels"); err != nil {
		return nil, err
	}
	if in.estimate, err = o.optF64("estimate"); err != nil {
		return nil, err
	}
	if in.dueDate, err = o.optDate("due_date"); err != nil {
		return nil, err
	}
	if in.custom, err = o.optValue("custom"); err != nil {
		return nil, err
	}
	if in.triage, err = o.optBool("triage"); err != nil {
		return nil, err
	}

	title = strings.TrimSpace(title)
	if title == "" {
		return nil, command.ErrInvalid("title is required")
	}
	var spaceID string
	switch {
	case in.spaceID != nil:
		spaceID = *in.spaceID
	case in.spaceKey != nil:
		if spaceID, err = spaceKeyUpper(c, *in.spaceKey); err != nil {
			return nil, err
		}
	default:
		return nil, command.ErrInvalid("space_id or space_key is required")
	}
	if err := access.AssertSpaceAccess(c, spaceID, true); err != nil {
		return nil, err
	}
	wf, err := SpaceWF(c, spaceID)
	if err != nil {
		return nil, err
	}
	itemType := "task"
	if in.itemType != nil {
		itemType = *in.itemType
	}
	itemType = rustLower(strings.TrimSpace(itemType))
	if err := checkType(wf.Def, itemType); err != nil {
		return nil, err
	}
	priority := "medium"
	if in.priority != nil {
		priority = *in.priority
	}
	if err := checkPriority(priority); err != nil {
		return nil, err
	}
	if err := validateAssignee(c, in.assigneeID, in.assigneeAgentID); err != nil {
		return nil, err
	}
	if err := assertInSpace(c, "projects.sprints", in.sprint, spaceID); err != nil {
		return nil, err
	}
	if err := assertInSpace(c, "projects.versions", in.fixVer, spaceID); err != nil {
		return nil, err
	}
	if err := assertInSpace(c, "projects.components", in.component, spaceID); err != nil {
		return nil, err
	}

	noteID, err := notes.InsertNote(c, spaceID, title, in.parentID, in.description, []string{itemType})
	if err != nil {
		return nil, err
	}
	// The row lock on the space serialises concurrent creators, so keys are unique and sequential.
	var number int32
	var spaceKey string
	if err := c.Tx.QueryRow(c.Context, "update core.spaces set item_seq = item_seq + 1 where id = $1::uuid returning item_seq, key", spaceID).Scan(&number, &spaceKey); err != nil {
		return nil, command.DB(err)
	}
	var maxRank *string
	if err := c.Tx.QueryRow(c.Context, `select max(rank collate "C") from projects.items where space_id = $1::uuid`, spaceID).Scan(&maxRank); err != nil {
		return nil, command.DB(err)
	}
	status := wf.Def.Initial()
	key := fmt.Sprintf("%s-%d", spaceKey, number)
	triage := c.Principal.IsMachine()
	if in.triage != nil {
		triage = *in.triage
	}
	custom := json.RawMessage(`{}`)
	if in.custom != nil {
		custom = in.custom
	}
	if _, err := c.Tx.Exec(c.Context,
		`insert into projects.items (note_id, workspace_id, space_id, number, key, item_type, status, status_category,
           priority, assignee_id, assignee_agent_id, reporter_id, sprint_id, fix_version_id, component_id, estimate,
           due_date, labels, custom, rank, workflow_version, triage)
         values ($1::uuid,$2::uuid,$3::uuid,$4::int4,$5::text,$6::text,$7::text,$8::text,$9::text,$10::uuid,$11::uuid,$12::uuid,$13::uuid,$14::uuid,$15::uuid,$16::float8::numeric,$17::text::date,$18::text[],$19::jsonb,$20::text,$21::int4,$22::bool)`,
		noteID, c.WorkspaceID(), spaceID, number, key, itemType, status.Name, status.Category,
		priority, in.assigneeID, in.assigneeAgentID, c.Principal.UserID, in.sprint, in.fixVer, in.component, in.estimate,
		in.dueDate, in.labels, custom, RankBetween(maxRank, nil), wf.Version, triage); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("item.created", &noteID, map[string]any{"key": key, "item_type": itemType, "status": status.Name, "space_id": spaceID, "triage": triage}); err != nil {
		return nil, err
	}
	if in.assigneeAgentID != nil {
		if _, err := c.Emit("item.assigned_to_agent", &noteID, map[string]any{"key": key, "agent_id": *in.assigneeAgentID}); err != nil {
			return nil, err
		}
	}
	return LoadItem(c, idRef(noteID), false)
}

func checkType(d *Definition, t string) error {
	if len(d.Types) > 0 && !slices.Contains(d.Types, t) {
		return command.ErrInvalid(fmt.Sprintf("item type '%s' is not allowed here; use one of %s", t, strings.Join(d.Types, ", ")))
	}
	return nil
}

// ---------- get ----------

type itemDetail struct {
	*Item
	Transitions []any `json:"transitions"`
	Evidence    []any `json:"evidence"`
}

func itemGet(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	ref, err := refOf(o)
	if err != nil {
		return nil, err
	}
	item, err := LoadItem(c, ref, false)
	if err != nil {
		return nil, err
	}
	wf, err := SpaceWF(c, item.SpaceID)
	if err != nil {
		return nil, err
	}
	def, err := DefinitionFor(c, item.SpaceID, wf, item.WorkflowVersion)
	if err != nil {
		return nil, err
	}
	transitions := []any{}
	for _, t := range def.AllowedFrom(item.Status) {
		g := t.Guards
		if g == nil {
			g = []Guard{}
		}
		transitions = append(transitions, map[string]any{"name": t.Name, "to": t.To, "guards": g})
	}
	rows, err := c.Tx.Query(c.Context, "select kind, source, url, created_at from projects.evidence where note_id = $1::uuid order by created_at", item.ID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	evidence := []any{}
	for rows.Next() {
		var kind, source string
		var url *string
		var at time.Time
		if err := rows.Scan(&kind, &source, &url, &at); err != nil {
			return nil, command.DB(err)
		}
		evidence = append(evidence, map[string]any{"kind": kind, "source": source, "url": url, "at": command.Ts(at)})
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	return itemDetail{item, transitions, evidence}, nil
}

// ---------- update ----------

func samePtr[T comparable](a, b *T) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func itemUpdate(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	ref, err := refOf(o)
	if err != nil {
		return nil, err
	}
	var title, description, priority, itemTypeIn, dueDate, sprint, fixVer, component *string
	for _, f := range []struct {
		k string
		p **string
	}{{"title", &title}, {"description", &description}, {"priority", &priority}, {"item_type", &itemTypeIn}} {
		if *f.p, err = o.optStr(f.k); err != nil {
			return nil, err
		}
	}
	for _, f := range []struct {
		k string
		p **string
	}{{"sprint_id", &sprint}, {"fix_version_id", &fixVer}, {"component_id", &component}} {
		if *f.p, err = o.optUUID(f.k); err != nil {
			return nil, err
		}
	}
	labels, hasLabels, err := o.optStrVec("labels")
	if err != nil {
		return nil, err
	}
	estimate, err := o.optF64("estimate")
	if err != nil {
		return nil, err
	}
	if dueDate, err = o.optDate("due_date"); err != nil {
		return nil, err
	}
	custom, err := o.optValue("custom")
	if err != nil {
		return nil, err
	}
	clear, err := o.strVecDef("clear")
	if err != nil {
		return nil, err
	}

	before, err := LoadItem(c, ref, true)
	if err != nil {
		return nil, err
	}
	if priority != nil {
		if err := checkPriority(*priority); err != nil {
			return nil, err
		}
	}
	if custom != nil && custom[0] != '{' {
		return nil, command.ErrInvalid("custom must be an object")
	}
	for _, cl := range clear {
		if !slices.Contains([]string{"estimate", "due_date", "sprint", "fix_version", "component"}, cl) {
			return nil, command.ErrInvalid(fmt.Sprintf("cannot clear '%s'", cl))
		}
	}
	var itemType *string
	if itemTypeIn != nil {
		t := rustLower(strings.TrimSpace(*itemTypeIn))
		wf, err := SpaceWF(c, before.SpaceID)
		if err != nil {
			return nil, err
		}
		if err := checkType(wf.Def, t); err != nil {
			return nil, err
		}
		itemType = &t
	}
	if err := assertInSpace(c, "projects.sprints", sprint, before.SpaceID); err != nil {
		return nil, err
	}
	if err := assertInSpace(c, "projects.versions", fixVer, before.SpaceID); err != nil {
		return nil, err
	}
	if err := assertInSpace(c, "projects.components", component, before.SpaceID); err != nil {
		return nil, err
	}
	if title != nil || description != nil {
		if _, err := c.Call("note.update", map[string]any{"id": before.ID, "title": title, "content_md": description}); err != nil {
			return nil, err
		}
	}
	var labelsArg any
	if hasLabels {
		if labels == nil {
			labels = []string{}
		}
		labelsArg = labels
	}
	var customArg any
	if custom != nil {
		customArg = custom
	}
	if _, err := c.Tx.Exec(c.Context,
		`update projects.items set
           priority = coalesce($2::text, priority),
           labels = coalesce($3::text[], labels),
           estimate = case when 'estimate' = any($4::text[]) then null else coalesce($5::float8::numeric, estimate) end,
           due_date = case when 'due_date' = any($4::text[]) then null else coalesce($6::text::date, due_date) end,
           sprint_id = case when 'sprint' = any($4::text[]) then null else coalesce($7::uuid, sprint_id) end,
           fix_version_id = case when 'fix_version' = any($4::text[]) then null else coalesce($8::uuid, fix_version_id) end,
           component_id = case when 'component' = any($4::text[]) then null else coalesce($9::uuid, component_id) end,
           custom = custom || coalesce($10::jsonb, '{}'::jsonb),
           item_type = coalesce($11::text, item_type)
         where note_id = $1::uuid`,
		before.ID, priority, labelsArg, clear, estimate, dueDate, sprint, fixVer, component, customArg, itemType); err != nil {
		return nil, command.DB(err)
	}
	if itemType != nil {
		if _, err := c.Tx.Exec(c.Context, "update core.notes set types = array[$2::text] where id = $1::uuid", before.ID, *itemType); err != nil {
			return nil, command.DB(err)
		}
	}
	after, err := LoadItem(c, idRef(before.ID), false)
	if err != nil {
		return nil, err
	}
	changed := []string{}
	for _, d := range []struct {
		name string
		diff bool
	}{
		{"title", before.Title != after.Title},
		{"description", before.Description != after.Description},
		{"priority", before.Priority != after.Priority},
		{"labels", !slices.Equal(before.Labels, after.Labels)},
		{"estimate", !samePtr(before.Estimate, after.Estimate)},
		{"due_date", !samePtr(before.DueDate, after.DueDate)},
		{"sprint_id", !samePtr(before.SprintID, after.SprintID)},
		{"fix_version_id", !samePtr(before.FixVersionID, after.FixVersionID)},
		{"component_id", !samePtr(before.ComponentID, after.ComponentID)},
		{"item_type", before.ItemType != after.ItemType},
		{"custom", string(before.Custom) != string(after.Custom)},
	} {
		if d.diff {
			changed = append(changed, d.name)
		}
	}
	if len(changed) > 0 {
		if _, err := c.Emit("item.updated", &after.ID, map[string]any{"key": after.Key, "changed": changed}); err != nil {
			return nil, err
		}
	}
	return after, nil
}

// ---------- transition ----------

func setField(c *command.Ctx, id, field string, v json.RawMessage) error {
	bad := func(m string) error { return command.ErrInvalid(fmt.Sprintf("set_field %s: %s", field, m)) }
	var err error
	switch field {
	case "priority":
		var p string
		if json.Unmarshal(v, &p) != nil || v[0] != '"' {
			return bad("expected a string")
		}
		if err := checkPriority(p); err != nil {
			return err
		}
		_, err = c.Tx.Exec(c.Context, "update projects.items set priority = $2::text where note_id = $1::uuid", id, p)
	case "estimate":
		var est *float64
		if !isNull(v) {
			var f float64
			if json.Unmarshal(v, &f) != nil || v[0] == '"' {
				return bad("expected a number")
			}
			est = &f
		}
		_, err = c.Tx.Exec(c.Context, "update projects.items set estimate = $2::float8::numeric where note_id = $1::uuid", id, est)
	case "due_date":
		var d *pgtype.Date
		switch {
		case isNull(v):
		case v[0] == '"':
			var s string
			_ = json.Unmarshal(v, &s)
			p, ok := chronoParse(s, []numItem{{kind: 'Y'}, {kind: ' '}, {kind: 'l', lit: "-"}, {kind: 'm'}, {kind: ' '}, {kind: 'l', lit: "-"}, {kind: 'd'}, {kind: ' '}})
			if !ok {
				return bad("expected YYYY-MM-DD")
			}
			d = &pgtype.Date{Time: time.Date(p.y, time.Month(p.mo), p.d, 0, 0, 0, 0, time.UTC), Valid: true}
		default:
			return bad("expected YYYY-MM-DD")
		}
		_, err = c.Tx.Exec(c.Context, "update projects.items set due_date = $2::date where note_id = $1::uuid", id, d)
	case "labels":
		l, ok := asStrs(v, false)
		if !ok {
			return bad("expected an array of strings")
		}
		_, err = c.Tx.Exec(c.Context, "update projects.items set labels = $2::text[] where note_id = $1::uuid", id, l)
	default:
		nv, nerr := notes.NormValue(v)
		if nerr != nil {
			return nerr
		}
		_, err = c.Tx.Exec(c.Context, "update projects.items set custom = custom || jsonb_build_object($2::text, $3::jsonb) where note_id = $1::uuid", id, field, nv)
	}
	return command.DB(err)
}

// TransitionItem applies the workflow transition from the item's current status to target (status or transition name).
func TransitionItem(c *command.Ctx, item *Item, target string) (*Item, error) {
	wf, err := SpaceWF(c, item.SpaceID)
	if err != nil {
		return nil, err
	}
	def, err := DefinitionFor(c, item.SpaceID, wf, item.WorkflowVersion)
	if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, "select distinct kind from projects.evidence where note_id = $1::uuid", item.ID)
	if err != nil {
		return nil, command.DB(err)
	}
	evidence, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, command.DB(err)
	}
	t, err := def.Pick(item.Status, target, GuardInput{Role: c.Principal.Role, Filled: FilledFields(item), Evidence: evidence})
	if err != nil {
		return nil, command.ErrInvalid(err.Error())
	}
	to := def.Status(t.To)
	if to == nil {
		return nil, command.NewError(command.Internal, "validated definition")
	}
	if _, err := c.Tx.Exec(c.Context,
		`update projects.items set status = $2::text, status_category = $3::text,
           resolved_at = case when $3::text = 'done' then coalesce(resolved_at, now()) else null end
         where note_id = $1::uuid`, item.ID, to.Name, to.Category); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Tx.Exec(c.Context, "update core.notes set updated_at = now(), version = version + 1 where id = $1::uuid", item.ID); err != nil {
		return nil, command.DB(err)
	}
	for _, a := range t.PostActions {
		switch a.Type {
		case "set_field":
			if err := setField(c, item.ID, a.Field, a.Value); err != nil {
				return nil, err
			}
		case "assign_reporter":
			if _, err := c.Tx.Exec(c.Context, "update projects.items set assignee_id = reporter_id, assignee_agent_id = null where note_id = $1::uuid and reporter_id is not null", item.ID); err != nil {
				return nil, command.DB(err)
			}
		case "emit":
			if _, err := c.Emit(a.Event, &item.ID, map[string]any{"key": item.Key, "from": item.Status, "to": to.Name}); err != nil {
				return nil, err
			}
		}
	}
	if _, err := c.Emit("item.transitioned", &item.ID, map[string]any{
		"key": item.Key, "from": item.Status, "to": to.Name, "from_category": item.StatusCategory,
		"category": to.Category, "transition": t.Name, "workflow_version": item.WorkflowVersion,
		"space_id": item.SpaceID, "sprint_id": item.SprintID, "estimate": item.Estimate,
	}); err != nil {
		return nil, err
	}
	return LoadItem(c, idRef(item.ID), false)
}

func itemTransition(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	ref, err := refOf(o)
	if err != nil {
		return nil, err
	}
	to, err := o.reqStr("to")
	if err != nil {
		return nil, err
	}
	item, err := LoadItem(c, ref, true)
	if err != nil {
		return nil, err
	}
	return TransitionItem(c, item, to)
}

// ---------- assign ----------

func assignItem(c *command.Ctx, ref ItemRef, user, agent *string) (any, error) {
	before, err := LoadItem(c, ref, true)
	if err != nil {
		return nil, err
	}
	if err := validateAssignee(c, user, agent); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "update projects.items set assignee_id = $2::uuid, assignee_agent_id = $3::uuid where note_id = $1::uuid", before.ID, user, agent); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Tx.Exec(c.Context, "update core.notes set updated_at = now(), version = version + 1 where id = $1::uuid", before.ID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("item.assigned", &before.ID, map[string]any{
		"key": before.Key, "assignee_id": user, "assignee_agent_id": agent,
		"previous_assignee_id": before.AssigneeID, "previous_assignee_agent_id": before.AssigneeAgentID,
	}); err != nil {
		return nil, err
	}
	if agent != nil {
		if _, err := c.Emit("item.assigned_to_agent", &before.ID, map[string]any{"key": before.Key, "agent_id": *agent, "title": before.Title}); err != nil {
			return nil, err
		}
	}
	return LoadItem(c, idRef(before.ID), false)
}

func itemAssign(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	ref, err := refOf(o)
	if err != nil {
		return nil, err
	}
	user, err := o.optUUID("user_id")
	if err != nil {
		return nil, err
	}
	agent, err := o.optUUID("agent_id")
	if err != nil {
		return nil, err
	}
	return assignItem(c, ref, user, agent)
}

// ---------- list ----------

func cnqlCx(c *command.Ctx) *Cx { return &Cx{Now: time.Now().UTC(), User: c.Principal.UserID} }

func compileQuery(c *command.Ctx, query string, first int) (*Compiled, error) {
	q, perr := ParseCNQL(query)
	if perr != nil {
		return nil, command.ErrInvalid(perr.Error())
	}
	comp, cerr := CompileCNQL(q, cnqlCx(c), first)
	if cerr != nil {
		return nil, command.ErrInvalid(cerr.Error())
	}
	return comp, nil
}

func itemList(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	q, err := o.optStr("cnql")
	if err != nil {
		return nil, err
	}
	limit, err := o.optI64("limit")
	if err != nil {
		return nil, err
	}
	query := ""
	if q != nil {
		query = *q
	}
	return listWith(c, query, "true", limit)
}

// listWith runs a CNQL query over the items the caller may read; extra is a static SQL predicate.
func listWith(c *command.Ctx, query, extra string, limit *int64) ([]*Item, error) {
	comp, err := compileQuery(c, query, 5)
	if err != nil {
		return nil, err
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	lim := int64(50)
	if limit != nil {
		lim = *limit
	}
	lim = max(1, min(200, lim))
	sql := itemSelect + ` where n.deleted_at is null and i.space_id = any($1::text[]::uuid[]) and core.note_access(n.id, $3::uuid, $4::bool, $1::text[]::uuid[]) <> 'none'
           and ` + extra + ` and (` + comp.Where + `) order by ` + comp.Order + ` limit $2::int8`
	rows, err := c.Tx.Query(c.Context, sql, append([]any{spaces, lim, uid, admin}, comp.Args()...)...)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []*Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, command.DB(err)
		}
		out = append(out, it)
	}
	return out, command.DB(rows.Err())
}

// ItemMatches reports whether the item matches a CNQL query (automation triggers and conditions).
func ItemMatches(c *command.Ctx, noteID, query string) (bool, error) {
	comp, err := compileQuery(c, query, 2)
	if err != nil {
		return false, err
	}
	var ok bool
	err = c.Tx.QueryRow(c.Context, "select exists(select 1 from "+FromSQL+" where i.note_id = $1::uuid and ("+comp.Where+"))",
		append([]any{noteID}, comp.Args()...)...).Scan(&ok)
	return ok, command.DB(err)
}

// MatchingItemIDs are the items matching a CNQL query inside one space (schedule triggers).
func MatchingItemIDs(c *command.Ctx, space *string, query string, limit int64) ([]string, error) {
	comp, err := compileQuery(c, query, 3)
	if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context,
		"select i.note_id::text from "+FromSQL+" where n.deleted_at is null and ($1::uuid is null or i.space_id = $1::uuid) and ("+comp.Where+") order by "+comp.Order+" limit $2::int8",
		append([]any{space, limit}, comp.Args()...)...)
	if err != nil {
		return nil, command.DB(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if ids == nil {
		ids = []string{}
	}
	return ids, command.DB(err)
}

// ---------- rank ----------

func neighbourRank(c *command.Ctx, space, r string) (string, error) {
	var rank string
	err := c.Tx.QueryRow(c.Context, "select rank from projects.items where space_id = $1::uuid and (note_id::text = $2::text or key = upper($2::text))", space, r).Scan(&rank)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", command.ErrNotFound(fmt.Sprintf("item %s in this space", r))
	}
	return rank, command.DB(err)
}

func itemRank(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	ref, err := refOf(o)
	if err != nil {
		return nil, err
	}
	after, err := o.optStr("after")
	if err != nil {
		return nil, err
	}
	before, err := o.optStr("before")
	if err != nil {
		return nil, err
	}
	item, err := LoadItem(c, ref, true)
	if err != nil {
		return nil, err
	}
	var lo, hi *string
	switch {
	case after == nil && before == nil:
		return nil, command.ErrInvalid("give after and/or before")
	case after != nil && before != nil:
		l, err := neighbourRank(c, item.SpaceID, *after)
		if err != nil {
			return nil, err
		}
		h, err := neighbourRank(c, item.SpaceID, *before)
		if err != nil {
			return nil, err
		}
		lo, hi = &l, &h
	case after != nil:
		l, err := neighbourRank(c, item.SpaceID, *after)
		if err != nil {
			return nil, err
		}
		lo = &l
		if err := c.Tx.QueryRow(c.Context,
			`select min(rank collate "C") from projects.items where space_id = $1::uuid and note_id <> $3::uuid and rank collate "C" > $2::text collate "C"`,
			item.SpaceID, l, item.ID).Scan(&hi); err != nil {
			return nil, command.DB(err)
		}
	default:
		h, err := neighbourRank(c, item.SpaceID, *before)
		if err != nil {
			return nil, err
		}
		hi = &h
		if err := c.Tx.QueryRow(c.Context,
			`select max(rank collate "C") from projects.items where space_id = $1::uuid and note_id <> $3::uuid and rank collate "C" < $2::text collate "C"`,
			item.SpaceID, h, item.ID).Scan(&lo); err != nil {
			return nil, command.DB(err)
		}
	}
	if lo != nil && hi != nil && *lo > *hi {
		return nil, command.ErrInvalid("'after' ranks below 'before'")
	}
	rank := RankBetween(lo, hi)
	if _, err := c.Tx.Exec(c.Context, "update projects.items set rank = $2::text where note_id = $1::uuid", item.ID, rank); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("item.ranked", &item.ID, map[string]any{"key": item.Key, "rank": rank}); err != nil {
		return nil, err
	}
	return LoadItem(c, idRef(item.ID), false)
}

// ---------- triage ----------

func triageList(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	limit, err := o.optI64("limit")
	if err != nil {
		return nil, err
	}
	return listWith(c, "ORDER BY created", "i.triage", limit)
}

func triageAccept(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	ref, err := refOf(o)
	if err != nil {
		return nil, err
	}
	priority, err := o.optStr("priority")
	if err != nil {
		return nil, err
	}
	user, err := o.optUUID("assignee_id")
	if err != nil {
		return nil, err
	}
	agent, err := o.optUUID("assignee_agent_id")
	if err != nil {
		return nil, err
	}
	item, err := LoadItem(c, ref, true)
	if err != nil {
		return nil, err
	}
	if !item.Triage {
		return nil, command.ErrConflict("item is not in triage")
	}
	if priority != nil {
		if err := checkPriority(*priority); err != nil {
			return nil, err
		}
	}
	if _, err := c.Tx.Exec(c.Context, "update projects.items set triage = false, priority = coalesce($2::text, priority) where note_id = $1::uuid", item.ID, priority); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("item.triage_accepted", &item.ID, map[string]any{"key": item.Key}); err != nil {
		return nil, err
	}
	if user != nil || agent != nil {
		return assignItem(c, idRef(item.ID), user, agent)
	}
	return LoadItem(c, idRef(item.ID), false)
}
