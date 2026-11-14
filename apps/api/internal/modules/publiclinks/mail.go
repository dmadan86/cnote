package publiclinks

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"time"
)

func getenv(k string) string { return strings.TrimSpace(os.Getenv(k)) }

// sendMail sends a plain-text message over SMTP_URL (smtps:// = implicit TLS, smtp:// = STARTTLS when offered).
// shortcut: no retry queue and no HTML; a failure is returned so the caller can roll back the link.
func sendMail(smtpURL, from, to, subject, body string) error {
	u, err := url.Parse(smtpURL)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("bad SMTP_URL")
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"smtps": "465", "smtp": "587"}[u.Scheme]
	}
	addr := net.JoinHostPort(u.Hostname(), port)
	d := net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	if u.Scheme == "smtps" {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: u.Hostname()})
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	cl, err := smtp.NewClient(conn, u.Hostname())
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer cl.Close()
	if u.Scheme == "smtp" {
		if ok, _ := cl.Extension("STARTTLS"); ok {
			if err := cl.StartTLS(&tls.Config{ServerName: u.Hostname()}); err != nil {
				return err
			}
		}
	}
	if pw, ok := u.User.Password(); ok {
		if err := cl.Auth(smtp.PlainAuth("", u.User.Username(), pw, u.Hostname())); err != nil {
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
	msg := "From: " + from + "\r\nTo: " + to + "\r\nSubject: " + strings.NewReplacer("\r", " ", "\n", " ").Replace(subject) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + strings.ReplaceAll(body, "\n", "\r\n")
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}

// SendMail is sendMail for other modules (chat mention emails).
func SendMail(smtpURL, from, to, subject, body string) error {
	return sendMail(smtpURL, from, to, subject, body)
}
