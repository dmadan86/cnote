package notes

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

func init() {
	a2Handlers["note.trash"] = noteTrash
	a2Handlers["note.undelete"] = noteUndelete
}

// Why a restore is refused (7.9 table, extended by 7.10.5). Shown to people as written.
const (
	blockedTrashRestricted = "The parent page is in the trash and this page was inside a restricted area. Restore the parent first, or ask a space admin."
	blockedGoneRestricted  = "The parent page no longer exists. Ask a space admin."
	blockedReleased        = "This page was inside a restricted area that no longer covers its parent. Ask the owner of that page or a space admin."
	blockedNoEvidence      = "This page was deleted before restore rules were recorded. Ask a space admin."
	blockedSaving          = "This page is still being saved. Try again in a moment."
)

type trashPage struct {
	ID, Title, Space string
	Icon             *string
	Parent           *string // parent_id as stored now (null after a purge of the parent)
	DeletedParent    *string
	Evidence         []string // nil: unknown (deleted before A2)
	DeletedAt        time.Time
	DeletedBy        *string
	DeletedByName    *string
	Types            []string
	Position         int64
	CanEdit          bool
}

type restorePlan struct {
	Placed  string // original | space_root | blocked
	Blocked string
	Root    trashPage
	// parentEdit: restoring in place under a live parent, which the caller must also be able to edit
	ParentEdit *string
}

// deletionRoot is the page whose parent is not part of the same deletion.
func deletionRoot(pages []trashPage) trashPage {
	in := map[string]bool{}
	for _, p := range pages {
		in[p.ID] = true
	}
	var root *trashPage
	for i, p := range pages {
		if p.Parent == nil || !in[*p.Parent] {
			if root == nil || p.ID < root.ID {
				root = &pages[i]
			}
		}
	}
	if root == nil {
		root = &pages[0]
	}
	return *root
}

// planRestore decides exactly where a deletion would go and whether the caller may do it (7.10.5). It is the one place
// note.trash and note.undelete both ask.
func planRestore(c *command.Ctx, pages []trashPage) (restorePlan, error) {
	root := deletionRoot(pages)
	plan := restorePlan{Root: root, Placed: "original"}
	admin := c.Principal.Role >= command.Admin
	if !admin {
		var err error
		if admin, err = canManage(c, root.Space, nil); err != nil {
			return plan, err
		}
	}
	var parent *struct {
		live bool
		id   string
	}
	if root.Parent != nil {
		var live bool
		err := c.Tx.QueryRow(c.Context, "select deleted_at is null from core.notes where id = $1::uuid", *root.Parent).Scan(&live)
		if err == nil {
			parent = &struct {
				live bool
				id   string
			}{live, *root.Parent}
		} else if err != pgx.ErrNoRows {
			return plan, command.DB(err)
		}
	}
	live := parent != nil && parent.live
	if root.Evidence == nil { // unknown: only an admin may judge
		if !admin {
			plan.Placed, plan.Blocked = "blocked", blockedNoEvidence
			return plan, nil
		}
		if !live && root.DeletedParent != nil || root.Parent != nil && !live {
			plan.Placed = "space_root"
		}
		if live {
			plan.ParentEdit = root.Parent
		}
		return plan, nil
	}
	switch {
	case root.DeletedParent == nil:
		// it was at the space root
	case live:
		_, chain, err := chainDepth(c, root.Parent)
		if err != nil {
			plan.Placed, plan.Blocked = "blocked", "Pages can be nested at most 50 levels deep"
			return plan, nil
		}
		cur := restrictedIDs(chain)
		for _, e := range root.Evidence {
			if slices.Contains(cur, e) {
				continue
			}
			// a restriction that applied then and does not now: restoring here releases it
			var owner *string
			var sp string
			err := c.Tx.QueryRow(c.Context, "select space_id::text, coalesce(owner_id, created_by)::text from core.notes where id = $1::uuid", e).Scan(&sp, &owner)
			ok := false
			if err == nil {
				if ok, err = canManage(c, sp, owner); err != nil {
					return plan, err
				}
			} else if err != pgx.ErrNoRows {
				return plan, command.DB(err)
			} else {
				ok = admin
			}
			if !ok {
				plan.Placed, plan.Blocked = "blocked", blockedReleased
				return plan, nil
			}
		}
		plan.ParentEdit = root.Parent
	default: // the parent is in the trash or gone
		purged := parent == nil // purged parents are gone; a recorded ancestor may have been purged too
		for _, e := range root.Evidence {
			if purged {
				break
			}
			var n int
			if err := c.Tx.QueryRow(c.Context, "select count(*) from core.notes where id = $1::uuid", e).Scan(&n); err != nil {
				return plan, command.DB(err)
			}
			purged = n == 0
		}
		if (purged || len(root.Evidence) > 0) && !admin { // 7.10.5: a purged parent or ancestor needs an admin, restriction or not
			plan.Placed, plan.Blocked = "blocked", blockedTrashRestricted
			if purged {
				plan.Blocked = blockedGoneRestricted
			}
			return plan, nil
		}
		plan.Placed = "space_root"
	}
	return plan, nil
}

