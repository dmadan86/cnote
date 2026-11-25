// Package mailer builds and sends one customer or staff email over SMTP_URL: parsed EMAIL_FROM, a
// "<business> via <sender>" display name, Reply-To, Date, Message-ID and multipart/alternative (text first,
// then HTML), UTF-8 throughout. It is a leaf package so modules can send without import cycles.
package mailer

import "context"

type Mail struct {
	To       string `json:"to,omitempty"`
	ReplyTo  string `json:"reply_to,omitempty"`  // optional; must be one valid address
	FromName string `json:"from_name,omitempty"` // business name; rendered as "<FromName> via <EMAIL_FROM name or C Note>"
	Subject  string `json:"subject,omitempty"`
	Text     string `json:"text,omitempty"` // required
	HTML     string `json:"html,omitempty"` // optional; when empty the mail is text/plain only
	// MessageID is optional ("<id@host>"); empty means a random one at the sender's domain.
	MessageID string `json:"message_id,omitempty"`
	// Headers are extra message headers (List-Unsubscribe, List-Unsubscribe-Post). Names are letters, digits and hyphens;
	// a value is put on one line. Set by code, never by a customer.
	Headers map[string]string `json:"headers,omitempty"`
	// Notify ("kind:ref") names a worker hook that runs right before the outbox send (may cancel it) and after it. Ignored by Send.
	Notify string `json:"notify,omitempty"`
}

// Send delivers m and returns the Message-ID it set. Implemented by agent B (see docs/design/customer-messages.md).
func Send(ctx context.Context, m Mail) (messageID string, err error) {
	return send(ctx, m)
}
