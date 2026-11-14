package publiclinks

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
)

type decisionMail struct{ to, subject, noteID string }

// notifyDecision tells the quote's owner, the person who made the link and every owner and admin that the customer
// accepted or declined: one in-app notification each (the unique index makes a replay a no-op), plus the address to mail.
// It runs once per decision, in the decision's transaction, only when the quote really changed state.
func notifyDecision(ctx context.Context, tx pgx.Tx, l link, status, customer string) ([]decisionMail, error) {
	var number, title *string
	if err := tx.QueryRow(ctx, `select q.number, n.title from billing.quotations q join core.notes n on n.id = q.note_id where q.note_id = $1::uuid`,
		l.note).Scan(&number, &title); err != nil {
		return nil, err
	}
	what := "quote " + strings.TrimSpace(strings.Join([]string{deref(number), deref(title)}, " "))
	verb := map[string]string{"accepted": "accepted", "rejected": "declined"}[status]
	text := fmt.Sprintf("%s %s %s", customer, verb, strings.TrimSpace(what))
	rows, err := tx.Query(ctx, `insert into core.notifications (workspace_id, user_id, kind, note_id, title)
		select $1::uuid, u.id, 'quote_decided', $2::uuid, $3 from core.users u
		 where u.id in (select m.user_id from core.memberships m where m.workspace_id = $1::uuid and m.deactivated_at is null
		                 and (m.role in ('owner','admin') or m.user_id in ($4::uuid, (select created_by from core.notes where id = $2::uuid))))
		on conflict do nothing returning user_id::text`, l.ws, l.note, text, l.creator)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	var out []decisionMail
	for _, id := range ids {
		var to string
		if err := tx.QueryRow(ctx, "select email from core.users where id = $1::uuid", id).Scan(&to); err != nil {
			return nil, err
		}
		out = append(out, decisionMail{to: to, subject: text, noteID: l.note})
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// sendDecisionMail uses the same SMTP_URL / EMAIL_FROM as the mention email; skipped when unset, a failure is logged
// and not retried (the in-app notification already exists).
func sendDecisionMail(m decisionMail, ws string) {
	smtpURL, from := getenv("SMTP_URL"), getenv("EMAIL_FROM")
	if smtpURL == "" || from == "" {
		return
	}
	base := getenv("PUBLIC_WEB_URL")
	if base == "" {
		base = getenv("PUBLIC_BASE_URL")
	}
	if base == "" {
		base = getenv("WEB_URL")
	}
	if base == "" {
		base = "http://localhost:3000"
	}
	body := fmt.Sprintf("%s\n\n%s/w/%s/n/%s\n", m.subject, strings.TrimRight(base, "/"), ws, m.noteID)
	if err := sendMail(smtpURL, from, m.to, m.subject, body); err != nil {
		slog.Warn("quote decision email failed", "error", err)
	}
}
