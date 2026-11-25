package notes

import (
	"encoding/json"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// Phase A2 page lifecycle: move, reorder, delete (docs-parity 7.9 as overridden by 7.10). Every structural writer takes the
// tree lock of its space first (lockTree), then reads the subtree, the ancestry, the permissions and the positions under it.
// Lock order everywhere: the space lock, then note rows ordered by id.

const (
	// RestrictedMessage is the one refusal for "cannot see" and "cannot edit": it never names, counts or hints at a page.
	RestrictedMessage = "Some pages here are restricted"
	// CouldntSaveMessage is the refusal when the live edits of a page could not be folded into it first (the web matches it).
	CouldntSaveMessage = "Couldn't save the latest edits. Try again."
	maxTreeDepth       = 50
	maxSubtreePages    = 500
	positionGap        = 1024
)

func init() {
	a2Handlers["note.move"] = noteMove
	a2Handlers["note.reorder"] = noteReorder
}

func errRestricted() error { return command.ErrForbidden(RestrictedMessage) }

// requireStructural: a person at Member or above (a guest never changes the tree, even on a shared page) and a caller whose
// scope holds write_notes (the read-only AI scope does not). note.update with a parent change runs the same gate.
func requireStructural(c *command.Ctx) error {
	p := c.Principal
	if p.Role < command.Member {
		return command.ErrForbidden("changing the page tree requires role member")
	}
	if p.Scopes != nil && !slices.ContainsFunc(p.Scopes, func(s string) bool { return s == "*" || s == string(command.GroupWriteNotes) }) {
		return command.ErrForbidden("scope write_notes not granted")
	}
	return nil
}

// lockTree serialises every structural writer of one space for the rest of the transaction.
func lockTree(c *command.Ctx, space string) error {
	_, err := c.Tx.Exec(c.Context, "select pg_advisory_xact_lock(hashtextextended('notes.tree:' || $1::text, 0))", space)
	return command.DB(err)
}

type ancestor struct {
	ID, Restriction string
	Owner           *string
	Depth           int
}

// chainUp is the page and its ancestors, the page first, deleted ones included. At most 52 rows, so a cycle ends.
func chainUp(c *command.Ctx, start string) ([]ancestor, error) {
	rows, err := c.Tx.Query(c.Context,
		`with recursive up as (
		   select id, parent_id, restriction, coalesce(owner_id, created_by) o, 1 d from core.notes where id = $1::uuid
		   union all
		   select p.id, p.parent_id, p.restriction, coalesce(p.owner_id, p.created_by), up.d + 1
		     from core.notes p join up on p.id = up.parent_id where up.d < 51)
		 select id::text, restriction, o::text, d from up order by d`, start)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	var out []ancestor
	for rows.Next() {
		var a ancestor
		if err := rows.Scan(&a.ID, &a.Restriction, &a.Owner, &a.Depth); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, a)
	}
	return out, command.DB(rows.Err())
}

func restrictedIDs(chain []ancestor) []string {
	var out []string
	for _, a := range chain {
		if a.Restriction != "none" {
			out = append(out, a.ID)
		}
	}
	return out
}

func errTooDeep() error {
	return command.ErrInvalid("Pages can be nested at most 50 levels deep")
}

// chainDepth is the number of levels from the space root down to (and including) start; 0 for no start.
// A chain longer than the limit, or a cycle, is refused as too deep.
func chainDepth(c *command.Ctx, start *string) (int, []ancestor, error) {
	if start == nil {
		return 0, nil, nil
	}
	chain, err := chainUp(c, *start)
	if err != nil {
		return 0, nil, err
	}
	if len(chain) > maxTreeDepth {
		return 0, nil, errTooDeep()
	}
	return len(chain), chain, nil
}

type subPage struct {
	ID     string
	Parent *string
	Types  []string
}

