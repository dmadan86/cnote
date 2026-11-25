package notes

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// note.tree (docs-parity C 5.4, 13.3.4, 13.5). One readable level of the page tree at a time. Every level is an inner query that
// holds the full access predicate, the cursor and the LIMIT; has_children is computed afterwards for those rows only, so a hidden
// page never makes a row, a cursor position or an arrow. The "Shared with you" level is built from the reader's own
// note_permissions rows: a page is readable while its parent is not only through a direct share, because a restriction or an
// unreadable space that hides the parent hides the child too, and a share on an ancestor makes the parent readable.

func init() { c1Handlers["note.tree"] = noteTree }

const (
	treeDefaultLimit = 100
	treeSharedRoot   = "shared"
	treeWindow       = 100 // rows on each side of the revealed page, so a level is cut at 200 rows
	treeMaxDepth     = 51
)

type TreeRow struct {
	ID          string     `json:"id"`
	SpaceID     string     `json:"space_id"`
	ParentID    *string    `json:"parent_id"`
	Title       string     `json:"title"`
	Icon        *string    `json:"icon"`
	Position    int64      `json:"position"`
	Status      *string    `json:"status"`
	Restriction string     `json:"restriction"`
	Freshness   string     `json:"freshness"`
	VerifyBy    *string    `json:"verify_by"`
	UpdatedAt   command.Ts `json:"updated_at"`
	HasChildren bool       `json:"has_children"`
}

type treeLevelOut struct {
	ParentID *string   `json:"parent_id"`
	Rows     []TreeRow `json:"rows"`
	Next     *string   `json:"next"`
	Prev     *string   `json:"prev"`
	Shared   *bool     `json:"shared,omitempty"`
}

// treeKey is a row's place in sibling order; the cursor is this triple (keyset), so a reorder between two calls neither repeats
// nor skips a row that did not move.
type treeKey struct {
	Pos int64
	At  time.Time
	ID  string
}

