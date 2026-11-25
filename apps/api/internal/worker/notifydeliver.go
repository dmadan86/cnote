package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/mailer"
	"github.com/cnote/api/internal/notify"
	"github.com/cnote/api/internal/unsub"
)

// Notification mail (docs-parity-b 4.3, 9.2 and 13.2.5).
//
// A sweep decides WHICH notifications to mail; it claims them (FOR UPDATE SKIP LOCKED), marks each one queued and puts one
// job in the outbox holding only references: the person, the kind of mail and the notification ids. This job builds the
// message. It runs just before SMTP, on the data as it is then, so a person who lost the page, left the workspace, turned
// mail off or already read the notification in the meantime gets nothing about it, and a mail with nothing left is not sent.

// NotifyMailJob is the core.jobs kind of one notification mail.
const NotifyMailJob = "notify.mail"

// Modes of a notification mail. Direct mail is sent within minutes for a few kinds; the digest is the daily summary.
const (
	ModeDirect = "direct"
	ModeDigest = "digest"
)

// DirectKinds are mailed within minutes, in smart mode. Everything else the mail covers waits for the digest.
var DirectKinds = []string{"mention", "assigned", "shared", "access_request"}

// MailKinds are the notification kinds that can reach an inbox at all; approvals, reminders, leads and chat keep what they have.
var MailKinds = []string{"mention", "assigned", "shared", "access_request", "comment", "page_updated"}

const maxMailLines = 30

// notifyMailRef is the whole payload of the outbox entry. No title, no name, no text: only references.
type notifyMailRef struct {
	UserID        string   `json:"user_id"`
	Mode          string   `json:"mode"`
	Notifications []string `json:"notifications"`
	MessageID     string   `json:"message_id"`
	Sent          string   `json:"sent_message_id,omitempty"`
}

// DigestExtras lets the package that owns tasks add lines to a digest at send time (overdue and due-today tasks the person can
// still read). Nil means none. The lines are plain text; the job escapes them for HTML.
var DigestExtras func(c *command.Ctx, userID string) ([]string, error)

// EnqueueNotifyMail marks the notifications queued and puts the job in the outbox, in the caller's transaction, so a
// rollback sends nothing and a retry cannot enqueue twice. The caller must hold the rows (FOR UPDATE).
func EnqueueNotifyMail(ctx context.Context, tx pgx.Tx, ws, user, mode string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `update core.notifications set emailed_at = now(), email_state = 'queued' where id = any($1::uuid[]) and user_id = $2::uuid`, ids, user); err != nil {
		return err
	}
	var mid string
	if err := tx.QueryRow(ctx, "select gen_random_uuid()::text").Scan(&mid); err != nil {
		return err
	}
	b, _ := json.Marshal(notifyMailRef{UserID: user, Mode: mode, Notifications: ids, MessageID: "<" + mid + "@cnote>"})
	delay, _ := time.ParseDuration(os.Getenv("MAIL_OUTBOX_DELAY"))
	_, err := tx.Exec(ctx, "insert into core.jobs (workspace_id, kind, payload, run_at) values ($1::uuid,$2,$3::jsonb, now() + make_interval(secs => $4))",
		ws, NotifyMailJob, b, delay.Seconds())
	return err
}

type mailItem struct {
	id, kind, title, note, actor string
	restricted                   bool
}