// subtree is the page and all its live descendants (at most 500, root first) and the number of levels it spans.
func subtree(c *command.Ctx, root string) ([]subPage, int, error) {
	rows, err := c.Tx.Query(c.Context,
		`with recursive s as (
		   select id, parent_id, types from core.notes where id = $1::uuid and deleted_at is null
		   union
		   select c.id, c.parent_id, c.types from core.notes c join s on c.parent_id = s.id where c.deleted_at is null)
		 select id::text, parent_id::text, types from s limit $2`, root, maxSubtreePages+1)
	if err != nil {
		return nil, 0, command.DB(err)
	}
	defer rows.Close()
	byID := map[string]subPage{}
	for rows.Next() {
		var p subPage
		if err := rows.Scan(&p.ID, &p.Parent, &p.Types); err != nil {
			return nil, 0, command.DB(err)
		}
		byID[p.ID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, 0, command.DB(err)
	}
	if len(byID) > maxSubtreePages {
		return nil, 0, command.ErrInvalid("This page has too many sub-pages for one step; move or delete them in parts")
	}
	if _, ok := byID[root]; !ok {
		return nil, 0, command.ErrNotFound("note")
	}
	kids := map[string][]string{}
	for _, p := range byID {
		if p.Parent != nil && p.ID != root {
			kids[*p.Parent] = append(kids[*p.Parent], p.ID)
		}
	}
	for _, k := range kids {
		slices.Sort(k)
	}
	out, seen, level, height := []subPage{byID[root]}, map[string]bool{root: true}, []string{root}, 0
	for len(level) > 0 {
		height++
		var next []string
		for _, id := range level {
			for _, k := range kids[id] {
				if !seen[k] {
					seen[k] = true
					out = append(out, byID[k])
					next = append(next, k)
				}
			}
		}
		level = next
	}
	return out, height, nil
}

func subIDs(pages []subPage) []string {
	ids := make([]string, len(pages))
	for i, p := range pages {
		ids[i] = p.ID
	}
	return ids
}

// lockRows takes the rows FOR UPDATE in id order.
func lockRows(c *command.Ctx, ids []string) error {
	_, err := c.Tx.Exec(c.Context, "select 1 from core.notes where id = any($1::text[]::uuid[]) order by id for update", ids)
	return command.DB(err)
}

// canEdit is the edit rule for every page of ids, all in one space: page-level edit access (as it is now, or as it was for a
// page in the trash when anyState is set) and write access to the space.
func canEdit(c *command.Ctx, space string, ids []string, anyState bool) (bool, error) {
	if err := access.AssertSpaceAccess(c, space, true); err != nil {
		var ce *command.Error
		if errors.As(err, &ce) && (ce.Code == command.Forbidden || ce.Code == command.NotFound) {
			return false, nil
		}
		return false, err
	}
	if len(ids) == 0 {
		return true, nil
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return false, err
	}
	uid, admin := access.Viewer(c)
	fn := "core.note_access"
	if anyState {
		fn = "core.note_access_any"
	}
	var bad int
	err = c.Tx.QueryRow(c.Context,
		`select count(*) from (select distinct x as id from unnest($1::text[]::uuid[]) x) t
		 where `+fn+`(t.id, $2::uuid, $3::bool, $4::text[]::uuid[]) <> 'edit'`, ids, uid, admin, spaces).Scan(&bad)
	return bad == 0, command.DB(err)
}

// canEditParent is canEdit for a destination: the page, or the space root when parent is nil.
func canEditParent(c *command.Ctx, space string, parent *string) (bool, error) {
	if parent == nil {
		return canEdit(c, space, nil, false)
	}
	return canEdit(c, space, []string{*parent}, false)
}

// canManage: may the caller manage the restriction of a page: its owner (created_by when none), a space admin or a workspace admin.
func canManage(c *command.Ctx, space string, owner *string) (bool, error) {
	p := c.Principal
	if p.Role >= command.Admin {
		return true, nil
	}
	if p.UserID == nil {
		return false, nil
	}
	if owner != nil && *owner == *p.UserID {
		return true, nil
	}
	var ok bool
	err := c.Tx.QueryRow(c.Context, "select exists(select 1 from core.space_members where space_id = $1::uuid and user_id = $2::uuid and role = 'admin')", space, *p.UserID).Scan(&ok)
	return ok, command.DB(err)
}

type siblingRow struct {
	ID  string
	Pos int64
}

func siblings(c *command.Ctx, space string, parent *string, except string) ([]siblingRow, error) {
	rows, err := c.Tx.Query(c.Context,
		`select id::text, coalesce(position, 0) from core.notes
		 where space_id = $1::uuid and parent_id is not distinct from $2::uuid and deleted_at is null and id <> $3::uuid
		 order by coalesce(position, 0), created_at, id`, space, parent, except)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	var out []siblingRow
	for rows.Next() {
		var s siblingRow
		if err := rows.Scan(&s.ID, &s.Pos); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, s)
	}
	return out, command.DB(rows.Err())
}

// renumber gives the live siblings (except one page) positions 1024, 2048, ... in their current order. Siblings the caller cannot
// see are renumbered too: only their order is used, nothing is exposed.
func renumber(c *command.Ctx, space string, parent *string, except string) error {
	if _, err := c.Tx.Exec(c.Context,
		`select 1 from core.notes where space_id = $1::uuid and parent_id is not distinct from $2::uuid and deleted_at is null and id <> $3::uuid
		 order by id for update`, space, parent, except); err != nil {
		return command.DB(err)
	}
	_, err := c.Tx.Exec(c.Context,
		`with ord as (select id, row_number() over (order by coalesce(position, 0), created_at, id) rn from core.notes
		   where space_id = $1::uuid and parent_id is not distinct from $2::uuid and deleted_at is null and id <> $3::uuid)
		 update core.notes n set position = ord.rn * $4 from ord where n.id = ord.id`, space, parent, except, int64(positionGap))
	return command.DB(err)
}

