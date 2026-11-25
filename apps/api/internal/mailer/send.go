package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ErrNotConfigured means SMTP_URL or EMAIL_FROM is unset.
var ErrNotConfigured = errors.New("email is not configured (SMTP_URL, EMAIL_FROM)")

// Configured reports whether Send can work.
func Configured() bool {
	return strings.TrimSpace(os.Getenv("SMTP_URL")) != "" && strings.TrimSpace(os.Getenv("EMAIL_FROM")) != ""
}

var headerName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,63}$`)

var oneLine = strings.NewReplacer("\r", " ", "\n", " ")

func send(ctx context.Context, m Mail) (string, error) {
	smtpURL, rawFrom := strings.TrimSpace(os.Getenv("SMTP_URL")), strings.TrimSpace(os.Getenv("EMAIL_FROM"))
	if smtpURL == "" || rawFrom == "" {
		return "", ErrNotConfigured
	}
	fa, err := mail.ParseAddress(rawFrom)
	if err != nil {
		return "", fmt.Errorf("bad EMAIL_FROM: %w", err)
	}
	ta, err := mail.ParseAddress(m.To)
	if err != nil || strings.ContainsAny(m.To, "\r\n") {
		return "", errors.New("bad recipient address")
	}
	var reply *mail.Address
	if strings.TrimSpace(m.ReplyTo) != "" {
		if reply, err = mail.ParseAddress(m.ReplyTo); err != nil || strings.ContainsAny(m.ReplyTo, "\r\n") {
			return "", errors.New("bad reply-to address")
		}
	}
	base := fa.Name
	if base == "" {
		base = "C Note"
	}
	name := base
	if n := strings.TrimSpace(oneLine.Replace(m.FromName)); n != "" {
		name = n + " via " + base
	}
	from := (&mail.Address{Name: name, Address: fa.Address}).String()
	mid := m.MessageID
	if mid == "" {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		mid = "<" + hex.EncodeToString(b[:]) + "@" + fa.Address[strings.LastIndex(fa.Address, "@")+1:] + ">"
	}
	var extra strings.Builder
	keys := make([]string, 0, len(m.Headers))
	for k := range m.Headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !headerName.MatchString(k) {
			return "", errors.New("bad header name")
		}
		extra.WriteString(k + ": " + strings.TrimSpace(oneLine.Replace(m.Headers[k])) + "\r\n")
	}
	subject := mime.QEncoding.Encode("utf-8", strings.TrimSpace(oneLine.Replace(m.Subject)))
	build := func(eightBit bool) []byte {
		var b bytes.Buffer
		b.WriteString("From: " + from + "\r\nTo: " + ta.Address + "\r\n")
		if reply != nil {
			b.WriteString("Reply-To: " + reply.String() + "\r\n")
		}
		b.WriteString("Subject: " + subject + "\r\nDate: " + time.Now().Format(time.RFC1123Z) + "\r\nMessage-ID: " + mid + "\r\n" + extra.String() + "MIME-Version: 1.0\r\n")
		if m.HTML == "" {
			b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
			writeText(&b, m.Text, eightBit)
			return b.Bytes()
		}
		var r [12]byte
		_, _ = rand.Read(r[:])
		bd := "cnote-" + hex.EncodeToString(r[:])
		b.WriteString("Content-Type: multipart/alternative; boundary=\"" + bd + "\"\r\n\r\n")
		b.WriteString("--" + bd + "\r\nContent-Type: text/plain; charset=utf-8\r\n")
		writeText(&b, m.Text, eightBit)
		b.WriteString("\r\n--" + bd + "\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
		writeQP(&b, m.HTML)
		b.WriteString("\r\n--" + bd + "--\r\n")
		return b.Bytes()
	}
	return mid, Deliver(ctx, smtpURL, fa.Address, ta.Address, build)
}

func crlf(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

func writeQP(b *bytes.Buffer, s string) {
	w := quotedprintable.NewWriter(b)
	_, _ = w.Write([]byte(crlf(s)))
	_ = w.Close()
}

// writeText keeps ASCII (and, when the server takes 8BITMIME, UTF-8) readable in the raw data; otherwise QP.
func writeText(b *bytes.Buffer, s string, eightBit bool) {
	ascii, long := true, false
	for _, l := range strings.Split(s, "\n") {
		long = long || len(l) > 900
	}
	for _, r := range s {
		ascii = ascii && r < 128
	}
	switch {
	case long || (!ascii && !eightBit):
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		writeQP(b, s)
	case ascii:
		b.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n" + crlf(s))
	default:
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n" + crlf(s))
	}
}

type plainAuth struct{ user, pass string }

func (a plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}
func (plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if !more { // net/smtp calls Next with the 235 success line too
		return nil, nil
	}
	return nil, errors.New("unexpected server challenge")
}

// Deliver runs one SMTP dialogue: envelope MAIL FROM is the bare address `from`; build(eightBit) makes the data.
// smtp_url follows lettre's from_url: smtps:// is implicit TLS (465), smtp:// is cleartext (25) unless ?tls=required
// (STARTTLS mandatory) or ?tls=opportunistic; with credentials and no ?tls, STARTTLS is mandatory so AUTH PLAIN never
// goes out in cleartext (?tls=opportunistic or ?tls=none explicitly accepts that).
func Deliver(ctx context.Context, smtpURL, from, to string, build func(eightBit bool) []byte) error {
	u, err := url.Parse(smtpURL)
	if err != nil || u.Hostname() == "" {
		return errors.New("bad SMTP_URL")
	}
	port, implicit := u.Port(), u.Scheme == "smtps"
	switch u.Scheme {
	case "smtps":
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
	dl := time.Now().Add(60 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(dl) { // the caller's timeout bounds the whole dialogue
		dl = d
	}
	_ = conn.SetDeadline(dl)
	cl, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer cl.Close()
	mode := u.Query().Get("tls")
	if mode == "" && u.User != nil {
		mode = "required"
	}
	if !implicit && (mode == "required" || mode == "opportunistic") {
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
	if err := cl.Mail(from); err != nil {
		return err
	}
	if err := cl.Rcpt(to); err != nil {
		return err
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	eight, _ := cl.Extension("8BITMIME")
	if _, err := w.Write(build(eight)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	// The server accepted DATA: the mail is delivered whatever QUIT says.
	_ = cl.Quit()
	return nil
}
