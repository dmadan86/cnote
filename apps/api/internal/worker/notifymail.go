package worker

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/mailer"
	"github.com/cnote/api/internal/modules/notes"
	"github.com/cnote/api/internal/notify"
)

// Daily digest (docs-parity-b 4.3, 9.2, 13.2.5). One mail per person per workspace per day, skipped when empty. The sweep claims
// candidate notifications (FOR UPDATE SKIP LOCKED), checks each against the person's current access, and in the same
// transaction marks failures suppressed and queues the rest through EnqueueNotifyMail, together with the claim of the day.
// The message itself is rebuilt by the notify.mail job just before SMTP (notifydeliver.go).

func init() {
	RegisterSweep(Sweep{"notify.mail_digest", 15 * time.Minute, NotifyDigestSweep})
	DigestExtras = digestTasks
}

// shortcut: no per-person time zone exists yet (core.users has no such column), so every digest follows IST; read the
// person's zone here when one is added.
var digestZone = time.FixedZone("IST", 330*60)

const digestMaxAge = 48 * time.Hour

// AccessRequestMailedToday is the batching hook for access requests: the first one each day goes out directly, later ones wait
// for the digest. The direct sweep calls it and leaves the person's access_request rows alone when it is true (the digest sweep
// takes exactly those). day is the local date, YYYY-MM-DD.
func AccessRequestMailedToday(ctx context.Context, tx pgx.Tx, user, day string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, "select "+accessRequestMailedSQL("$1::uuid", "$2::date"), user, day).Scan(&ok)
	return ok, err
}

// Uses emailed_at in IST; a request handled as suppressed does not count, because nothing reached the inbox.
func accessRequestMailedSQL(user, day string) string {
	return `exists(select 1 from core.notifications x where x.user_id = ` + user + ` and x.kind = 'access_request'
	and x.email_state in ('queued','mailed') and (x.emailed_at at time zone 'Asia/Kolkata')::date = ` + day + `)`
}

// NotifyDigestSweepAt is the digest sweep with the clock injected, for tests.
func NotifyDigestSweepAt(ctx context.Context, w *Worker, now time.Time) error {
	if !mailer.Configured() {
		return nil
	}
	ids, err := allWorkspaceIDs(ctx, w.Pool)
	if err != nil {
		return err
	}
	for _, ws := range ids {
		if err := digestSweepWorkspace(ctx, w, ws, now); err != nil {
			return err
		}
	}
	return nil
}

func NotifyDigestSweep(ctx context.Context, w *Worker) error {
	return NotifyDigestSweepAt(ctx, w, time.Now())
}

