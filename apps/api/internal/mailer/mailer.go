// Package mailer builds and sends one customer or staff email over SMTP_URL: parsed EMAIL_FROM, a
// "<business> via <sender>" display name, Reply-To, Date, Message-ID and multipart/alternative (text first,
// then HTML), UTF-8 throughout. It is a leaf package so modules can send without import cycles.
package mailer

import "context"

type Mail struct {
	To       string
	ReplyTo  string // optional; must be one valid address
	FromName string // business name; rendered as "<FromName> via <EMAIL_FROM name or C Note>"
	Subject  string
	Text     string // required
	HTML     string // optional; when empty the mail is text/plain only
}

// Send delivers m and returns the Message-ID it set. Implemented by agent B (see docs/design/customer-messages.md).
func Send(ctx context.Context, m Mail) (messageID string, err error) {
	return send(ctx, m)
}
