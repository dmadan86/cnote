package notes

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/notify"
)

// note.request_access (docs-parity-b 5.1, 13.2.7). The answer is {ok: true} whatever happens: a page that does not exist, one in the
// trash, one the caller can read, one nobody can grant, or a limit that was hit. Everything below runs in a savepoint, so a failure
// inside leaves no trace and cannot change the answer either. The first mail of the day goes out directly and later ones wait for the
// digest (worker.AccessRequestMailedToday); the limits count durably in core.rate_buckets even when the mail is suppressed.
func init() { b2Handlers["note.request_access"] = noteRequestAccess }

const (
	maxRecipients     = 5
	limitPerRequester = 10  // per hour
	limitPerRecipient = 20  // per day
	limitPerWorkspace = 200 // per day
	maxMessage        = 200
)

func noteRequestAccess(c *command.Ctx, input json.RawMessage) (any, error) {
	id, err := noteIDInput(input, nil)
	if err != nil {
		return nil, err
	}
	uid, err := requireUser(c)
	if err != nil {
		return nil, err
	}
	var in struct {
		Message *string `json:"message"`
	}
	_ = json.Unmarshal(input, &in)
	msg := ""
	if in.Message != nil {
		if utf8.RuneCountInString(*in.Message) > maxMessage {
			return nil, command.ErrInvalid("message must be at most 200 characters")
		}
		msg = strings.Join(strings.Fields(*in.Message), " ")
	}
	if sp, err := c.Tx.Begin(c.Context); err == nil {
		if err := requestAccess(c.Context, c, sp, id, uid, msg); err != nil {
			_ = sp.Rollback(c.Context)
		} else {
			_ = sp.Commit(c.Context)
		}
	}
	return map[string]any{"ok": true}, nil
}

func requestAccess(ctx context.Context, c *command.Ctx, tx pgx.Tx, id, uid, msg string) error {
	if n, err := db.Bump(ctx, tx, "ar:req:"+uid, false); err != nil || n > limitPerRequester {
		return err
	}
	var space, title string
	var spaceArchived bool
	if err := tx.QueryRow(ctx,
		`select n.space_id::text, n.title, s.archived_at is not null from core.notes n join core.spaces s on s.id = n.space_id
		 where n.id = $1::uuid and n.deleted_at is null`, id).Scan(&space, &title, &spaceArchived); err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return err
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return err
	}
	if level, err := access.NoteLevel(c, id, spaces); err != nil || level != "none" || spaceArchived { // an archived space cannot grant anything
		return err
	}
	grant, kind, err := blockingGrant(ctx, c, tx, id, space, uid)
	if err != nil || kind == "" {
		return err
	}
	rcp, err := recipients(ctx, tx, id, grant, kind, space, uid)
	if err != nil || len(rcp) == 0 {
		return err
	}
	today := time.Now().UTC().Format(time.DateOnly)
	if msg != "" {
		title += " - " + msg
	}
	source := "access:" + id + ":" + uid
	for _, r := range rcp {
		if n, err := db.Bump(ctx, tx, "ar:ws:"+c.WorkspaceID(), true); err != nil || n > limitPerWorkspace {
			return err
		}
		if n, err := db.Bump(ctx, tx, "ar:rpr:"+uid+":"+id+":"+r, true); err != nil {
			return err
		} else if n > 1 {
			continue
		}
		if n, err := db.Bump(ctx, tx, "ar:rcp:"+r, true); err != nil {
			return err
		} else if n > limitPerRecipient {
			continue
		}
		if _, err := notify.Send(ctx, tx, notify.Notification{WorkspaceID: c.WorkspaceID(), UserID: r, Kind: notify.KindAccessRequest,
			Title: title, NoteID: &id, ActorUserID: &uid, Source: &source, SourceDay: &today}); err != nil {
			return err
		}
	}
	return nil
}