func (k treeKey) cursor() *string {
	b, _ := json.Marshal([]any{k.Pos, k.At.UTC().Format(time.RFC3339Nano), k.ID})
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

func parseCursor(s string) (treeKey, error) {
	var k treeKey
	b, err := base64.RawURLEncoding.DecodeString(s)
	var v []json.RawMessage
	if err != nil || json.Unmarshal(b, &v) != nil || len(v) != 3 {
		return k, command.ErrInvalid("invalid cursor")
	}
	var at string
	if json.Unmarshal(v[0], &k.Pos) != nil || json.Unmarshal(v[1], &at) != nil || json.Unmarshal(v[2], &k.ID) != nil || !uuidRe.MatchString(k.ID) {
		return k, command.ErrInvalid("invalid cursor")
	}
	if k.At, err = time.Parse(time.RFC3339Nano, at); err != nil {
		return k, command.ErrInvalid("invalid cursor")
	}
	k.ID = strings.ToLower(k.ID)
	return k, nil
}

type treeIn struct {
	SpaceID  string          `json:"space_id"`
	ParentID json.RawMessage `json:"parent_id"`
	After    string          `json:"after"`
	Before   string          `json:"before"`
	From     string          `json:"from"`
	Limit    int             `json:"limit"`
	Reveal   string          `json:"reveal"`
}

// treeScope is everything a level query needs about the reader.
type treeScope struct {
	c        *command.Ctx
	uid      *string
	admin    bool
	spaces   []string
	space    string
	readable bool // the reader can read the whole space (not just shared pages in it)
}

func noteTree(c *command.Ctx, input json.RawMessage) (any, error) {
	var in treeIn
	dec := json.NewDecoder(bytes.NewReader(input))
	if err := dec.Decode(&in); err != nil {
		return nil, command.ErrInvalid("invalid input")
	}
	for _, id := range []*string{&in.SpaceID, &in.Reveal} {
		if *id != "" && !uuidRe.MatchString(*id) {
			return nil, command.ErrInvalid("ids must be UUIDs")
		}
		*id = strings.ToLower(*id)
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	s := &treeScope{c: c, uid: uid, admin: admin, spaces: spaces}
	if in.Reveal != "" {
		return s.reveal(in.Reveal)
	}
	if in.SpaceID == "" {
		return nil, command.ErrInvalid("space_id or reveal is required")
	}
	if n := btoi(in.After != "") + btoi(in.Before != "") + btoi(in.From != ""); n > 1 {
		return nil, command.ErrInvalid("after, before and from cannot be combined")
	}
	s.space = in.SpaceID
	s.readable = slices.Contains(spaces, s.space)
	limit := in.Limit
	if limit <= 0 {
		limit = treeDefaultLimit
	}
	limit = min(limit, 200)
	var parent *string // nil = space root
	shared := false
	if raw := bytes.TrimSpace(in.ParentID); len(raw) > 0 && string(raw) != "null" {
		var p string
		if json.Unmarshal(raw, &p) != nil {
			return nil, command.ErrInvalid("parent_id must be a page id, null or \"shared\"")
		}
		if p == treeSharedRoot {
			shared = true
		} else if !uuidRe.MatchString(p) {
			return nil, command.ErrInvalid("parent_id must be a page id, null or \"shared\"")
		} else {
			p = strings.ToLower(p)
			parent = &p
		}
	}
	var cur *treeKey
	back := in.Before != ""
	if cs := in.After + in.Before + in.From; cs != "" {
		k, err := parseCursor(cs)
		if err != nil {
			return nil, err
		}
		cur = &k
	}
	rows, more, err := s.level(parent, shared, cur, back, in.From != "", limit)
	if err != nil {
		return nil, err
	}
	out := s.package_(rows, more, cur != nil, back, parent, shared)
	if parent == nil && !shared && cur == nil {
		sh, err := s.hasShared()
		if err != nil {
			return nil, err
		}
		out.Shared = &sh
	}
	return out, nil
}

// package_ turns the (ascending) rows of a page into a level with its cursors. more says there are rows beyond the page in the
// direction it was read; a page read from a cursor always has rows on the other side.
func (s *treeScope) package_(rows []treeRowKey, more, hasCur, back bool, parent *string, shared bool) treeLevelOut {
	out := treeLevelOut{Rows: make([]TreeRow, len(rows))}
	for i, r := range rows {
		out.Rows[i] = r.TreeRow
	}
	switch {
	case shared:
		p := treeSharedRoot
		out.ParentID = &p
	default:
		out.ParentID = parent
	}
	if len(rows) > 0 {
		if (!back && more) || (back && hasCur) {
			out.Next = rows[len(rows)-1].key.cursor()
		}
		if (back && more) || (!back && hasCur) {
			out.Prev = rows[0].key.cursor()
		}
	}
	return out
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

type treeRowKey struct {
	TreeRow
	key treeKey
}

// level reads one page of a level in sibling order. back reads the rows before the cursor (returned ascending); inclusive makes
// the cursor row part of a forward read. more reports whether another row exists past the page in the direction read.
func (s *treeScope) level(parent *string, shared bool, cur *treeKey, back, inclusive bool, limit int) ([]treeRowKey, bool, error) {
	// A level is a list of (from, cond) branches, each read in sibling order with the predicate and the LIMIT inside, then merged.
	type branch struct{ from, cond string }
	var branches []branch
	args := []any{s.space, s.uid, s.admin, s.spaces}
	switch {
	case shared:
		if s.uid == nil {
			return nil, false, nil
		}
		// Candidates come only from the reader's own permission rows (note_permissions_user), never from a scan of the space.
		branches = []branch{{`core.note_permissions pm join core.notes n on n.id = pm.note_id and n.space_id = $1::uuid`,
			`pm.user_id = $2::uuid and (
			(not $5::bool and (n.parent_id is null or exists (select 1 from core.notes h where h.id = n.parent_id and (h.deleted_at is not null or h.freshness = 'archived'))))
			or exists (select 1 from core.notes p where p.id = n.parent_id and p.deleted_at is null and p.freshness <> 'archived'
			           and core.note_access(p.id, $2::uuid, $3, $4::text[]::uuid[]) = 'none'))`}}
		args = append(args, s.readable)
	case parent != nil:
		branches = []branch{{`core.notes n`, `n.parent_id = $5::uuid`}}
		args = append(args, *parent)
	default:
		if !s.readable { // pages of a space the reader cannot open are only reachable through Shared with you
			return nil, false, nil
		}
		// Roots, and live pages whose parent is trashed or archived (found through notes_hidden_parents, not by scanning the
		// space). Two ordered branches stop early; one OR over the whole space sorts every page (13.5: 21 ms against 32 to 46).
		branches = []branch{
			{`core.notes n`, `n.parent_id is null`},
			{`core.notes n join core.notes h on h.id = n.parent_id and h.space_id = $1::uuid and (h.deleted_at is not null or h.freshness = 'archived')`, `true`},
		}
	}
	op, dir := ">", "asc"
	if back {
		op, dir = "<", "desc"
	}
	curCond := ""
	if cur != nil {
		if inclusive {
			op += "="
		}
		args = append(args, cur.Pos, cur.At, cur.ID)
		n := len(args)
		curCond = ` and (n.position, n.created_at, n.id) ` + op + ` ($` + strconv.Itoa(n-2) + `::int8, $` + strconv.Itoa(n-1) + `::timestamptz, $` + strconv.Itoa(n) + `::uuid)`
	}
	args = append(args, limit+1)
	lim := `limit $` + strconv.Itoa(len(args)) + `::int`
	order := `order by n.position ` + dir + `, n.created_at ` + dir + `, n.id ` + dir
	var parts []string
	for _, br := range branches {
		parts = append(parts, `(select n.id, n.space_id, n.parent_id, n.title, n.icon, n.position, n.page_status, n.restriction, n.created_at, n.freshness, n.verify_by, n.updated_at
		      from `+br.from+`
		      where n.space_id = $1::uuid and n.deleted_at is null and n.freshness <> 'archived' and n.types = '{}' and `+br.cond+curCond+`
		        and core.note_access(n.id, $2::uuid, $3, $4::text[]::uuid[]) <> 'none'
		      `+order+` `+lim+`)`)
	}
	inner := parts[0]
	if len(parts) > 1 {
		inner = `select * from (` + strings.Join(parts, ` union all `) + `) n ` + order + ` ` + lim
	}
	q := `select t.id::text, t.space_id::text, t.parent_id::text, t.title, t.icon, coalesce(t.position, 0), t.page_status, t.restriction, t.created_at,
		t.freshness, t.verify_by::text, t.updated_at,
		exists (select 1 from core.notes k where k.space_id = t.space_id and k.parent_id = t.id and k.deleted_at is null and k.freshness <> 'archived'
		        and k.types = '{}' and core.note_access(k.id, $2::uuid, $3, $4::text[]::uuid[]) <> 'none') as has_children
		from (` + inner + `) t
		order by t.position ` + dir + `, t.created_at ` + dir + `, t.id ` + dir
	rs, err := s.c.Tx.Query(s.c.Context, q, args...)
	if err != nil {
		return nil, false, command.DB(err)
	}
	out, err := pgx.CollectRows(rs, func(r pgx.CollectableRow) (treeRowKey, error) {
		var x treeRowKey
		var at, upd time.Time
		err := r.Scan(&x.ID, &x.SpaceID, &x.ParentID, &x.Title, &x.Icon, &x.Position, &x.Status, &x.Restriction, &at, &x.Freshness, &x.VerifyBy, &upd, &x.HasChildren)
		x.UpdatedAt = command.Ts(upd)
		x.key = treeKey{x.Position, at, x.ID}
		return x, err
	})
	if err != nil {
		return nil, false, command.DB(err)
	}
	more := len(out) > limit
	if more { // the extra row is the farthest one in the direction read
		out = out[:limit]
	}
	if back { // read descending: flip to ascending
		slices.Reverse(out)
	}
	return out, more, nil
}

// hasShared reports whether the reader holds pages of this space that sit outside the space's own tree.
func (s *treeScope) hasShared() (bool, error) {
	rows, _, err := s.level(nil, true, nil, false, false, 1)
	return len(rows) > 0, err
}

// ---------- reveal ----------

type chainNode struct {
	id, space string
	parent    *string
	key       treeKey
	live      bool
	archived  bool
	level     string
}

// reveal returns the sibling level of each readable ancestor of a page, root first, each cut at 200 rows around the path. The
// chain stops at the first ancestor that is trashed, archived or unreadable; when the break is an unreadable ancestor (or the
// space is not readable) the top level is the synthetic Shared with you level. Hidden ancestors contribute nothing.
func (s *treeScope) reveal(id string) (any, error) {
	rs, err := s.c.Tx.Query(s.c.Context,
		`with recursive chain as (
		   select n.id, n.parent_id, n.space_id, n.position, n.created_at, n.deleted_at, n.freshness, 1 as d from core.notes n where n.id = $1::uuid
		   union all
		   select p.id, p.parent_id, p.space_id, p.position, p.created_at, p.deleted_at, p.freshness, c.d + 1
		   from core.notes p join chain c on p.id = c.parent_id where c.d < $5)
		 select id::text, parent_id::text, space_id::text, coalesce(position, 0), created_at, deleted_at is null, freshness = 'archived',
		        core.note_access(id, $2::uuid, $3, $4::text[]::uuid[])
		 from chain order by d`, id, s.uid, s.admin, s.spaces, treeMaxDepth)
	if err != nil {
		return nil, command.DB(err)
	}
	chain, err := pgx.CollectRows(rs, func(r pgx.CollectableRow) (chainNode, error) {
		var n chainNode
		var at time.Time
		var pos int64
		err := r.Scan(&n.id, &n.parent, &n.space, &pos, &at, &n.live, &n.archived, &n.level)
		n.key = treeKey{pos, at, n.id}
		return n, err
	})
	if err != nil {
		return nil, command.DB(err)
	}
	if len(chain) == 0 || !chain[0].live || chain[0].level == "none" {
		return nil, command.ErrNotFound("note")
	}
	s.space = chain[0].space
	s.readable = slices.Contains(s.spaces, s.space)
	// Path from the page up to the highest ancestor that belongs in the tree. An archived page is not a row, so the path starts
	// at its parent.
	var path []chainNode
	sharedTop := !s.readable
	for i, n := range chain {
		if i == 0 && n.archived {
			continue
		}
		if i > 0 && (!n.live || n.archived) {
			sharedTop = !s.readable // a trashed or archived parent: the child is a root level row
			break
		}
		if i > 0 && n.level == "none" {
			sharedTop = true
			break
		}
		path = append(path, n)
	}
	slices.Reverse(path)
	levels := make([]treeLevelOut, 0, len(path)+1)
	for i, n := range path {
		var parent *string
		shared := false
		if i == 0 {
			shared = sharedTop
		} else {
			parent = &path[i-1].id
		}
		lv, err := s.window(parent, shared, n.key)
		if err != nil {
			return nil, err
		}
		levels = append(levels, lv)
	}
	if len(path) == 0 { // an archived top-level page: nothing of its own to show
		levels = []treeLevelOut{}
	}
	return map[string]any{"levels": levels}, nil
}

// window reads up to 100 rows before the pivot and 100 from it on, so the revealed row sits in the middle of a level that is
// longer than a page. Both ends carry a cursor when more exists.
func (s *treeScope) window(parent *string, shared bool, pivot treeKey) (treeLevelOut, error) {
	before, moreBefore, err := s.level(parent, shared, &pivot, true, false, treeWindow)
	if err != nil {
		return treeLevelOut{}, err
	}
	from, moreAfter, err := s.level(parent, shared, &pivot, false, true, treeWindow)
	if err != nil {
		return treeLevelOut{}, err
	}
	rows := append(before, from...)
	out := s.package_(rows, false, false, false, parent, shared)
	if len(rows) > 0 {
		if moreAfter {
			out.Next = rows[len(rows)-1].key.cursor()
		}
		if moreBefore {
			out.Prev = rows[0].key.cursor()
		}
	}
	return out, nil
}
