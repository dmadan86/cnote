package notes

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/notify"
)

const defaultVerifyDays = 90

func withDescribe(c command.Command) command.Command {
	c.Description, c.Schema = A2Describe(c.Name)
	return c
}

func writeCommands() []command.Command {
	guest := func(c command.Command) command.Command { c.MinRole = command.Guest; return c } // page access (shares) is checked per note
	w, d, r := command.GroupWriteNotes, command.GroupDestructive, command.GroupRead
	return []command.Command{
		command.NewCommand("space.create", w, spaceCreate),
		command.NewCommand("note.create", w, noteCreate),
		guest(command.NewCommand("note.update", w, noteUpdate)),
		command.NewCommand("note.list", r, noteList),
		withDescribe(command.NewCommand("note.delete", d, noteDelete)),
		command.NewCommand("relation.add", w, relationAdd),
		command.NewCommand("relation.remove", w, relationRemove),
		guest(command.NewCommand("comment.add", w, commentAdd)),
		command.NewCommand("comment.list", r, commentList),
		guest(command.NewCommand("comment.resolve", w, commentResolve)),
		command.NewCommand("search.notes", r, searchNotes),
		command.NewCommand("view.create", w, viewCreate),
		command.NewCommand("view.list", r, viewList),
	}
}

// ---------- space.create ----------

// Rust's to_uppercase uses full case mapping; these are the multi-letter ASCII results Go's rune mapping lacks.
var fullUpper = strings.NewReplacer("ß", "SS", "ﬀ", "FF", "ﬁ", "FI", "ﬂ", "FL", "ﬃ", "FFI", "ﬄ", "FFL", "ﬅ", "ST", "ﬆ", "ST")

func spaceCreate(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	key, err := o.reqStr("key")
	if err != nil {
		return nil, err
	}
	name, err := o.reqStr("name")
	if err != nil {
		return nil, err
	}
	template, err := o.optStr("template")
	if err != nil {
		return nil, err
	}
	visibility, err := o.optStr("visibility")
	if err != nil {
		return nil, err
	}
	key = strings.ToUpper(fullUpper.Replace(strings.TrimSpace(key)))
	ok := key != "" && len(key) <= 10
	for _, ch := range key {
		ok = ok && (ch >= '0' && ch <= '9' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z')
	}
	if !ok {
		return nil, command.ErrInvalid("key must be 1-10 letters or digits")
	}
	tpl, vis := "blank", "open"
	if template != nil {
		tpl = *template
	}
	if visibility != nil {
		vis = *visibility
	}
	var s Space
	var cfg []byte
	if err := c.Tx.QueryRow(c.Context,
		`insert into core.spaces (workspace_id, key, name, template, visibility) values ($1::uuid,$2,$3,$4,$5)
		 returning id::text, key, name, template, visibility, config`,
		c.WorkspaceID(), key, strings.TrimSpace(name), tpl, vis).Scan(&s.ID, &s.Key, &s.Name, &s.Template, &s.Visibility, &cfg); err != nil {
		return nil, err
	}
	if s.Config, err = command.Norm(cfg); err != nil {
		return nil, err
	}
	if s.Visibility == "private" && c.Principal.UserID != nil {
		if _, err := c.Tx.Exec(c.Context,
			"insert into core.space_members (workspace_id, space_id, user_id, role) values ($1::uuid,$2::uuid,$3::uuid,'admin')",
			c.WorkspaceID(), s.ID, c.Principal.UserID); err != nil {
			return nil, err
		}
	}
	if _, err := c.Emit("space.created", nil, map[string]any{"space_id": s.ID, "key": s.Key, "template": s.Template}); err != nil {
		return nil, err
	}
	return s, nil
}

// ---------- note.create ----------

type createNote struct {
	spaceID, title                string
	parentID, content, owner, vby *string
	types                         []string
}

func noteCreate(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	var i createNote
	if i.spaceID, err = o.reqUUID("space_id"); err != nil {
		return nil, err
	}
	if i.title, err = o.reqStr("title"); err != nil {
		return nil, err
	}
	if i.parentID, err = o.optUUID("parent_id"); err != nil {
		return nil, err
	}
	if i.content, err = o.optStr("content_md"); err != nil {
		return nil, err
	}
	if i.types, err = o.types("types"); err != nil {
		return nil, err
	}
	if i.owner, err = o.optUUID("owner_id"); err != nil {
		return nil, err
	}
	if i.vby, err = o.optDate("verify_by"); err != nil {
		return nil, err
	}
	return insertNote(c, i)
}

// InsertTypedNote reuses the note creation access checks, ownership and outbox behavior.
func InsertTypedNote(c *command.Ctx, spaceID, title, typ string, owner *string) (Note, error) {
	v, err := insertNote(c, createNote{spaceID: spaceID, title: title, types: []string{typ}, owner: owner})
	if err != nil {
		return Note{}, err
	}
	return v.(Note), nil
}

func parentSpace(c *command.Ctx, parent string) (string, error) {
	var s string
	err := c.Tx.QueryRow(c.Context, "select space_id::text from core.notes where id = $1::uuid and deleted_at is null", parent).Scan(&s)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", command.ErrNotFound("parent")
	}
	return s, err
}