// notifyMailJob rebuilds the mail from references and sends it, or sends nothing.
func notifyMailJob(c *command.Ctx, payload json.RawMessage) error {
	var ref notifyMailRef
	if json.Unmarshal(payload, &ref) != nil || !isUUID(ref.UserID) || len(ref.Notifications) == 0 {
		return command.ErrInvalid("notify mail payload")
	}
	if ref.Sent != "" {
		return nil // sent by an earlier run whose completion did not commit
	}
	mark := func(state string, ids []string) error {
		_, err := c.Tx.Exec(c.Context, `update core.notifications set email_state = $2 where id = any($1::uuid[]) and user_id = $3::uuid and email_state = 'queued'`, ids, state, ref.UserID)
		return err
	}
	suppressAll := func(why string) error {
		slog.Debug("notification mail dropped", "why", why)
		return mark("suppressed", ref.Notifications)
	}
	if !mailer.Configured() {
		return suppressAll("email is not configured")
	}
	var email, name, wsName string
	var member bool
	err := c.Tx.QueryRow(c.Context,
		`select u.email, u.name, w.name, exists(select 1 from core.memberships m where m.user_id = u.id and m.workspace_id = w.id)
		 from core.users u, core.workspaces w where u.id = $1::uuid and w.id = $2::uuid`, ref.UserID, c.WorkspaceID()).Scan(&email, &name, &wsName, &member)
	if err == pgx.ErrNoRows || (err == nil && !member) {
		return suppressAll("not a member")
	}
	if err != nil {
		return err
	}
	// Preferences as they are now. No row means the defaults.
	mode, directOff, digestOff := "smart", false, false
	err = c.Tx.QueryRow(c.Context, `select email_mode, direct_off, digest_off from core.notify_prefs where user_id = $1::uuid`, ref.UserID).Scan(&mode, &directOff, &digestOff)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	switch {
	case mode == "off", ref.Mode == ModeDirect && (mode != "smart" || directOff), ref.Mode == ModeDigest && digestOff:
		return suppressAll("preferences")
	}

	rows, err := c.Tx.Query(c.Context,
		`select n.id::text, n.kind, n.title, n.note_id::text, coalesce(a.name, ''), n.source, n.read_at is not null
		 from core.notifications n left join core.users a on a.id = n.actor_user_id
		 where n.id = any($1::uuid[]) and n.user_id = $2::uuid and n.email_state = 'queued' order by n.created_at, n.id`, ref.Notifications, ref.UserID)
	if err != nil {
		return err
	}
	type cand struct {
		mailItem
		source *string
		read   bool
	}
	var cands []cand
	for rows.Next() {
		var x cand
		var note *string
		if err := rows.Scan(&x.id, &x.kind, &x.title, &note, &x.actor, &x.source, &x.read); err != nil {
			rows.Close()
			return err
		}
		if note != nil {
			x.note = *note
		}
		cands = append(cands, x)
	}
	rows.Close()
	if rows.Err() != nil {
		return rows.Err()
	}
	var keep []mailItem
	var drop, kept []string
	for _, x := range cands {
		ok := !x.read && slicesContains(MailKinds, x.kind) && (ref.Mode == ModeDigest || slicesContains(DirectKinds, x.kind))
		switch {
		case !ok:
		case x.note != "": // a page: the person must still be able to read it, with their own role and spaces
			if err := c.Tx.QueryRow(c.Context, "select "+notify.ReadableExpr("$1::uuid", "$2::uuid"), ref.UserID, x.note).Scan(&ok); err != nil {
				return err
			}
			if ok {
				err := c.Tx.QueryRow(c.Context,
					`with recursive chain as (select id, parent_id, restriction, 1 d from core.notes where id = $1::uuid
					   union all select p.id, p.parent_id, p.restriction, c.d + 1 from core.notes p join chain c on p.id = c.parent_id where c.d < 50)
					 select coalesce((select bool_or(restriction <> 'none') from chain), false)
					     or exists(select 1 from core.notes n join core.spaces s on s.id = n.space_id where n.id = $1::uuid and s.visibility = 'private')`, x.note).Scan(&x.restricted)
				if err != nil {
					return err
				}
			}
		case x.source != nil && strings.HasPrefix(*x.source, "space:"): // a space: the person must still be in it
			space := strings.TrimPrefix(*x.source, "space:")
			ok = false
			if isUUID(space) {
				if err := c.Tx.QueryRow(c.Context,
					`select exists(select 1 from core.spaces s join core.memberships m on m.user_id = $1::uuid
					   left join core.space_members sm on sm.space_id = s.id and sm.user_id = m.user_id
					   where s.id = $2::uuid and (sm.user_id is not null or m.role in ('admin','owner') or (s.visibility = 'open' and m.role <> 'guest')))`,
					ref.UserID, space).Scan(&ok); err != nil {
					return err
				}
			}
		default:
			ok = false
		}
		if ok {
			keep = append(keep, x.mailItem)
			kept = append(kept, x.id)
		} else {
			drop = append(drop, x.id)
		}
	}
	if len(drop) > 0 {
		if err := mark("suppressed", drop); err != nil {
			return err
		}
	}
	var extra []string
	if ref.Mode == ModeDigest && DigestExtras != nil {
		if extra, err = DigestExtras(c, ref.UserID); err != nil {
			return err
		}
	}
	if len(keep) == 0 && len(extra) == 0 {
		return nil // nothing left: send nothing
	}

	m, err := buildNotifyMail(c.WorkspaceID(), wsName, ref, name, email, keep, extra)
	if err != nil {
		slog.Warn("notification mail not built; dropped", "error", err)
		return mark("suppressed", kept)
	}
	id, err := mailer.Send(c.Context, m)
	if err != nil {
		slog.Warn("notify.mail send failed", "error", err)
		return err // the queue retries with backoff; the rows stay queued and the next run checks everything again
	}
	if jid, ok := jobs.JobID(c.Context); ok {
		if _, err := c.Tx.Exec(c.Context, `update core.jobs set payload = payload || jsonb_build_object('sent_message_id', $2::text) where id = $1`, jid, id); err != nil {
			return err
		}
	}
	return mark("mailed", kept)
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func webBase() string {
	for _, k := range []string{"PUBLIC_WEB_URL", "PUBLIC_BASE_URL", "WEB_URL"} {
		if v := os.Getenv(k); v != "" {
			return strings.TrimRight(v, "/")
		}
	}
	return "http://localhost:3000"
}

var kindSentence = map[string]string{
	"mention":        "mentioned you in",
	"assigned":       "assigned you a task in",
	"shared":         "shared with you",
	"access_request": "asked for access to",
	"comment":        "commented on",
	"page_updated":   "updated",
}

// buildNotifyMail makes the message. It holds the actor's name, a fixed sentence per kind, the page title and a link, never
// page or comment text (9.2). A restricted page's title stays out of the subject. Titles and names are one line in headers and
// escaped in HTML. The unsubscribe token covers the kind of mail this is.
func buildNotifyMail(ws, wsName string, ref notifyMailRef, name, email string, items []mailItem, extra []string) (mailer.Mail, error) {
	kind := unsub.Direct
	if ref.Mode == ModeDigest {
		kind = unsub.Digest
	}
	tok, err := unsub.Token(ws, ref.UserID, kind)
	if err != nil {
		return mailer.Mail{}, err
	}
	base := webBase()
	unsubURL := base + "/api/public/unsubscribe/" + tok
	settings := base + "/w/" + ws + "/settings"
	one := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	who := func(a string) string {
		if a = one(a); a != "" {
			return a
		}
		return "Someone"
	}
	var subject string
	switch {
	case ref.Mode == ModeDigest:
		subject = fmt.Sprintf("%d updates in %s", len(items)+len(extra), one(wsName))
	case len(items) == 1 && items[0].restricted:
		subject = who(items[0].actor) + " " + kindSentence[items[0].kind] + " a restricted page"
	case len(items) == 1:
		subject = who(items[0].actor) + " " + kindSentence[items[0].kind] + " " + one(items[0].title)
	default:
		subject = fmt.Sprintf("%d new notifications in %s", len(items), one(wsName))
	}
	var tb, hb strings.Builder
	tb.WriteString("Hi " + one(name) + ",\n\n")
	hb.WriteString("<p>Hi " + html.EscapeString(one(name)) + ",</p>\n<ul>\n")
	line := func(text, link string) {
		tb.WriteString("- " + text + "\n")
		if link != "" {
			tb.WriteString("  " + link + "\n")
		}
		hb.WriteString("<li>" + html.EscapeString(text))
		if link != "" {
			hb.WriteString(` <a href="` + html.EscapeString(link) + `">Open</a>`)
		}
		hb.WriteString("</li>\n")
	}
	for i, x := range items {
		if i == maxMailLines {
			line(fmt.Sprintf("and %d more", len(items)-maxMailLines), base+"/w/"+ws+"/notifications")
			break
		}
		text := who(x.actor) + " " + kindSentence[x.kind] + " " + one(x.title)
		link := ""
		if x.note != "" {
			link = base + "/w/" + ws + "/n/" + x.note
		}
		line(text, link)
	}
	for i, e := range extra {
		if len(items)+i >= maxMailLines {
			line(fmt.Sprintf("and %d more", len(extra)-i), "")
			break
		}
		line(one(e), "")
	}
	tb.WriteString("\nYou get this because of your notification settings: " + settings + "\nStop these emails: " + unsubURL + "\n")
	hb.WriteString(`</ul>` + "\n" + `<p style="color:#666;font-size:12px">You get this because of your notification settings (<a href="` +
		html.EscapeString(settings) + `">change them</a>). <a href="` + html.EscapeString(unsubURL) + `">Stop these emails</a>.</p>` + "\n")
	return mailer.Mail{
		To: email, Subject: subject, Text: tb.String(), HTML: hb.String(), MessageID: ref.MessageID,
		Headers: map[string]string{
			"List-Unsubscribe":      "<" + unsubURL + ">",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
	}, nil
}

// Direct sweep: every two minutes, one mail per person for unread direct-kind notifications that are at least three minutes
// old (reading one in the app within that time cancels its mail) and at most a day old.
const (
	directMinAge = 3 * time.Minute
	directMaxAge = 24 * time.Hour
)

// NotifyDirectSweepAt is the direct sweep with the clock injected, for tests.
func NotifyDirectSweepAt(ctx context.Context, w *Worker, now time.Time) error {
	if !mailer.Configured() {
		return nil
	}
	ids, err := allWorkspaceIDs(ctx, w.Pool)
	if err != nil {
		return err
	}
	for _, ws := range ids {
		if err := directSweepWorkspace(ctx, w, ws, now); err != nil {
			return err
		}
	}
	return nil
}

func NotifyDirectSweep(ctx context.Context, w *Worker) error {
	return NotifyDirectSweepAt(ctx, w, time.Now())
}

func directSweepWorkspace(ctx context.Context, w *Worker, ws string, now time.Time) error {
	tx, err := db.BeginTenant(ctx, w.Pool, ws)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := directSweepTx(ctx, tx, ws, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// directSweepTx claims and queues one workspace's direct mail inside tx; the caller commits.
func directSweepTx(ctx context.Context, tx pgx.Tx, ws string, now time.Time) error {
	rows, err := tx.Query(ctx,
		`select n.id::text, n.user_id::text,
		        coalesce((select p.email_mode = 'smart' and not p.direct_off from core.notify_prefs p where p.user_id = n.user_id), true)
		 from core.notifications n
		 where n.emailed_at is null and n.read_at is null and n.kind = any($1::text[])
		   and n.created_at <= $2::timestamptz - make_interval(secs => $3) and n.created_at > $2::timestamptz - make_interval(secs => $4)
		 order by n.user_id, n.created_at limit 500
		 for update of n skip locked`, DirectKinds, now, directMinAge.Seconds(), directMaxAge.Seconds())
	if err != nil {
		return err
	}
	byUser := map[string][]string{}
	var order []string
	var off []string
	for rows.Next() {
		var id, user string
		var on bool
		if err := rows.Scan(&id, &user, &on); err != nil {
			rows.Close()
			return err
		}
		if !on {
			off = append(off, id)
			continue
		}
		if _, ok := byUser[user]; !ok {
			order = append(order, user)
		}
		byUser[user] = append(byUser[user], id)
	}
	rows.Close()
	if rows.Err() != nil {
		return rows.Err()
	}
	if len(off) > 0 { // a person who does not want direct mail: handled, never mailed
		if _, err := tx.Exec(ctx, `update core.notifications set emailed_at = now(), email_state = 'suppressed' where id = any($1::uuid[])`, off); err != nil {
			return err
		}
	}
	for _, u := range order {
		if err := EnqueueNotifyMail(ctx, tx, ws, u, ModeDirect, byUser[u]); err != nil {
			return err
		}
	}
	return nil
}
