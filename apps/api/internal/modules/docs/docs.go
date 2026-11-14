// Package docs ports the living-page and template commands of crates/cnote-docs (commands.rs, templates.rs).
// doc.ask and doc.capture call the AI provider and stay in Rust; the event handlers, jobs and sweeps
// (bindings, freshness) and the seed_templates workspace hook stay in the Rust worker.
package docs

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

const (
	defaultVerifyDays = 90
	lapseGraceDays    = 14
)

func Commands() []command.Command {
	w, r := command.GroupWriteNotes, command.GroupRead
	return []command.Command{
		command.NewCommand("doc.verify", w, docVerify),
		command.NewCommand("doc.set_owner", w, docSetOwner),
		command.NewCommand("doc.flags", r, docFlags),
		command.NewCommand("doc.proposals", r, docProposals),
		command.NewCommand("doc.proposal_decide", w, docProposalDecide),
		command.NewCommand("doc.archived", r, docArchived),
		command.NewCommand("doc.restore", w, docRestore),
		command.NewCommand("doc.freshness_score", r, docFreshnessScore),
		command.NewCommand("capture.list", r, captureList),
		command.NewCommand("capture.answer", w, captureAnswer),
		command.NewCommand("template.save", w, templateSave),
		command.NewCommand("template.list", r, templateList),
		command.NewCommand("template.delete", w, templateDelete),
		command.NewCommand("template.fields", r, templateFields),
		command.NewCommand("template.render", w, templateRender),
		command.NewCommand("template.refresh", w, templateRefresh),
	}
}

func user(c *command.Ctx) (string, error) {
	if c.Principal.UserID == nil {
		return "", command.ErrForbidden("a user is required")
	}
	return *c.Principal.UserID, nil
}

func todayPlus(days int) string {
	return time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour).Format("2006-01-02")
}

func same(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

// pageOwner loads a page the caller can read and returns its owner; manage demands owner-or-admin.
func pageOwner(c *command.Ctx, id string, manage bool) (*string, error) {
	if _, err := access.AssertNoteAccess(c, id, manage); err != nil {
		return nil, err
	}
	var owner *string
	err := c.Tx.QueryRow(c.Context, "select owner_id::text from core.notes where id = $1::uuid and cardinality(types) = 0", id).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("page")
	}
	if err != nil {
		return nil, err
	}
	if manage && c.Principal.Role < command.Admin && !same(owner, c.Principal.UserID) {
		return nil, command.ErrForbidden("only the page owner or an admin can do this")
	}
	return owner, nil
}

func resolveFlags(c *command.Ctx, noteID string) error {
	_, err := c.Tx.Exec(c.Context, "update docs.freshness_flags set resolved_at = now() where note_id = $1::uuid and resolved_at is null", noteID)
	return err
}

// ---------- verify / owner / restore ----------

func docVerify(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.ReqUUID("note_id")
	if err != nil {
		return nil, err
	}
	vby, err := o.OptDate("verify_by")
	if err != nil {
		return nil, err
	}
	if _, err := pageOwner(c, id, true); err != nil {
		return nil, err
	}
	by := todayPlus(defaultVerifyDays)
	if vby != nil {
		by = *vby
	}
	if by <= todayPlus(0) {
		return nil, command.ErrInvalid("verify_by must be in the future")
	}
	if _, err := c.Tx.Exec(c.Context,
		`update core.notes set verified_at = now(), verify_by = $2::date, freshness = 'fresh', lapse_count = 0, archived_at = null
		 where id = $1::uuid`, id, by); err != nil {
		return nil, err
	}
	if err := resolveFlags(c, id); err != nil {
		return nil, err
	}
	if _, err := c.Emit("doc.verified", &id, map[string]any{"verify_by": by}); err != nil {
		return nil, err
	}
	return map[string]any{"note_id": id, "freshness": "fresh", "verify_by": by}, nil
}