// placePosition is the position of page self among the siblings of parent, before the sibling before (last when nil).
// A gap below 2 renumbers all siblings first. Must run under the tree lock.
func placePosition(c *command.Ctx, space string, parent *string, before *string, self string) (int64, error) {
	for attempt := 0; attempt < 2; attempt++ {
		sibs, err := siblings(c, space, parent, self)
		if err != nil {
			return 0, err
		}
		if before == nil {
			if len(sibs) == 0 {
				return positionGap, nil
			}
			return sibs[len(sibs)-1].Pos + positionGap, nil
		}
		idx := slices.IndexFunc(sibs, func(s siblingRow) bool { return s.ID == *before })
		if idx < 0 {
			return 0, command.ErrInvalid("before_id must be a page under the same parent")
		}
		var prev int64
		if idx > 0 {
			prev = sibs[idx-1].Pos
		}
		if gap := sibs[idx].Pos - prev; gap >= 2 {
			return prev + gap/2, nil
		}
		if err := renumber(c, space, parent, self); err != nil {
			return 0, err
		}
	}
	return 0, command.NewError(command.Internal, "could not place the page")
}

type moveInput struct {
	id        string
	parent    *string
	before    *string
	bump      bool // note.move bumps the page version; note.update does it itself
	space     string
	oldParent *string
}

type moveResult struct {
	ID       string  `json:"id"`
	SpaceID  string  `json:"space_id"`
	ParentID *string `json:"parent_id"`
	Position int64   `json:"position"`
}

func samePtrStr(a, b *string) bool { return samePtr(a, b) }

// movePage is the one implementation of "give a page another parent and place": note.move and a parent change in note.update.
// The caller has passed requireStructural.
func movePage(c *command.Ctx, in moveInput) (moveResult, error) {
	space, err := access.AssertNoteAccess(c, in.id, false)
	if err != nil {
		return moveResult{}, err
	}
	if err := lockTree(c, space); err != nil {
		return moveResult{}, err
	}
	var oldParent *string
	if err := c.Tx.QueryRow(c.Context, "select parent_id::text from core.notes where id = $1::uuid and deleted_at is null", in.id).Scan(&oldParent); errors.Is(err, pgx.ErrNoRows) {
		return moveResult{}, command.ErrNotFound("note")
	} else if err != nil {
		return moveResult{}, command.DB(err)
	}
	if in.parent != nil {
		if _, err := access.AssertNoteAccess(c, *in.parent, false); err != nil {
			return moveResult{}, err
		}
		ps, err := parentSpace(c, *in.parent)
		if err != nil {
			return moveResult{}, err
		}
		if ps != space {
			return moveResult{}, command.ErrInvalid("a page's parent must be in the same space")
		}
	}
	newDepth, newChain, err := chainDepth(c, in.parent)
	if err != nil {
		return moveResult{}, err
	}
	for _, a := range newChain {
		if a.ID == in.id {
			return moveResult{}, command.ErrInvalid("a page cannot move under itself or one of its own sub-pages")
		}
	}
	sub, height, err := subtree(c, in.id)
	if err != nil {
		return moveResult{}, err
	}
	if newDepth+height > maxTreeDepth {
		return moveResult{}, errTooDeep()
	}
	sameParent := samePtrStr(oldParent, in.parent)
	if in.before != nil && *in.before == in.id {
		if !sameParent {
			return moveResult{}, command.ErrInvalid("before_id must be a page under the same parent")
		}
		var pos int64
		err := c.Tx.QueryRow(c.Context, "select coalesce(position, 0) from core.notes where id = $1::uuid", in.id).Scan(&pos)
		return moveResult{in.id, space, oldParent, pos}, command.DB(err)
	}
	if err := lockRows(c, subIDs(sub)); err != nil {
		return moveResult{}, err
	}
	if in.parent != nil {
		if _, err := c.Tx.Exec(c.Context, "select 1 from core.notes where id = $1::uuid for share", *in.parent); err != nil {
			return moveResult{}, command.DB(err)
		}
	}
	for _, check := range []func() (bool, error){
		func() (bool, error) { return canEdit(c, space, subIDs(sub), false) },
		func() (bool, error) { return canEditParent(c, space, oldParent) },
		func() (bool, error) { return canEditParent(c, space, in.parent) },
	} {
		if ok, err := check(); err != nil {
			return moveResult{}, err
		} else if !ok {
			return moveResult{}, errRestrictedIn(c, space)
		}
	}
	// A restriction the pages inherit now and will not inherit after the move is released: only its owner or an admin may do that.
	released := false
	if !sameParent && oldParent != nil {
		_, oldChain, err := chainDepth(c, oldParent)
		if err != nil {
			return moveResult{}, err
		}
		keep := restrictedIDs(newChain)
		for _, a := range oldChain {
			if a.Restriction == "none" || slices.Contains(keep, a.ID) {
				continue
			}
			released = true
			if ok, err := canManage(c, space, a.Owner); err != nil {
				return moveResult{}, err
			} else if !ok {
				return moveResult{}, errRestrictedIn(c, space)
			}
		}
	}
	pos, err := placePosition(c, space, in.parent, in.before, in.id)
	if err != nil {
		return moveResult{}, err
	}
	q := "update core.notes set parent_id = $2::uuid, position = $3 where id = $1::uuid"
	if in.bump {
		q = "update core.notes set parent_id = $2::uuid, position = $3, updated_at = now(), version = version + 1 where id = $1::uuid"
	}
	if _, err := c.Tx.Exec(c.Context, q, in.id, in.parent, pos); err != nil {
		return moveResult{}, command.DB(err)
	}
	if !sameParent {
		if oldParent != nil {
			if _, err := c.Tx.Exec(c.Context, "delete from core.relations where from_id = $1::uuid and to_id = $2::uuid and kind = 'child'", *oldParent, in.id); err != nil {
				return moveResult{}, command.DB(err)
			}
		}
		if in.parent != nil {
			if err := addRelation(c, *in.parent, in.id, "child", nil); err != nil {
				return moveResult{}, err
			}
		}
	}
	if _, err := c.Emit("note.moved", &in.id, map[string]any{"from_parent": oldParent, "to_parent": in.parent, "space_id": space, "position": pos, "restrictions_released": released}); err != nil {
		return moveResult{}, err
	}
	return moveResult{in.id, space, in.parent, pos}, nil
}