// blockingGrant names the grant that keeps the requester out, and what kind it is. "page": a view restriction on the page or an
// ancestor the requester has no share on (the share goes on that page; it also opens a private space), or, for a guest, the page
// itself. "space": a private space the requester is not in; its admins decide. "": nothing a person can grant.
func blockingGrant(ctx context.Context, c *command.Ctx, tx pgx.Tx, id, space, uid string) (string, string, error) {
	chain, err := chainUp(c, id)
	if err != nil {
		return "", "", err
	}
	if len(chain) > maxTreeDepth {
		return "", "", nil
	}
	rows, err := tx.Query(ctx, `select note_id::text from core.note_permissions where user_id = $1::uuid and note_id = any($2::text[]::uuid[])`, uid, ancestorIDs(chain))
	if err != nil {
		return "", "", err
	}
	shared, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", "", err
	}
	has := map[string]bool{}
	for _, s := range shared {
		has[s] = true
	}
	for _, a := range chain {
		if a.Restriction == "view" && !has[a.ID] {
			return a.ID, "page", nil
		}
	}
	var member bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from core.space_members where space_id = $1::uuid and user_id = $2::uuid)`, space, uid).Scan(&member); err != nil {
		return "", "", err
	}
	if member {
		return "", "", nil
	}
	if c.Principal.Role == command.Guest {
		return id, "page", nil
	}
	var private bool
	if err := tx.QueryRow(ctx, `select visibility = 'private' from core.spaces where id = $1::uuid`, space).Scan(&private); err != nil || !private {
		return "", "", err
	}
	return space, "space", nil
}

func ancestorIDs(chain []ancestor) []string {
	ids := make([]string, len(chain))
	for i, a := range chain {
		ids[i] = a.ID
	}
	return ids
}

// readable: the candidate's readable spaces, as notify.ReadableExpr computes them.
const readable = `array(select s.id from core.spaces s left join core.space_members sm on sm.space_id = s.id and sm.user_id = m.user_id
	where m.role in ('admin','owner') or sm.user_id is not null or (s.visibility = 'open' and m.role <> 'guest'))`

// recipients are at most five people who can change the blocking grant and can read the requested page: for a page grant, people
// with edit access on the grant page (and a space role that is not viewer), the owner first; for a space, its admins, then workspace
// admins. Guests are never asked and nobody is asked about a page they cannot read.
func recipients(ctx context.Context, tx pgx.Tx, id, grant, kind, space, uid string) ([]string, error) {
	var q string
	if kind == "space" {
		q = `select m.user_id::text from core.memberships m
		     where m.user_id <> $2::uuid and m.role <> 'guest'
		       and (m.role in ('admin','owner') or exists(select 1 from core.space_members sm where sm.space_id = $1::uuid and sm.user_id = m.user_id and sm.role = 'admin'))
		       and core.note_access($3::uuid, m.user_id, m.role in ('admin','owner'), ` + readable + `) <> 'none'
		     order by exists(select 1 from core.space_members sm where sm.space_id = $1::uuid and sm.user_id = m.user_id and sm.role = 'admin') desc, m.user_id limit 5`
		return recipientRows(ctx, tx, q, grant, uid, id)
	}
	q = `select m.user_id::text from core.memberships m
	     where m.user_id <> $2::uuid and m.role <> 'guest'
	       and core.note_access($1::uuid, m.user_id, m.role in ('admin','owner'), ` + readable + `) = 'edit'
	       and core.note_access($3::uuid, m.user_id, m.role in ('admin','owner'), ` + readable + `) <> 'none'
	       and (m.role in ('admin','owner') or coalesce((select sm.role from core.space_members sm where sm.space_id = $4::uuid and sm.user_id = m.user_id), 'editor') <> 'viewer')
	     order by m.user_id = (select coalesce(owner_id, created_by) from core.notes where id = $1::uuid) desc,
	              exists(select 1 from core.note_permissions p where p.note_id = $1::uuid and p.user_id = m.user_id and p.level = 'edit') desc, m.user_id limit 5`
	return recipientRows(ctx, tx, q, grant, uid, id, space)
}

func recipientRows(ctx context.Context, tx pgx.Tx, q string, args ...any) ([]string, error) {
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