func insertNote(c *command.Ctx, i createNote) (any, error) {
	if err := access.AssertSpaceAccess(c, i.spaceID, true); err != nil {
		return nil, err
	}
	if err := lockTree(c, i.spaceID); err != nil { // before reading the parent: a delete or move of it finishes first
		return nil, err
	}
	if i.parentID != nil {
		// a child inherits the parent's restrictions, so creating one is an edit of the parent
		if _, err := access.AssertNoteAccess(c, *i.parentID, true); err != nil {
			return nil, err
		}
		ps, err := parentSpace(c, *i.parentID)
		if err != nil {
			return nil, err
		}
		if ps != i.spaceID {
			return nil, command.ErrInvalid("a page's parent must be in the same space")
		}
		if d, _, err := chainDepth(c, i.parentID); err != nil {
			return nil, err
		} else if d+1 > maxTreeDepth {
			return nil, errTooDeep()
		}
	}
	owner := i.owner
	if owner == nil {
		owner = c.Principal.UserID
	}
	vby := i.vby
	if vby == nil && len(i.types) == 0 {
		d := time.Now().UTC().Add(defaultVerifyDays * 24 * time.Hour).Format("2006-01-02")
		vby = &d
	}
	content := ""
	if i.content != nil {
		content = *i.content
	}
	n, err := scanNote(c.Tx.QueryRow(c.Context,
		`insert into core.notes (workspace_id, space_id, parent_id, title, types, content_md, owner_id, verify_by, created_by)
		 values ($1::uuid,$2::uuid,$3::uuid,$4,$5::text[],$6,$7::uuid,$8::date,$9::uuid) returning `+noteCols,
		c.WorkspaceID(), i.spaceID, i.parentID, strings.TrimSpace(i.title), i.types, content, owner, vby, c.Principal.UserID))
	if err != nil {
		return nil, err
	}
	if n.ParentID != nil {
		if err := addRelation(c, *n.ParentID, n.ID, "child", nil); err != nil {
			return nil, err
		}
	}
	if _, err := c.Emit("note.created", &n.ID, map[string]any{"title": n.Title, "types": n.Types, "space_id": n.SpaceID}); err != nil {
		return nil, err
	}
	// The mention store starts with the body the page was created with; mentions typed later are diffed against it.
	if err := SeedMentionState(c, n.ID, c.WorkspaceID(), n.ContentMD, n.Version); err != nil {
		return nil, err
	}
	return n, nil
}

// ---------- note.update ----------

