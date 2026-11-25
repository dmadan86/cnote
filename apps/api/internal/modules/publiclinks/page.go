package publiclinks

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// Public page links (docs-parity-b 4.2, 9.1 and 13.2). One active link per page; the token is shown once (page_link.create)
// and only its SHA-256 is stored. Whether a link serves is computed on every request: the resolver
// (core.resolve_page_link) plus PageServes below, which the HTTP handler runs in the same transaction that holds the link row
// FOR SHARE. Revoke, replace and revoke_space take the row FOR UPDATE, so a request authorized after the commit fails.

func init() {
	b1Handlers["page_link.get"] = pageLinkGet
	b1Handlers["page_link.create"] = pageLinkCreate
	b1Handlers["page_link.revoke"] = pageLinkRevoke
	b1Handlers["page_link.list"] = pageLinkList
	b1Handlers["page_link.revoke_space"] = pageLinkRevokeSpace
}

// HashToken is the stored form of a token.
func HashToken(t string) string { return hashToken(t) }

// ReasonRestricted is what create says for a page that a view restriction closes.
const ReasonRestricted = "Remove the view restriction to share this page publicly"

type pageIn struct {
	NoteID        string `json:"note_id"`
	SpaceID       string `json:"space_id"`
	ExpiresInDays *int   `json:"expires_in_days"`
}

func decodePage(raw json.RawMessage, needNote, needSpace bool) (pageIn, error) {
	var v pageIn
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, command.ErrInvalid("invalid input")
	}
	v.NoteID, v.SpaceID = strings.ToLower(v.NoteID), strings.ToLower(v.SpaceID)
	if (needNote && !uuidRe.MatchString(v.NoteID)) || (needSpace && !uuidRe.MatchString(v.SpaceID)) {
		return v, command.ErrInvalid("invalid id")
	}
	return v, nil
}

// Restricted reports whether the page or any ancestor has restriction 'view'. A chain longer than 50 or a cycle counts as
// restricted (the same bound as core.note_access), as does a page that is not there.
func Restricted(c *command.Ctx, noteID string) (bool, error) {
	var r bool
	err := c.Tx.QueryRow(c.Context, `with recursive chain as (
		  select n.id, n.parent_id, n.restriction, 1 as d from core.notes n where n.id = $1::uuid and n.deleted_at is null
		  union all
		  select p.id, p.parent_id, p.restriction, c.d + 1 from core.notes p join chain c on p.id = c.parent_id where c.d < 51)
		select count(*) = 0 or coalesce(bool_or(restriction = 'view' or d > 50), false) from chain`, noteID).Scan(&r)
	return r, command.DB(err)
}