type trashEntry struct {
	DeletionID string       `json:"deletion_id"`
	SpaceID    string       `json:"space_id"`
	Root       trashRoot    `json:"root"`
	PageCount  int          `json:"page_count"`
	DeletedAt  command.Ts   `json:"deleted_at"`
	DeletedBy  *trashPerson `json:"deleted_by"`
	RestoreTo  string       `json:"restore_to"`
	Blocked    *string      `json:"blocked_reason"`
}
type trashRoot struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Icon  *string `json:"icon"`
}
type trashPerson struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

// loadDeletion reads every page of the given deletions with what the caller could do to it as it was.
func loadDeletions(c *command.Ctx, dids []string) (map[string][]trashPage, error) {
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		`select n.deletion_id::text, n.id::text, n.title, n.space_id::text, n.icon, n.parent_id::text, n.deleted_parent_id::text,
		        n.deleted_restricted_by::text[], n.deleted_at, u.id::text, u.name, n.types, coalesce(n.position, 0),
		        core.note_access_any(n.id, $2::uuid, $3::bool, $4::text[]::uuid[]) = 'edit'
		 from core.notes n
		 left join core.memberships m on m.user_id = n.deleted_by and m.workspace_id = n.workspace_id
		 left join core.users u on u.id = m.user_id
		 where n.deletion_id = any($1::text[]::uuid[]) order by n.id`, dids, uid, admin, spaces)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := map[string][]trashPage{}
	for rows.Next() {
		var did string
		var p trashPage
		if err := rows.Scan(&did, &p.ID, &p.Title, &p.Space, &p.Icon, &p.Parent, &p.DeletedParent, &p.Evidence, &p.DeletedAt, &p.DeletedBy, &p.DeletedByName, &p.Types, &p.Position, &p.CanEdit); err != nil {
			return nil, command.DB(err)
		}
		out[did] = append(out[did], p)
	}
	return out, command.DB(rows.Err())
}

// listable: plain pages only, and the caller could edit every one as it was and write to the space.
func listable(c *command.Ctx, pages []trashPage, spaceOK map[string]bool) (bool, error) {
	space := pages[0].Space
	for _, p := range pages {
		if !p.CanEdit || len(p.Types) > 0 || p.Space != space {
			return false, nil
		}
	}
	if v, ok := spaceOK[space]; ok {
		return v, nil
	}
	err := access.AssertSpaceAccess(c, space, true)
	spaceOK[space] = err == nil
	return err == nil, nil
}