func noteUpdate(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.reqUUID("id")
	if err != nil {
		return nil, err
	}
	title, err := o.optStr("title")
	if err != nil {
		return nil, err
	}
	content, err := o.optStr("content_md")
	if err != nil {
		return nil, err
	}
	owner, err := o.optUUID("owner_id")
	if err != nil {
		return nil, err
	}
	vby, err := o.optDate("verify_by")
	if err != nil {
		return nil, err
	}
	parent, err := o.optUUID("parent_id")
	if err != nil {
		return nil, err
	}
	expV, err := o.optInt("expected_version", 32)
	if err != nil {
		return nil, err
	}
	expE, err := o.optInt("expected_epoch", 32)
	if err != nil {
		return nil, err
	}
	schema, err := o.optInt("schema", 32)
	if err != nil {
		return nil, err
	}

	pc, err := parsePageChrome(o)
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	if err := fenceBodySave(c, content != nil, schema, expE); err != nil {
		return nil, err
	}
	before, err := loadNote(c, id)
	if err != nil {
		return nil, err
	}
	moving := parent != nil && !samePtr(parent, before.ParentID)
	if moving { // a parent change is a move: same gate, same subtree and restriction checks (7.10.2)
		if err := requireStructural(c); err != nil {
			return nil, err
		}
	}
	if expV != nil && int32(*expV) != before.Version {
		return nil, command.ErrConflict("note changed (version " + strconv.Itoa(int(before.Version)) + " != " + strconv.FormatInt(*expV, 10) + ")")
	}
	if expE != nil && content != nil && int32(*expE) != before.CollabEpoch {
		return nil, command.ErrConflict("the page was restored since this copy was opened")
	}
	if title != nil {
		t := strings.TrimSpace(*title)
		title = &t
	}
	if moving {
		if _, err := movePage(c, moveInput{id: id, parent: parent, space: before.SpaceID}); err != nil {
			return nil, err
		}
	}
	n, err := scanNote(c.Tx.QueryRow(c.Context,
		`update core.notes set title = coalesce($2::text, title), content_md = coalesce($3::text, content_md),
		   owner_id = coalesce($4::uuid, owner_id), verify_by = coalesce($5::date, verify_by),
		   page_status = case when $6::bool then $7::text else page_status end,
		   icon = case when $8::bool then $9::text else icon end, wide = coalesce($10::bool, wide),
		   updated_at = now(), version = version + 1
		 where id = $1::uuid returning `+noteCols, id, title, content, owner, vby,
		pc.setStatus, pc.status, pc.setIcon, pc.icon, pc.wide))
	if err != nil {
		return nil, err
	}
	changed := []string{}
	if n.Title != before.Title {
		changed = append(changed, "title")
	}
	if n.ContentMD != before.ContentMD {
		changed = append(changed, "content")
	}
	if !samePtr(n.OwnerID, before.OwnerID) {
		changed = append(changed, "owner")
	}
	if !samePtr(n.VerifyBy, before.VerifyBy) {
		changed = append(changed, "verify_by")
	}
	if !samePtr(n.ParentID, before.ParentID) {
		changed = append(changed, "parent")
	}
	if !samePtr(n.Status, before.Status) {
		changed = append(changed, "status")
	}
	if !samePtr(n.Icon, before.Icon) {
		changed = append(changed, "icon")
	}
	if n.Wide != before.Wide {
		changed = append(changed, "wide")
	}
	if _, err := c.Emit("note.updated", &n.ID, map[string]any{"changed": changed, "version": n.Version}); err != nil {
		return nil, err
	}
	if n.ContentMD != before.ContentMD {
		if err := checkpoint(c, n.ID, c.Principal.UserID, checkpointEverySecs); err != nil {
			return nil, err
		}
		if err := NotifyPageMentions(c, n.ID, &before.ContentMD); err != nil {
			return nil, err
		}
	}
	return n, nil
}