func digestSweepWorkspace(ctx context.Context, w *Worker, ws string, now time.Time) error {
	tx, err := db.BeginTenant(ctx, w.Pool, ws)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := digestSweepTx(ctx, tx, ws, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// digestMaxUsers is how many people one sweep run takes. The limit is on people, never on rows: a person's digest claims every
// row they have, so a person with a long backlog is not cut off and left with the rest stranded behind today's claim.
const digestMaxUsers = 200

// digestSweepTx claims and queues one workspace's digests inside tx; the caller commits. It picks up to digestMaxUsers people, then
// handles each one under a lock on their digest-claim row (core.notify_prefs, FOR UPDATE): everything of theirs that is eligible
// and the claim of the day happen together, so two sweeps at once cannot split one person's rows.
func digestSweepTx(ctx context.Context, tx pgx.Tx, ws string, now time.Time) error {
	local := now.In(digestZone)
	day, hour := local.Format("2006-01-02"), local.Hour()
	// People with something to mail: not off, digest not switched off, the digest hour has come and today's is not claimed. The
	// kinds a person gets: everything in digest mode; in smart mode the digest-only kinds plus the access requests the direct
	// sweep left over. Access is judged later, per person, under their lock.
	rows, err := tx.Query(ctx,
		`select n.user_id::text from core.notifications n
		 join core.memberships mm on mm.user_id = n.user_id and mm.workspace_id = $1::uuid
		 left join core.notify_prefs p on p.user_id = n.user_id and p.workspace_id = $1::uuid
		 where `+digestDue+`
		 group by n.user_id order by min(n.created_at), n.user_id limit $7`,
		ws, MailKinds, now, digestMaxAge.Seconds(), hour, day, digestMaxUsers)
	if err != nil {
		return err
	}
	users, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, u := range users {
		seen[u] = true
	}
	// Someone with only due tasks and nothing unread still gets the digest; the job rechecks the tasks at send time.
	trows, err := tx.Query(ctx,
		`select distinct t.assignee_id::text from core.note_tasks t
		 join core.memberships mm on mm.user_id = t.assignee_id and mm.workspace_id = $1::uuid
		 left join core.notify_prefs p on p.user_id = t.assignee_id and p.workspace_id = $1::uuid
		 where t.workspace_id = $1::uuid and not t.done and t.due <= $2::date
		   and coalesce(p.email_mode, 'smart') <> 'off' and not coalesce(p.digest_off, false)
		   and coalesce(p.digest_hour, 8) <= $3::int and p.last_digest_on is distinct from $2::date limit $4`, ws, day, hour, digestMaxUsers)
	if err != nil {
		return err
	}
	taskUsers, err := pgx.CollectRows(trows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, u := range taskUsers {
		if !seen[u] && len(users) < digestMaxUsers {
			users = append(users, u)
		}
	}
	slices.Sort(users) // one lock order for every sweep, so two of them cannot wait on each other
	for _, u := range users {
		if err := digestOneUser(ctx, tx, ws, u, day, hour, now); err != nil {
			return err
		}
	}
	return nil
}

// digestDue is the per-row eligibility shared by the pick and the per-person claim (n, p and mm are joined by the caller).
var digestDue = `n.emailed_at is null and n.read_at is null and n.kind = any($2::text[])
		   and n.created_at > $3::timestamptz - make_interval(secs => $4)
		   and coalesce(p.email_mode, 'smart') <> 'off' and not coalesce(p.digest_off, false)
		   and coalesce(p.digest_hour, 8) <= $5::int and p.last_digest_on is distinct from $6::date
		   and (coalesce(p.email_mode, 'smart') = 'digest' or n.kind in ('comment', 'page_updated')
		        or (n.kind = 'access_request' and (` + accessRequestMailedSQL("n.user_id", "$6::date") + `)))`

// digestOneUser claims one person's day under the lock on their digest-claim row. A concurrent sweep that reaches the same person
// waits here, then finds the day claimed and leaves them alone.
func digestOneUser(ctx context.Context, tx pgx.Tx, ws, user, day string, hour int, now time.Time) error {
	if _, err := tx.Exec(ctx, `insert into core.notify_prefs (workspace_id, user_id) values ($1::uuid, $2::uuid) on conflict do nothing`, ws, user); err != nil {
		return err
	}
	var claimed bool
	if err := tx.QueryRow(ctx, `select coalesce(last_digest_on = $3::date, false) from core.notify_prefs where workspace_id = $1::uuid and user_id = $2::uuid for update`, ws, user, day).Scan(&claimed); err != nil {
		return err
	}
	if claimed {
		return nil
	}
	rows, err := tx.Query(ctx,
		`select n.id::text,
		   case when n.note_id is not null then `+notify.ReadableExpr("n.user_id", "n.note_id")+`
		        when n.source ~ '^space:[0-9a-fA-F-]{36}$' then exists(select 1 from core.spaces s join core.memberships m on m.user_id = n.user_id
		            left join core.space_members sm on sm.space_id = s.id and sm.user_id = m.user_id
		            where s.id = substring(n.source from 7)::uuid and (sm.user_id is not null or m.role in ('admin','owner') or (s.visibility = 'open' and m.role <> 'guest')))
		        else false end
		 from core.notifications n
		 join core.memberships mm on mm.user_id = n.user_id and mm.workspace_id = $1::uuid
		 left join core.notify_prefs p on p.user_id = n.user_id and p.workspace_id = $1::uuid
		 where n.user_id = $7::uuid and `+digestDue+`
		 order by n.created_at, n.id
		 for update of n skip locked`,
		ws, MailKinds, now, digestMaxAge.Seconds(), hour, day, user)
	if err != nil {
		return err
	}
	var ids, drop []string
	for rows.Next() {
		var id string
		var ok bool
		if err := rows.Scan(&id, &ok); err != nil {
			rows.Close()
			return err
		}
		if ok {
			ids = append(ids, id)
		} else {
			drop = append(drop, id) // access lost, or not a member: handled, never mentioned
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return rows.Err()
	}
	if len(drop) > 0 {
		if _, err := tx.Exec(ctx, `update core.notifications set emailed_at = now(), email_state = 'suppressed' where id = any($1::uuid[])`, drop); err != nil {
			return err
		}
	}
	if len(ids) == 0 {
		due, err := notes.TasksDueBy(ctx, tx, user, day, 1) // with the person's own access, so a lost page adds nothing
		if err != nil {
			return err
		}
		if len(due) == 0 {
			return nil
		}
	}
	if _, err := tx.Exec(ctx, `update core.notify_prefs set last_digest_on = $3::date where workspace_id = $1::uuid and user_id = $2::uuid`, ws, user, day); err != nil {
		return err
	}
	return EnqueueNotifyMail(ctx, tx, ws, user, ModeDigest, ids)
}

// digestTasks adds the person's open tasks that are overdue or due today to a digest ("Tasks due"). P4's query holds the access
// predicate (docs-parity-b 9.2). Runs at send time, inside the job's transaction.
func digestTasks(c *command.Ctx, user string) ([]string, error) {
	today := time.Now().In(digestZone).Format("2006-01-02")
	rows, err := notes.TasksDueBy(c.Context, c.Tx, user, today, maxMailLines)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range rows {
		when := "due today"
		if r.Due != nil && *r.Due < today {
			when = "overdue since " + *r.Due
		}
		out = append(out, fmt.Sprintf("Task due: %s (%s)", r.Text, when))
	}
	return out, nil
}
