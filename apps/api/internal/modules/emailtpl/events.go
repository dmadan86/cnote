package emailtpl

import "slices"

func f(key, label, example string) Field { return Field{key, label, example} }

var commonFields = []Field{
	f("contact.name", "Customer name", "Meera Traders"),
	f("workspace.name", "Business name", "Acme Works"),
	f("company.legal_name", "Legal name", "Acme Works Pvt Ltd"),
	f("company.gstin", "GSTIN", "29ABCDE1234F1Z5"),
	f("company.phone", "Business phone", "+91 98765 43210"),
	f("company.email", "Business email", "accounts@acme.example"),
	f("company.upi_vpa", "UPI ID", "acme@okbank"),
	f("user.name", "Your name", "Ravi Kumar"),
	f("today", "Today's date", "18 Nov 2026"),
}

var invoiceFields = []Field{
	f("invoice.number", "Invoice number", "INV/2026-27/0042"),
	f("invoice.date", "Invoice date", "1 Nov 2026"),
	f("invoice.due_date", "Due date", "30 Nov 2026"),
	f("invoice.total", "Invoice total", "₹1,18,000.00"),
	f("invoice.balance", "Amount still to pay", "₹1,18,000.00"),
	f("invoice.days_overdue", "Days overdue", "12"),
	f("invoice.link", "Link to view and pay", "https://app.example.com/p/sample"),
}

var quoteFields = []Field{
	f("quote.number", "Quotation number", "QT/2026-27/0007"),
	f("quote.date", "Quotation date", "1 Nov 2026"),
	f("quote.valid_until", "Valid until", "30 Nov 2026"),
	f("quote.total", "Quotation total", "₹1,18,000.00"),
	f("quote.link", "Link to view and accept", "https://app.example.com/p/sample"),
}

var receiptFields = []Field{
	f("receipt.number", "Receipt number", "RV/2026-27/0003"),
	f("receipt.date", "Date received", "18 Nov 2026"),
	f("receipt.amount", "Amount received", "₹50,000.00"),
	f("receipt.link", "Link to the receipt", "https://app.example.com/p/sample"),
}

var ticketFields = []Field{
	f("ticket.ref", "Ticket reference", "TKT-000123"),
	f("ticket.subject", "Ticket subject", "Question about my invoice"),
	f("ticket.message", "The reply text", "Thanks for writing. We have fixed this."),
}

func fields(extra ...[]Field) []Field {
	out := slices.Clone(commonFields)
	for _, e := range extra {
		out = append(out, e...)
	}
	return out
}

const (
	gInvoices  = "Invoices"
	gReminders = "Payment reminders"
	gQuotes    = "Quotes and receipts"
	gSupport   = "Support"
)

var events = []EventInfo{
	{Event: "invoice.sent", Label: "Invoice sent", Group: gInvoices, SentWhen: "You email an invoice to a customer.",
		DefaultSubject: "Invoice {{invoice.number}} from {{workspace.name}}",
		DefaultBody: "Hello {{contact.name}},\n\nThank you for your business. Here is invoice **{{invoice.number}}** for **{{invoice.total}}**, due on {{invoice.due_date}}.\n\n" +
			"You can view it and pay online here:\n{{invoice.link}}\n\nIf you have any questions, just reply to this email.",
		LinkKey: "invoice.link", Fields: fields(invoiceFields)},
	{Event: "invoice.reminder_1", Label: "Friendly reminder", Group: gReminders, SentWhen: "You send a first reminder for an unpaid invoice.",
		DefaultSubject: "Friendly reminder: invoice {{invoice.number}}",
		DefaultBody: "Hello {{contact.name}},\n\nA quick reminder that invoice **{{invoice.number}}** still has **{{invoice.balance}}** to be paid. It was due on {{invoice.due_date}}.\n\n" +
			"If you have already paid, thank you, please ignore this message. Otherwise you can view and pay it here:\n{{invoice.link}}\n\nIf anything is unclear, just reply to this email.",
		LinkKey: "invoice.link", Fields: fields(invoiceFields)},
	{Event: "invoice.reminder_2", Label: "Second reminder", Group: gReminders, SentWhen: "You send a second reminder for an unpaid invoice.",
		DefaultSubject: "Second reminder: invoice {{invoice.number}} is overdue",
		DefaultBody: "Hello {{contact.name}},\n\nInvoice **{{invoice.number}}** is now {{invoice.days_overdue}} days overdue, with **{{invoice.balance}}** still unpaid.\n\n" +
			"Please arrange the payment this week. You can view and pay it here:\n{{invoice.link}}\n\nIf there is a problem with the invoice, please tell us and we will sort it out.",
		LinkKey: "invoice.link", Fields: fields(invoiceFields)},
	{Event: "invoice.reminder_3", Label: "Final reminder", Group: gReminders, SentWhen: "You send a final reminder for an unpaid invoice.",
		DefaultSubject: "Final reminder: invoice {{invoice.number}}",
		DefaultBody: "Hello {{contact.name}},\n\nThis is our final reminder about invoice **{{invoice.number}}**. **{{invoice.balance}}** is {{invoice.days_overdue}} days overdue.\n\n" +
			"Please pay today, or reply to tell us when we can expect the payment:\n{{invoice.link}}\n\nThank you for your prompt attention.",
		LinkKey: "invoice.link", Fields: fields(invoiceFields)},
	{Event: "quote.sent", Label: "Quotation sent", Group: gQuotes, SentWhen: "You email a quotation to a customer.",
		DefaultSubject: "Quotation {{quote.number}} from {{workspace.name}}",
		DefaultBody: "Hello {{contact.name}},\n\nThank you for your interest. Here is our quotation **{{quote.number}}** for **{{quote.total}}**, valid until {{quote.valid_until}}.\n\n" +
			"You can review and accept it online:\n{{quote.link}}\n\nWe are happy to answer any questions.",
		LinkKey: "quote.link", Fields: fields(quoteFields)},
	{Event: "receipt.sent", Label: "Payment receipt", Group: gQuotes, SentWhen: "You email a payment receipt to a customer.",
		DefaultSubject: "Receipt {{receipt.number}} from {{workspace.name}}",
		DefaultBody:    "Hello {{contact.name}},\n\nThank you. We have received **{{receipt.amount}}** on {{receipt.date}}.\n\nYour receipt **{{receipt.number}}** is here:\n{{receipt.link}}",
		LinkKey:        "receipt.link", Fields: fields(receiptFields)},
	{Event: "ticket.reply", Label: "Support reply", Group: gSupport, SentWhen: "You reply to a customer on a support ticket. Only the sign-off and footer are yours to change; the body is the reply you type.",
		DefaultSubject: "[{{ticket.ref}}] {{ticket.subject}}", DefaultBody: "{{ticket.message}}", Fields: fields(ticketFields)},
}

// Events lists every templated event with its defaults and placeholders.
func Events() []EventInfo { return slices.Clone(events) }

func eventOf(key string) (EventInfo, bool) {
	for _, e := range events {
		if e.Event == key {
			return e, true
		}
	}
	return EventInfo{}, false
}

func (e EventInfo) has(key string) bool {
	return slices.ContainsFunc(e.Fields, func(f Field) bool { return f.Key == key })
}
