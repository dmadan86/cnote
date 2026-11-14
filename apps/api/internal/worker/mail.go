package worker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/jobs"
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
	if err := SendMail(c.Context, smtpURL, os.Getenv("EMAIL_FROM"), to, "You were mentioned in "+title, body); err != nil {
		slog.Warn("mention email failed", "error", err)
	}
	return nil
}

// plainAuth is AUTH PLAIN without net/smtp's refusal to send credentials over an unencrypted link: the operator
// chose the transport in SMTP_URL (like lettre, which only encrypts when the URL says so).
type plainAuth struct{ user, pass string }

func (a plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}
func (a plainAuth) Next([]byte, bool) ([]byte, error) {
	return nil, errors.New("unexpected server challenge")
}

// SendMail delivers one plain-text message. smtp_url follows lettre's from_url: smtps:// is implicit TLS (port
// 465), smtp:// is cleartext (port 25) unless ?tls=required (STARTTLS, mandatory) or ?tls=opportunistic.
func SendMail(ctx context.Context, smtpURL, from, to, subject, body string) error {
	fa, err := mail.ParseAddress(from)
	if err != nil {
		return err
	}
	ta, err := mail.ParseAddress(to)
	if err != nil {
		return err
	}
	u, err := url.Parse(smtpURL)
	if err != nil {
		return err
	}
	port := u.Port()
	implicit := false
	switch u.Scheme {
	case "smtps":
		implicit = true
		if port == "" {
			port = "465"
		}
	case "smtp":
		if port == "" {
			port = "25"
		}
	default:
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	host := u.Hostname()
	d := net.Dialer{Timeout: 30 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	if implicit {
		conn = tls.Client(conn, &tls.Config{ServerName: host})
	}
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	cl, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer cl.Close()
	if mode := u.Query().Get("tls"); !implicit && (mode == "required" || mode == "opportunistic") {
		if ok, _ := cl.Extension("STARTTLS"); ok {
			if err := cl.StartTLS(&tls.Config{ServerName: host}); err != nil {
				return err
			}
		} else if mode == "required" {
			return errors.New("server does not support STARTTLS")
		}
	}
	if u.User != nil {
		pass, _ := u.User.Password()
		if err := cl.Auth(plainAuth{u.User.Username(), pass}); err != nil {
			return err
		}
	}
	if err := cl.Mail(fa.Address); err != nil {
		return err
	}
	if err := cl.Rcpt(ta.Address); err != nil {
		return err
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(buildMessage(fa, ta, subject, body))); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}

func buildMessage(from, to *mail.Address, subject, body string) string {
	subject = strings.NewReplacer("\r", " ", "\n", " ").Replace(subject) // no header injection from note titles
	var b strings.Builder
	b.WriteString("From: " + from.String() + "\r\n")
	b.WriteString("To: " + to.String() + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
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
