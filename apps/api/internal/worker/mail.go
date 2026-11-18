package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/mailer"
)

// mentionEmail is the event handler for mention.created (notifications::email_handler). Skipped when SMTP_URL is
// unset; a send failure is logged, never retried (the in-app notification already exists and a retry could
// duplicate the email).
func mentionEmail(c *command.Ctx, ev jobs.Event) error {
	smtpURL, ok := os.LookupEnv("SMTP_URL")
	if !ok {
		slog.Debug("SMTP_URL unset; skipping mention email")
		return nil
	}
	var p struct {
		NotificationID string `json:"notification_id"`
	}
	if json.Unmarshal(ev.Payload, &p) != nil || !isUUID(p.NotificationID) {
		return nil
	}
	var to, title, noteID string
	var actor *string
	err := c.Tx.QueryRow(c.Context,
		`select u.email, n.title, a.name, n.note_id::text from core.notifications n
		 join core.users u on u.id = n.user_id left join core.users a on a.id = n.actor_user_id where n.id = $1::uuid`, p.NotificationID).
		Scan(&to, &title, &actor, &noteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	base := "http://localhost:3000"
	for _, k := range []string{"PUBLIC_WEB_URL", "PUBLIC_BASE_URL", "WEB_URL"} {
		if v := os.Getenv(k); v != "" {
			base = v
			break
		}
	}
	who := "Someone"
	if actor != nil {
		who = *actor
	}
	body := fmt.Sprintf("%s mentioned you in %s\n\n%s/w/%s/n/%s\n", who, title, strings.TrimRight(base, "/"), ev.WorkspaceID, noteID)
	if err := SendMail(c.Context, smtpURL, os.Getenv("EMAIL_FROM"), to, "You were mentioned in "+title, body, fmt.Sprintf("<cnote-mention-%d@cnote>", ev.ID)); err != nil {
		slog.Warn("mention email failed", "error", err)
	}
	return nil
}

// SendMail delivers one plain-text message over mailer's SMTP dialogue (smtp_url rules are documented on
// mailer.Deliver); the per-event Message-ID makes a re-sent mail recognisable.
func SendMail(ctx context.Context, smtpURL, from, to, subject, body, messageID string) error {
	fa, err := mail.ParseAddress(from)
	if err != nil {
		return err
	}
	ta, err := mail.ParseAddress(to)
	if err != nil {
		return err
	}
	return mailer.Deliver(ctx, smtpURL, fa.Address, ta.Address, func(bool) []byte {
		return []byte(buildMessage(fa, ta, subject, body, messageID))
	})
}

func buildMessage(from, to *mail.Address, subject, body, messageID string) string {
	subject = strings.NewReplacer("\r", " ", "\n", " ").Replace(subject) // no header injection from note titles
	var b strings.Builder
	b.WriteString("From: " + from.String() + "\r\n")
	b.WriteString("To: " + to.String() + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: " + messageID + "\r\n") // per event: a re-sent mail is recognisable
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n")
	ascii := true
	for _, r := range body {
		if r > 127 {
			ascii = false
		}
	}
	if ascii {
		b.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))
		return b.String()
	}
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	var qb strings.Builder
	qw := quotedprintable.NewWriter(&qb)
	_, _ = qw.Write([]byte(body))
	_ = qw.Close()
	b.WriteString(qb.String())
	return b.String()
}