func noteMove(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.reqUUID("id")
	if err != nil {
		return nil, err
	}
	if _, ok := o["parent_id"]; !ok {
		return nil, command.ErrInvalid("parent_id is required (null for the space root)")
	}
	parent, err := o.optUUID("parent_id")
	if err != nil {
		return nil, err
	}
	before, err := o.optUUID("before_id")
	if err != nil {
		return nil, err
	}
	if err := requireStructural(c); err != nil {
		return nil, err
	}
	return movePage(c, moveInput{id: id, parent: parent, before: before, bump: true})
}

func noteReorder(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.reqUUID("id")
	if err != nil {
		return nil, err
	}
	before, err := o.optUUID("before_id")
	if err != nil {
		return nil, err
	}
	if err := requireStructural(c); err != nil {
		return nil, err
	}
	space, err := access.AssertNoteAccess(c, id, false)
	if err != nil {
		return nil, err
	}
	if err := lockTree(c, space); err != nil {
		return nil, err
	}
	var parent *string
	if err := c.Tx.QueryRow(c.Context, "select parent_id::text from core.notes where id = $1::uuid and deleted_at is null", id).Scan(&parent); errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("note")
	} else if err != nil {
		return nil, command.DB(err)
	}
	if before != nil && *before == id {
		var pos int64
		err := c.Tx.QueryRow(c.Context, "select coalesce(position, 0) from core.notes where id = $1::uuid", id).Scan(&pos)
		return map[string]any{"id": id, "parent_id": parent, "position": pos}, command.DB(err)
	}
	if err := lockRows(c, []string{id}); err != nil {
		return nil, err
	}
	for _, check := range []func() (bool, error){
		func() (bool, error) { return canEdit(c, space, []string{id}, false) },
		func() (bool, error) { return canEditParent(c, space, parent) },
	} {
		if ok, err := check(); err != nil {
			return nil, err
		} else if !ok {
			return nil, errRestrictedIn(c, space)
		}
	}
	pos, err := placePosition(c, space, parent, before, id)
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "update core.notes set position = $2 where id = $1::uuid", id, pos); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("note.reordered", &id, map[string]any{"parent_id": parent, "position": pos}); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "parent_id": parent, "position": pos}, nil
}

// MoveUnder gives a page a parent through the shared structural path (tree lock, cycle and depth check, position),
// for writers outside this package such as the importers. A refusal that is about the shape of the tree comes back as an
// *command.Error with code invalid, so the caller can skip the link and keep going.
func MoveUnder(c *command.Ctx, id, parent string) error {
	if err := requireStructural(c); err != nil {
		return err
	}
	_, err := movePage(c, moveInput{id: id, parent: &parent})
	return err
}