func samePtr(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

// Seconds between automatic page-version checkpoints while someone keeps editing.
const checkpointEverySecs = 600.0

// checkpoint records the page's current content as a version unless it is empty, unchanged since the last
// version, or the last version is younger than minGapSecs.
func checkpoint(c *command.Ctx, note string, actor *string, minGapSecs float64) error {
	_, err := c.Tx.Exec(c.Context,
		`insert into core.note_revisions (workspace_id, note_id, number, title, content_md, reason, created_by)
		 select n.workspace_id, n.id, coalesce((select max(number) from core.note_revisions r where r.note_id = n.id), 0) + 1,
		        n.title, n.content_md, 'edit', $2::uuid
		 from core.notes n
		 where n.id = $1::uuid and n.content_md <> ''
		   and n.content_md is distinct from
		       (select content_md from core.note_revisions r where r.note_id = n.id order by number desc limit 1)
		   and not exists (select 1 from core.note_revisions r
		                   where r.note_id = n.id and r.created_at > now() - make_interval(secs => $3::float8))`,
		note, actor, minGapSecs)
	return err
}

// ---------- mentions (notifications.rs notify_mentions) ----------

var mentionRe = regexp.MustCompile(`\]\(/w/([0-9a-fA-F-]{36})/people/([0-9a-fA-F-]{36})\)`)

// mentions lists the users linked in md for workspace ws, in order of first appearance.
func mentions(md, ws string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range mentionRe.FindAllStringSubmatch(md, -1) {
		if !uuidRe.MatchString(m[1]) || !uuidRe.MatchString(m[2]) || strings.ToLower(m[1]) != ws {
			continue
		}
		if u := strings.ToLower(m[2]); !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// notifyMentions notifies people mentioned in newMD but not in oldMD (in-app notification + mention.created). It is the
// path for a comment, whose body is not a page version; a page goes through NotifyPageMentions (mentions.go).
// eventKey names the edit or comment that made the mention (see worker.pageNotifyOnEvent).
func notifyMentions(c *command.Ctx, noteID, title, oldMD, newMD, eventKey string) error {
	ws := strings.ToLower(c.WorkspaceID())
	old := map[string]bool{}
	for _, u := range mentions(oldMD, ws) {
		old[u] = true
	}
	var fresh []string
	for _, u := range mentions(newMD, ws) {
		if !old[u] {
			fresh = append(fresh, u)
		}
	}
	return notifyUsers(c, noteID, title, fresh, eventKey)
}

// notifyUsers sends the mention notification to each user who is not the actor and can read the page, then emits
// mention.created for hooks and agents (email is the mail sweep's job, from the notification row).
func notifyUsers(c *command.Ctx, noteID, title string, users []string, eventKey string) error {
	ws := strings.ToLower(c.WorkspaceID())
	for _, user := range users {
		if c.Principal.UserID != nil && strings.EqualFold(*c.Principal.UserID, user) {
			continue
		}
		// Member of this workspace who can read the page, judged with their own role and space access.
		var canRead bool
		if err := c.Tx.QueryRow(c.Context, "select "+notify.ReadableExpr("$1::uuid", "$2::uuid"), user, noteID).Scan(&canRead); err != nil {
			return err
		}
		if !canRead {
			continue
		}
		ids, err := notify.Send(c.Context, c.Tx, notify.Notification{
			WorkspaceID: ws, UserID: user, Kind: "mention", Title: title, NoteID: &noteID, ActorUserID: c.Principal.UserID, EventKey: &eventKey})
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			continue
		}
		if _, err := c.Emit("mention.created", &noteID, map[string]any{"notification_id": ids[0]}); err != nil {
			return err
		}
	}
	return nil
}

// ---------- note.list / note.delete ----------

func noteList(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	space, err := o.optUUID("space_id")
	if err != nil {
		return nil, err
	}
	parent, err := o.optUUID("parent_id")
	if err != nil {
		return nil, err
	}
	typ, err := o.optStr("type")
	if err != nil {
		return nil, err
	}
	lim, err := o.optInt("limit", 64)
	if err != nil {
		return nil, err
	}
	limit := int64(100)
	if lim != nil {
		limit = min(max(*lim, 1), 500)
	}
	ids, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		`select `+noteCols+` from core.notes
		 where deleted_at is null and freshness <> 'archived' and (space_id = any($1::text[]::uuid[]) or $3::uuid is not null)
		   and core.note_access(id, $6::uuid, $7::bool, $1::text[]::uuid[]) <> 'none'
		   and ($2::uuid is null or space_id = $2::uuid) and ($3::uuid is null or parent_id = $3::uuid)
		   and ($4::text is null or ($4::text = 'page' and types = '{}') or $4::text = any(types))
		 order by updated_at desc limit $5::int8`, ids, space, parent, typ, limit, uid, admin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Note{}
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ---------- relations ----------

func addRelation(c *command.Ctx, from, to, kind string, externalRef *string) error {
	_, err := c.Tx.Exec(c.Context,
		`insert into core.relations (workspace_id, from_id, to_id, kind, external_ref) values ($1::uuid,$2::uuid,$3::uuid,$4,$5)
		 on conflict (from_id, to_id, kind) do nothing`, c.WorkspaceID(), from, to, kind, externalRef)
	return err
}

func relationInput(input json.RawMessage) (from, to, kind string, err error) {
	o, err := parseObj(input)
	if err != nil {
		return
	}
	if from, err = o.reqUUID("from_id"); err != nil {
		return
	}
	if to, err = o.reqUUID("to_id"); err != nil {
		return
	}
	kind, err = o.reqStr("kind")
	return
}

func relationAdd(c *command.Ctx, input json.RawMessage) (any, error) {
	from, to, kind, err := relationInput(input)
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, from, true); err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, to, false); err != nil {
		return nil, err
	}
	if from == to {
		return nil, command.ErrInvalid("a note cannot relate to itself")
	}
	if err := addRelation(c, from, to, kind, nil); err != nil {
		return nil, err
	}
	if _, err := c.Emit("relation.added", &from, map[string]any{"to_id": to, "kind": kind}); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

func relationRemove(c *command.Ctx, input json.RawMessage) (any, error) {
	from, to, kind, err := relationInput(input)
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, from, true); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "delete from core.relations where from_id = $1::uuid and to_id = $2::uuid and kind = $3", from, to, kind); err != nil {
		return nil, err
	}
	if _, err := c.Emit("relation.removed", &from, map[string]any{"to_id": to, "kind": kind}); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

// ---------- views ----------

type View struct {
	ID      string          `json:"id"`
	SpaceID *string         `json:"space_id"`
	Name    string          `json:"name"`
	Layout  string          `json:"layout"`
	Query   string          `json:"query"`
	Options json.RawMessage `json:"options"`
}

func scanView(row pgx.Row) (any, error) {
	var v View
	var opts []byte
	if err := row.Scan(&v.ID, &v.SpaceID, &v.Name, &v.Layout, &v.Query, &opts); err != nil {
		return nil, err
	}
	var err error
	v.Options, err = normValue(opts)
	return v, err
}

func viewCreate(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	space, err := o.optUUID("space_id")
	if err != nil {
		return nil, err
	}
	name, err := o.reqStr("name")
	if err != nil {
		return nil, err
	}
	layout, err := o.reqStr("layout")
	if err != nil {
		return nil, err
	}
	query, err := o.reqStr("query")
	if err != nil {
		return nil, err
	}
	opts, err := o.optValue("options")
	if err != nil {
		return nil, err
	}
	if opts == nil {
		opts = []byte("{}")
	}
	if space != nil { // a saved view is written into a space: space access, and an archived space refuses it (13.2.2)
		if err := access.AssertSpaceAccess(c, *space, true); err != nil {
			return nil, err
		}
	}
	return scanView(c.Tx.QueryRow(c.Context,
		`insert into core.views (workspace_id, space_id, name, layout, query, options, created_by)
		 values ($1::uuid,$2::uuid,$3,$4,$5,$6::jsonb,$7::uuid) returning id::text, space_id::text, name, layout, query, options`,
		c.WorkspaceID(), space, name, layout, query, string(opts), c.Principal.UserID))
}

func viewList(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	space, err := o.optUUID("space_id")
	if err != nil {
		return nil, err
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context,
		`select id::text, space_id::text, name, layout, query, options from core.views
		 where ($1::uuid is null or space_id = $1::uuid) and (space_id is null or space_id = any($2::text[]::uuid[])) order by name`, space, spaces)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		v, err := scanView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// normValue is command.Norm for jsonb read back through serde_json::Value: integers keep their digits, every
// other number (fractions, exponents, integers beyond u64) is an f64 re-printed the way serde_json prints it.
func normValue(raw []byte) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return command.Marshal(serdeNumbers(v))
}

type f64 float64

func (f f64) MarshalJSON() ([]byte, error) {
	x := float64(f)
	if x == 0 {
		if math.Signbit(x) {
			return []byte("-0.0"), nil
		}
		return []byte("0.0"), nil
	}
	if a := math.Abs(x); a < 1e-5 || a >= 1e16 {
		s := strconv.FormatFloat(x, 'e', -1, 64) // 1e+20, 1.5e-07
		m, e, _ := strings.Cut(s, "e")
		exp, _ := strconv.Atoi(e)
		sign := "+"
		if exp < 0 {
			sign, exp = "-", -exp
		}
		return []byte(m + "e" + sign + strconv.Itoa(exp)), nil
	}
	s := strconv.FormatFloat(x, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return []byte(s), nil
}

func serdeNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		t := x.String()
		if !strings.ContainsAny(t, ".eE") {
			if _, err := strconv.ParseInt(t, 10, 64); err == nil {
				return x
			}
			if _, err := strconv.ParseUint(t, 10, 64); err == nil {
				return x
			}
		}
		f, _ := strconv.ParseFloat(t, 64)
		return f64(f)
	case []any:
		for i := range x {
			x[i] = serdeNumbers(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = serdeNumbers(x[k])
		}
	}
	return v
}