func docSetOwner(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.ReqUUID("note_id")
	if err != nil {
		return nil, err
	}
	owner, err := o.ReqUUID("owner_id")
	if err != nil {
		return nil, err
	}
	prev, err := pageOwner(c, id, true)
	if err != nil {
		return nil, err
	}
	var member bool
	if err := c.Tx.QueryRow(c.Context, "select exists (select 1 from core.memberships where user_id = $1::uuid)", owner).Scan(&member); err != nil {
		return nil, err
	}
	if !member {
		return nil, command.ErrInvalid("new owner must be a workspace member")
	}
	if _, err := c.Tx.Exec(c.Context, "update core.notes set owner_id = $2::uuid where id = $1::uuid", id, owner); err != nil {
		return nil, err
	}
	if _, err := c.Emit("doc.owner_changed", &id, map[string]any{"from": prev, "to": owner}); err != nil {
		return nil, err
	}
	return map[string]any{"note_id": id, "owner_id": owner}, nil
}

type archived struct {
	ID         string      `json:"id"`
	SpaceID    string      `json:"space_id"`
	Title      string      `json:"title"`
	OwnerID    *string     `json:"owner_id"`
	ArchivedAt *command.Ts `json:"archived_at"`
}

func docArchived(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	space, err := o.OptUUID("space_id")
	if err != nil {
		return nil, err
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		`select id::text, space_id::text, title, owner_id::text, archived_at from core.notes
		 where freshness = 'archived' and deleted_at is null and space_id = any($1::text[]::uuid[]) and ($2::uuid is null or space_id = $2::uuid)
		   and core.note_access(id, $3::uuid, $4, $1::text[]::uuid[]) <> 'none'
		 order by archived_at desc nulls last limit 200`, spaces, space, uid, admin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []archived{}
	for rows.Next() {
		var a archived
		var at *time.Time
		if err := rows.Scan(&a.ID, &a.SpaceID, &a.Title, &a.OwnerID, &at); err != nil {
			return nil, err
		}
		a.ArchivedAt = command.TsPtr(at)
		out = append(out, a)
	}
	return out, rows.Err()
}

func docRestore(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.ReqUUID("note_id")
	if err != nil {
		return nil, err
	}
	if _, err := pageOwner(c, id, true); err != nil {
		return nil, err
	}
	tag, err := c.Tx.Exec(c.Context,
		`update core.notes set freshness = 'stale', archived_at = null, lapse_count = 0, verify_by = $2::date
		 where id = $1::uuid and freshness = 'archived'`, id, todayPlus(lapseGraceDays))
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, command.ErrConflict("page is not archived")
	}
	// Restored pages come back stale: the owner has to re-verify them.
	if _, err := c.Tx.Exec(c.Context,
		`insert into docs.freshness_flags (workspace_id, note_id, reason, source_note_id, source_event_id)
		 values ($1::uuid,$2::uuid,'date_lapsed',null,null)`, c.WorkspaceID(), id); err != nil {
		return nil, err
	}
	if _, err := c.Emit("doc.restored", &id, map[string]any{}); err != nil {
		return nil, err
	}
	return map[string]any{"note_id": id, "freshness": "stale"}, nil
}

// ---------- owner dashboard ----------