// PageServes is the per-request half of the rules of 4.2: the page is live, plain and not archived in a space that allows
// public links; the link's creator is still a Member or above of the workspace, has edit access to the page (as a Member,
// never as an admin) and no view restriction closes the page. The resolver checks the same page rules, so this can only
// say no more often.
func PageServes(c *command.Ctx, noteID, creator string) (bool, error) {
	var role string
	err := c.Tx.QueryRow(c.Context, `select role from core.memberships where workspace_id = $1::uuid and user_id = $2::uuid`,
		c.WorkspaceID(), creator).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, command.DB(err)
	}
	if command.ParseRole(role) < command.Member {
		return false, nil
	}
	var ok bool
	if err := c.Tx.QueryRow(c.Context, `select exists(select 1 from core.notes n join core.spaces s on s.id = n.space_id
		where n.id = $1::uuid and n.deleted_at is null and n.types = '{}' and n.freshness <> 'archived'
		  and coalesce(s.public_links, true) and s.archived_at is null)`, noteID).Scan(&ok); err != nil || !ok {
		return false, command.DB(err)
	}
	if r, err := Restricted(c, noteID); err != nil || r {
		return false, err
	}
	cc := *c // the creator's own access, capped at Member, without disturbing the caller's context
	cc.Principal = command.Principal{Kind: command.KindSystem, WorkspaceID: c.WorkspaceID(), UserID: &creator, Role: command.Member}
	if _, err := access.AssertNoteAccess(&cc, noteID, true); err != nil {
		var ce *command.Error
		if errors.As(err, &ce) && (ce.Code == command.NotFound || ce.Code == command.Forbidden) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// blockReason says why a link cannot be made for this page now, or "" when it can.
func blockReason(c *command.Ctx, noteID string) (string, error) {
	var typed, archived, spaceArchived, off bool
	if err := c.Tx.QueryRow(c.Context, `select n.types <> '{}', n.freshness = 'archived', s.archived_at is not null, not coalesce(s.public_links, true)
		from core.notes n join core.spaces s on s.id = n.space_id where n.id = $1::uuid and n.deleted_at is null`, noteID).
		Scan(&typed, &archived, &spaceArchived, &off); err != nil {
		return "", command.DB(err)
	}
	switch {
	case typed:
		return "Only plain pages can be shared publicly", nil
	case spaceArchived:
		return access.ArchivedMessage, nil
	case archived:
		return "An archived page cannot be shared publicly", nil
	case off:
		return "Public links are turned off for this space", nil
	}
	if r, err := Restricted(c, noteID); err != nil {
		return "", err
	} else if r {
		return ReasonRestricted, nil
	}
	if c.Principal.UserID != nil {
		if ok, err := PageServes(c, noteID, *c.Principal.UserID); err != nil {
			return "", err
		} else if !ok {
			return "You need edit access to this page to share it publicly", nil
		}
	}
	return "", nil
}

type pageLinkOut struct {
	ID            string      `json:"id"`
	Status        string      `json:"status"`
	CreatedByName *string     `json:"created_by_name"`
	CreatedAt     command.Ts  `json:"created_at"`
	ExpiresAt     *command.Ts `json:"expires_at"`
	ViewCount     int32       `json:"view_count"`
	LastViewedAt  *command.Ts `json:"last_viewed_at"`
}

const pageLinkCols = `l.id::text, l.created_by::text, u.name, l.created_at, l.expires_at, l.view_count, l.last_viewed_at`

func scanPageLink(c *command.Ctx, noteID string, r pgx.Row) (*pageLinkOut, error) {
	var o pageLinkOut
	var creator string
	var created time.Time
	var exp, seen *time.Time
	if err := r.Scan(&o.ID, &creator, &o.CreatedByName, &created, &exp, &o.ViewCount, &seen); err != nil {
		return nil, err
	}
	o.CreatedAt, o.ExpiresAt, o.LastViewedAt = command.Ts(created), command.TsPtr(exp), command.TsPtr(seen)
	o.Status = "off"
	if exp == nil || exp.After(time.Now()) {
		if ok, err := PageServes(c, noteID, creator); err != nil {
			return nil, err
		} else if ok {
			o.Status = "active"
		}
	}
	return &o, nil
}

const activeLink = `from core.public_links l join core.users u on u.id = l.created_by
	where l.note_id = $1::uuid and l.purpose = 'page' and l.revoked_at is null`

func pageLinkGet(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decodePage(input, true, false)
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, v.NoteID, false); err != nil {
		return nil, err
	}
	var link *pageLinkOut
	row := c.Tx.QueryRow(c.Context, "select "+pageLinkCols+" "+activeLink, v.NoteID)
	if link, err = scanPageLink(c, v.NoteID, row); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, command.DB(err)
	}
	reason, err := blockReason(c, v.NoteID)
	if err != nil {
		return nil, err
	}
	var rs *string
	if reason != "" {
		rs = &reason
	}
	return map[string]any{"link": link, "allowed": reason == "", "reason": rs}, nil
}

func pageLinkCreate(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decodePage(input, true, false)
	if err != nil {
		return nil, err
	}
	if c.Principal.UserID == nil {
		return nil, command.ErrForbidden("a public link needs a person to own it")
	}
	if v.ExpiresInDays != nil && (*v.ExpiresInDays < 1 || *v.ExpiresInDays > 365) {
		return nil, command.ErrInvalid("expires_in_days must be between 1 and 365")
	}
	if _, err := access.AssertNoteAccess(c, v.NoteID, true); err != nil {
		return nil, err
	}
	reason, err := blockReason(c, v.NoteID)
	if err != nil {
		return nil, err
	}
	if reason != "" {
		return nil, command.ErrConflict(reason)
	}
	base, err := baseURL(nil)
	if err != nil {
		return nil, err
	}
	if _, err := revokePage(c, v.NoteID); err != nil { // the earlier link stops at once, in this transaction
		return nil, err
	}
	token, hash, err := newToken()
	if err != nil {
		return nil, command.NewError(command.Internal, "could not create a token")
	}
	var exp *time.Time
	var id string
	if err := c.Tx.QueryRow(c.Context, `insert into core.public_links (workspace_id, note_id, token_hash, purpose, expires_at, created_by)
		values ($1::uuid, $2::uuid, $3, 'page', case when $4::int is null then null else now() + make_interval(days => $4::int) end, $5::uuid)
		returning id::text, expires_at`, c.WorkspaceID(), v.NoteID, hash, v.ExpiresInDays, *c.Principal.UserID).Scan(&id, &exp); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("share.created", &v.NoteID, map[string]any{"link_id": id, "purpose": "page", "expires_at": command.TsPtr(exp)}); err != nil {
		return nil, err
	}
	link, err := scanPageLink(c, v.NoteID, c.Tx.QueryRow(c.Context, "select "+pageLinkCols+
		" from core.public_links l join core.users u on u.id = l.created_by where l.id = $1::uuid", id))
	if err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"link": link, "url": base + "/pg/" + token}, nil
}