func noteTrash(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	space, err := o.optUUID("space_id")
	if err != nil {
		return nil, err
	}
	lim, err := o.optInt("limit", 32)
	if err != nil {
		return nil, err
	}
	limit := 50
	if lim != nil {
		limit = int(min(max(*lim, 1), 200))
	}
	// Cursor: the last row's deleted_at (before) and deletion_id (before_id). With only before, everything strictly older.
	var beforeTS *time.Time
	if s, err := o.optStr("before"); err != nil {
		return nil, err
	} else if s != nil {
		t, err := time.Parse(time.RFC3339Nano, *s)
		if err != nil {
			return nil, command.ErrInvalid("before must be an RFC 3339 timestamp")
		}
		beforeTS = &t
	}
	beforeID, err := o.optUUID("before_id")
	if err != nil {
		return nil, err
	}
	if beforeTS != nil && beforeID == nil {
		minID := "00000000-0000-0000-0000-000000000000" // (ts, 0) sorts before every row of ts: those stay listed
		beforeID = &minID
	}
	out := []trashEntry{}
	spaceOK := map[string]bool{}
	const batch = 50
	for len(out) < limit {
		rows, err := c.Tx.Query(c.Context,
			`select deletion_id::text, min(deleted_at) from core.notes
			 where deletion_id is not null and ($1::uuid is null or space_id = $1::uuid)
			 group by deletion_id
			 having ($2::timestamptz is null or (min(deleted_at), deletion_id) < ($2::timestamptz, $3::uuid))
			 order by min(deleted_at) desc, deletion_id desc limit $4`, space, beforeTS, beforeID, batch)
		if err != nil {
			return nil, command.DB(err)
		}
		type key struct {
			id string
			at time.Time
		}
		var keys []key
		for rows.Next() {
			var k key
			if err := rows.Scan(&k.id, &k.at); err != nil {
				rows.Close()
				return nil, command.DB(err)
			}
			keys = append(keys, k)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, command.DB(err)
		}
		if len(keys) == 0 {
			break
		}
		dids := make([]string, len(keys))
		for i, k := range keys {
			dids[i] = k.id
		}
		pages, err := loadDeletions(c, dids)
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			t, did := k.at, k.id
			beforeTS, beforeID = &t, &did // the cursor moves past everything scanned, shown or not
			ps := pages[did]
			if len(ps) == 0 {
				continue
			}
			if ok, err := listable(c, ps, spaceOK); err != nil {
				return nil, err
			} else if !ok {
				continue
			}
			plan, err := planRestore(c, ps)
			if err != nil {
				return nil, err
			}
			ids := make([]string, len(ps))
			for i, p := range ps {
				ids[i] = p.ID
			}
			if plan.Placed != "blocked" {
				if pend, err := pendingPages(c, ids); err != nil {
					return nil, err
				} else if len(pend) > 0 {
					plan.Placed, plan.Blocked = "blocked", blockedSaving
				} else if plan.ParentEdit != nil {
					if ok, err := canEditParent(c, plan.Root.Space, plan.ParentEdit); err != nil {
						return nil, err
					} else if !ok {
						plan.Placed, plan.Blocked = "blocked", RestrictedMessage
					}
				}
			}
			e := trashEntry{DeletionID: did, SpaceID: plan.Root.Space, PageCount: len(ps), DeletedAt: command.Ts(plan.Root.DeletedAt),
				Root: trashRoot{plan.Root.ID, plan.Root.Title, plan.Root.Icon}, RestoreTo: plan.Placed}
			if plan.Blocked != "" {
				b := plan.Blocked
				e.Blocked = &b
			}
			if plan.Root.DeletedBy != nil && plan.Root.DeletedByName != nil {
				e.DeletedBy = &trashPerson{*plan.Root.DeletedBy, *plan.Root.DeletedByName}
			}
			out = append(out, e)
			if len(out) == limit {
				return out, nil
			}
		}
		if len(keys) < batch {
			break
		}
	}
	return out, nil
}