func docFlags(c *command.Ctx, input json.RawMessage) (any, error) {
	var obj map[string]json.RawMessage // `Empty {}` accepts any object and ignores fields
	if json.Unmarshal(input, &obj) != nil || obj == nil {
		return nil, command.ErrFallback
	}
	me, err := user(c)
	if err != nil {
		return nil, err
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	_, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		`select n.id::text, n.title, n.space_id::text, n.freshness, n.verify_by::text, n.lapse_count,
		        array_agg(f.reason order by f.created_at desc) as reasons
		 from core.notes n join docs.freshness_flags f on f.note_id = n.id and f.resolved_at is null
		 where n.owner_id = $1::uuid and n.space_id = any($2::text[]::uuid[]) and n.deleted_at is null and n.freshness <> 'archived'
		   and core.note_access(n.id, $1::uuid, $3, $2::text[]::uuid[]) <> 'none'
		 group by n.id order by min(f.created_at)`, me, spaces, admin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type flagged struct {
		ID         string   `json:"id"`
		Title      string   `json:"title"`
		SpaceID    string   `json:"space_id"`
		Freshness  string   `json:"freshness"`
		VerifyBy   *string  `json:"verify_by"`
		LapseCount int32    `json:"lapse_count"`
		Reasons    []string `json:"reasons"`
	}
	out := []flagged{}
	for rows.Next() {
		var f flagged
		if err := rows.Scan(&f.ID, &f.Title, &f.SpaceID, &f.Freshness, &f.VerifyBy, &f.LapseCount, &f.Reasons); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func docProposals(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	noteID, err := o.OptUUID("note_id")
	if err != nil {
		return nil, err
	}
	status, err := o.OptStr("status")
	if err != nil {
		return nil, err
	}
	st := "pending"
	if status != nil {
		st = *status
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		`select p.id::text, p.note_id::text, n.title, p.proposed_md, p.rationale, p.status, p.created_at
		 from docs.edit_proposals p join core.notes n on n.id = p.note_id
		 where n.space_id = any($1::text[]::uuid[]) and n.deleted_at is null and p.status = $2 and ($3::uuid is null or p.note_id = $3::uuid)
		   and (n.owner_id = $4::uuid or $5)  -- proposals may quote sources only the owner can read
		   and core.note_access(n.id, $4::uuid, $5, $1::text[]::uuid[]) <> 'none'
		 order by p.created_at desc limit 100`, spaces, st, noteID, uid, admin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type proposal struct {
		ID         string     `json:"id"`
		NoteID     string     `json:"note_id"`
		Title      string     `json:"title"`
		ProposedMD string     `json:"proposed_md"`
		Rationale  string     `json:"rationale"`
		Status     string     `json:"status"`
		CreatedAt  command.Ts `json:"created_at"`
	}
	out := []proposal{}
	for rows.Next() {
		var p proposal
		var at time.Time
		if err := rows.Scan(&p.ID, &p.NoteID, &p.Title, &p.ProposedMD, &p.Rationale, &p.Status, &at); err != nil {
			return nil, err
		}
		p.CreatedAt = command.Ts(at)
		out = append(out, p)
	}
	return out, rows.Err()
}

func docProposalDecide(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	pid, err := o.ReqUUID("proposal_id")
	if err != nil {
		return nil, err
	}
	approve, err := o.ReqBool("approve")
	if err != nil {
		return nil, err
	}
	me, err := user(c)
	if err != nil {
		return nil, err
	}
	var noteID, md string
	var flagID *string
	err = c.Tx.QueryRow(c.Context,
		"select note_id::text, flag_id::text, proposed_md from docs.edit_proposals where id = $1::uuid and status = 'pending' for update", pid).
		Scan(&noteID, &flagID, &md)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("pending proposal")
	}
	if err != nil {
		return nil, err
	}
	if _, err := pageOwner(c, noteID, true); err != nil {
		return nil, err
	}
	if approve {
		body, err := command.Marshal(map[string]any{"id": noteID, "content_md": md})
		if err != nil {
			return nil, err
		}
		if _, err := c.Call("note.update", body); err != nil {
			return nil, err
		}
		if flagID != nil {
			if _, err := c.Tx.Exec(c.Context, "update docs.freshness_flags set resolved_at = now() where id = $1::uuid", *flagID); err != nil {
				return nil, err
			}
		}
		// The owner reviewed the edit; the page is fresh again unless other flags are still open.
		if _, err := c.Tx.Exec(c.Context,
			`update core.notes set freshness = 'fresh' where id = $1::uuid and freshness = 'stale'
			   and not exists (select 1 from docs.freshness_flags where note_id = $1::uuid and resolved_at is null)`, noteID); err != nil {
			return nil, err
		}
	}
	status := "rejected"
	if approve {
		status = "approved"
	}
	if _, err := c.Tx.Exec(c.Context,
		"update docs.edit_proposals set status = $2, decided_by = $3::uuid, decided_at = now() where id = $1::uuid", pid, status, me); err != nil {
		return nil, err
	}
	if _, err := c.Emit("doc.proposal_decided", &noteID, map[string]any{"proposal_id": pid, "status": status, "decided_by": me}); err != nil {
		return nil, err
	}
	return map[string]any{"proposal_id": pid, "status": status}, nil
}

// ---------- freshness score ----------

func docFreshnessScore(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	space, err := o.OptUUID("space_id")
	if err != nil {
		return nil, err
	}
	win, err := o.OptInt32("window_days")
	if err != nil {
		return nil, err
	}
	days := int64(90)
	if win != nil {
		days = *win
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		`select s.id::text, s.key, count(*), count(*) filter (where n.freshness = 'fresh'
		         and coalesce(n.verified_at, n.created_at) >= now() - make_interval(days => $2::int))
		 from core.notes n join core.spaces s on s.id = n.space_id
		 where cardinality(n.types) = 0 and n.deleted_at is null and n.freshness <> 'archived'
		   and n.space_id = any($1::text[]::uuid[]) and ($3::uuid is null or n.space_id = $3::uuid)
		   and core.note_access(n.id, $4::uuid, $5, $1::text[]::uuid[]) <> 'none'
		 group by s.id, s.key order by s.key`, spaces, days, space, uid, admin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ratio := func(v, t int64) notes.F64 {
		if t == 0 {
			return 1
		}
		return notes.F64(float64(v) / float64(t))
	}
	var total, verified int64
	list := []map[string]any{}
	for rows.Next() {
		var id, key string
		var t, v int64
		if err := rows.Scan(&id, &key, &t, &v); err != nil {
			return nil, err
		}
		total, verified = total+t, verified+v
		list = append(list, map[string]any{"space_id": id, "key": key, "total": t, "verified": v, "score": ratio(v, t)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"total": total, "verified": verified, "score": ratio(verified, total), "spaces": list}, nil
}

// ---------- capture requests ----------

func captureList(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	status, err := o.OptStr("status")
	if err != nil {
		return nil, err
	}
	st := "open"
	if status != nil {
		st = *status
	}
	rows, err := c.Tx.Query(c.Context,
		`select id::text, question, asked_by::text, routed_to::text, answer_note_id::text, status, created_at
		 from docs.capture_requests
		 where status = $1 and ($2 or routed_to = $3::uuid or asked_by = $3::uuid)
		 order by created_at desc limit 100`, st, c.Principal.Role >= command.Admin, c.Principal.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type capture struct {
		ID           string     `json:"id"`
		Question     string     `json:"question"`
		AskedBy      *string    `json:"asked_by"`
		RoutedTo     *string    `json:"routed_to"`
		AnswerNoteID *string    `json:"answer_note_id"`
		Status       string     `json:"status"`
		CreatedAt    command.Ts `json:"created_at"`
	}
	out := []capture{}
	for rows.Next() {
		var r capture
		var at time.Time
		if err := rows.Scan(&r.ID, &r.Question, &r.AskedBy, &r.RoutedTo, &r.AnswerNoteID, &r.Status, &at); err != nil {
			return nil, err
		}
		r.CreatedAt = command.Ts(at)
		out = append(out, r)
	}
	return out, rows.Err()
}

func captureAnswer(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	rid, err := o.ReqUUID("request_id")
	if err != nil {
		return nil, err
	}
	noteID, err := o.ReqUUID("note_id")
	if err != nil {
		return nil, err
	}
	var routed, asked *string
	err = c.Tx.QueryRow(c.Context,
		"select routed_to::text, asked_by::text from docs.capture_requests where id = $1::uuid and status = 'open' for update", rid).Scan(&routed, &asked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("open capture request")
	}
	if err != nil {
		return nil, err
	}
	me := c.Principal.UserID
	if c.Principal.Role < command.Admin && !same(me, routed) && !same(me, asked) {
		return nil, command.ErrForbidden("not your capture request")
	}
	if _, err := access.AssertNoteAccess(c, noteID, false); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context,
		"update docs.capture_requests set answer_note_id = $2::uuid, status = 'answered' where id = $1::uuid", rid, noteID); err != nil {
		return nil, err
	}
	if _, err := c.Emit("doc.capture_answered", &noteID, map[string]any{"request_id": rid}); err != nil {
		return nil, err
	}
	return map[string]any{"request_id": rid, "status": "answered", "note_id": noteID}, nil
}