// revokePage turns off the page's active link. The row is taken FOR UPDATE: it waits for every request that holds it FOR SHARE,
// and a request that comes later fails its own re-check.
func revokePage(c *command.Ctx, noteID string) (int, error) {
	rows, err := c.Tx.Query(c.Context, `select id::text from core.public_links
		where note_id = $1::uuid and purpose = 'page' and revoked_at is null for update`, noteID)
	if err != nil {
		return 0, command.DB(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, command.DB(err)
	}
	for _, id := range ids {
		if _, err := c.Tx.Exec(c.Context, "update core.public_links set revoked_at = now() where id = $1::uuid", id); err != nil {
			return 0, command.DB(err)
		}
		if _, err := c.Emit("share.revoked", &noteID, map[string]any{"link_id": id, "purpose": "page"}); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func pageLinkRevoke(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decodePage(input, true, false)
	if err != nil {
		return nil, err
	}
	// Reading the page comes first and a space admin gets no page access from the space (13.2.1): an admin who cannot see a
	// restricted page learns nothing about its links.
	space, err := access.AssertNoteAccess(c, v.NoteID, false)
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, v.NoteID, true); err != nil {
		var ce *command.Error
		if !errors.As(err, &ce) || ce.Code != command.Forbidden {
			return nil, err
		}
		if aerr := access.AssertSpaceAdmin(c, space); aerr != nil { // admin of the space, or a locked archive: turning off is still safe
			return nil, err
		}
	}
	n, err := revokePage(c, v.NoteID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"revoked": n}, nil
}

func pageLinkList(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decodePage(input, false, true)
	if err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAdmin(c, v.SpaceID); err != nil {
		return nil, err
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	// The page filter runs in the query, before anything is returned: a page the caller cannot read is not listed.
	rows, err := c.Tx.Query(c.Context, `select n.id::text, n.title, u.name, l.created_at, l.expires_at, l.view_count
		from core.public_links l join core.notes n on n.id = l.note_id join core.users u on u.id = l.created_by
		where n.space_id = $1::uuid and l.purpose = 'page' and l.revoked_at is null and n.deleted_at is null
		  and core.note_access(n.id, $2::uuid, $3, $4::text[]::uuid[]) <> 'none'
		order by l.created_at desc limit 200`, v.SpaceID, uid, admin, spaces)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, title string
		var by *string
		var created time.Time
		var exp *time.Time
		var views int32
		if err := rows.Scan(&id, &title, &by, &created, &exp, &views); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, map[string]any{"note_id": id, "title": title, "created_by_name": by, "created_at": command.Ts(created),
			"expires_at": command.TsPtr(exp), "view_count": views})
	}
	return out, command.DB(rows.Err())
}

func pageLinkRevokeSpace(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decodePage(input, false, true)
	if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `select l.id::text, l.note_id::text from core.public_links l join core.notes n on n.id = l.note_id
		where n.space_id = $1::uuid and l.purpose = 'page' and l.revoked_at is null for update of l`, v.SpaceID)
	if err != nil {
		return nil, command.DB(err)
	}
	type pair struct{ id, note string }
	ps, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pair, error) {
		var p pair
		return p, r.Scan(&p.id, &p.note)
	})
	if err != nil {
		return nil, command.DB(err)
	}
	for _, p := range ps {
		if _, err := c.Tx.Exec(c.Context, "update core.public_links set revoked_at = now() where id = $1::uuid", p.id); err != nil {
			return nil, command.DB(err)
		}
		note := p.note
		if _, err := c.Emit("share.revoked", &note, map[string]any{"link_id": p.id, "purpose": "page"}); err != nil {
			return nil, err
		}
	}
	return map[string]any{"revoked": len(ps)}, nil
}
