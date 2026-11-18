// Package emailtpl holds the workspace's customer message templates (core.email_templates), their code defaults,
// the per-event placeholder whitelist and the renderer that turns a template plus values into subject, text and
// HTML in one fixed branded layout. See docs/design/customer-messages.md.
package emailtpl

// Override replaces the stored/default subject or body for one render (editor preview, one-off send).
type Override struct {
	Subject string
	Body    string
}

// Rendered is what callers hand to mailer.Send.
type Rendered struct {
	Subject  string
	Body     string // template body with values filled in, no link append, sign-off or footer (what a one-off edit starts from)
	Text     string
	HTML     string
	FromName string
	ReplyTo  string // empty for ticket.reply (replies must reach ticket intake)
}

// Field is one placeholder offered for an event.
type Field struct {
	Key     string `json:"key"`     // "invoice.number"
	Label   string `json:"label"`   // "Invoice number"
	Example string `json:"example"` // "INV/2026-27/0042"
}

// EventInfo describes one templated event.
type EventInfo struct {
	Event          string  `json:"event"` // "invoice.sent"
	Label          string  `json:"label"`
	Group          string  `json:"group"` // "Invoices" | "Payment reminders" | "Quotes and receipts" | "Support"
	SentWhen       string  `json:"sent_when"`
	DefaultSubject string  `json:"default_subject"`
	DefaultBody    string  `json:"default_body"`
	LinkKey        string  `json:"link_key,omitempty"` // placeholder appended when the body lacks it, e.g. "invoice.link"
	Fields         []Field `json:"fields"`
}

// Contract implemented by agent A:
//
//	func Events() []EventInfo
//	func Render(c *command.Ctx, event string, values map[string]string, o *Override) (Rendered, error)
//	func Commands() []command.Command   // email_template.list/save/reset/preview/test