func noteUndelete(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	did, err := o.reqUUID("deletion_id")
	if err != nil {
		return nil, err
	}
	if err := requireStructural(c); err != nil {
		return nil, err
	}
	var space string
	if err := c.Tx.QueryRow(c.Context, "select space_id::text from core.notes where deletion_id = $1::uuid limit 1", did).Scan(&space); err == pgx.ErrNoRows {
		return nil, command.ErrNotFound("note")
	} else if err != nil {
		return nil, command.DB(err)
	}
	if err := lockTree(c, space); err != nil {
		return nil, err
	}
	pages, err := loadDeletions(c, []string{did})
	if err != nil {
		return nil, err
	}
	ps := pages[did]
	if len(ps) == 0 {
		return nil, command.ErrNotFound("note")
	}
	ids := make([]string, len(ps))
	for i, p := range ps {
		ids[i] = p.ID
	}
	if err := lockRows(c, ids); err != nil {
		return nil, err
	}
	if ok, err := listable(c, ps, map[string]bool{}); err != nil {
		return nil, err
	} else if !ok {
		return nil, command.ErrNotFound("note") // a deletion the caller could not list does not exist for them
	}
	plan, err := planRestore(c, ps)
	if err != nil {
		return nil, err
	}
	if plan.Placed == "blocked" {
		return nil, command.ErrConflict(plan.Blocked)
	}
	if plan.ParentEdit != nil {
		if ok, err := canEditParent(c, space, plan.ParentEdit); err != nil {
			return nil, err
		} else if !ok {
			return nil, errRestricted()
		}
	}
	if pend, err := pendingPages(c, ids); err != nil {
		return nil, err
	} else if len(pend) > 0 {
		return nil, command.ErrConflict(blockedSaving)
	}
	root := plan.Root
	var newParent *string
	if plan.Placed == "original" {
		newParent = root.Parent
	}
	base, _, err := chainDepth(c, newParent)
	if err != nil {
		return nil, err
	}
	height := deletionHeight(ps, root.ID)
	if base+height > maxTreeDepth {
		return nil, errTooDeep()
	}
	// position: keep the old one unless the page moved or the place is taken
	pos := root.Position
	moved := plan.Placed == "space_root" && root.Parent != nil
	var taken bool
	if err := c.Tx.QueryRow(c.Context,
		`select exists(select 1 from core.notes where space_id = $1::uuid and parent_id is not distinct from $2::uuid and deleted_at is null and coalesce(position, 0) = $3)`,
		space, newParent, pos).Scan(&taken); err != nil {
		return nil, command.DB(err)
	}
	if moved || taken || pos == 0 {
		if err := c.Tx.QueryRow(c.Context,
			`select coalesce(max(position), 0) + $3 from core.notes where space_id = $1::uuid and parent_id is not distinct from $2::uuid and deleted_at is null`,
			space, newParent, int64(positionGap)).Scan(&pos); err != nil {
			return nil, command.DB(err)
		}
	}
	if _, err := c.Tx.Exec(c.Context,
		`update core.notes set deleted_at = null, deletion_id = null, deleted_by = null, deleted_parent_id = null, deleted_restricted_by = null,
		   collab_epoch = collab_epoch + 1, version = version + 1, updated_at = now() where id = any($1::text[]::uuid[])`, ids); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Tx.Exec(c.Context, "update core.notes set parent_id = $2::uuid, position = $3 where id = $1::uuid", root.ID, newParent, pos); err != nil {
		return nil, command.DB(err)
	}
	if moved {
		if _, err := c.Tx.Exec(c.Context, "delete from core.relations where to_id = $1::uuid and kind = 'child' and from_id = $2::uuid", root.ID, *root.Parent); err != nil {
			return nil, command.DB(err)
		}
	}
	for _, id := range ids {
		id := id
		if _, err := c.Emit("note.undeleted", &id, map[string]any{"deletion_id": did, "root_id": root.ID, "placed": plan.Placed}); err != nil {
			return nil, err
		}
	}
	return map[string]any{"deletion_id": did, "root_id": root.ID, "space_id": space, "count": len(ids), "placed": plan.Placed}, nil
}

// deletionHeight is the number of levels the deleted pages span below and including root.
func deletionHeight(ps []trashPage, root string) int {
	kids := map[string][]string{}
	for _, p := range ps {
		if p.Parent != nil && p.ID != root {
			kids[*p.Parent] = append(kids[*p.Parent], p.ID)
		}
	}
	h, level := 0, []string{root}
	seen := map[string]bool{root: true}
	for len(level) > 0 {
		h++
		var next []string
		for _, id := range level {
			for _, k := range kids[id] {
				if !seen[k] {
					seen[k] = true
					next = append(next, k)
				}
			}
		}
		level = next
	}
	return h
}
