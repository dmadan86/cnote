package emailtpl

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/docs"
)

// The Values functions read one record's placeholder values (contact.name and the kind's own keys; Render fills the
// common ones). They do no permission check: the caller (share.email, preview) has already checked page access and
// finance access. The link key is added by the caller.

func nz(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// InvoiceValues returns invoice.* and contact.name for an invoice note.
func InvoiceValues(c *command.Ctx, noteID string) (map[string]string, error) {
	v, _, err := invoiceValues(c, noteID)
	return v, err
}

func invoiceValues(c *command.Ctx, id string) (map[string]string, string, error) {
	var number, contact *string
	var cur string
	var d, due *time.Time
	var total, paid, credited, off int64
	err := c.Tx.QueryRow(c.Context,
		`select i.number, i.currency::text, i.invoice_date, i.due_date, i.total_minor, i.paid_minor, i.credited_minor, i.written_off_minor, cn.title
		 from billing.invoices i join core.notes n on n.id = i.note_id left join core.notes cn on cn.id = i.contact_id
		 where i.note_id = $1::uuid and i.kind = 'invoice' and n.deleted_at is null`, id).
		Scan(&number, &cur, &d, &due, &total, &paid, &credited, &off, &contact)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", command.ErrInvalid("that is not an invoice")
	}
	if err != nil {
		return nil, "", command.DB(err)
	}
	num := "DRAFT"
	if number != nil {
		num = *number
	}
	overdue := 0
	if due != nil {
		if days := int(docs.TodayIST().Truncate(24*time.Hour).Sub(*due).Hours() / 24); days > 0 {
			overdue = days
		}
	}
	v := map[string]string{"contact.name": nz(contact), "invoice.number": num, "invoice.date": docs.OptDate(d), "invoice.due_date": dueOrIssue(d, due),
		"invoice.total": docs.Money(total, cur), "invoice.balance": docs.Money(total-paid-credited-off, cur), "invoice.days_overdue": fmt.Sprint(overdue)}
	return v, label(num, nz(contact)), nil
}

// QuoteValues returns quote.* and contact.name for a quotation note.
func QuoteValues(c *command.Ctx, noteID string) (map[string]string, error) {
	v, _, err := quoteValues(c, noteID)
	return v, err
}

func quoteValues(c *command.Ctx, id string) (map[string]string, string, error) {
	var number, contact *string
	var cur string
	var d, until *time.Time
	var total int64
	err := c.Tx.QueryRow(c.Context,
		`select q.number, q.currency::text, q.quote_date, q.valid_until, q.total_minor, cn.title
		 from billing.quotations q join core.notes n on n.id = q.note_id left join core.notes cn on cn.id = q.contact_id
		 where q.note_id = $1::uuid and n.deleted_at is null`, id).Scan(&number, &cur, &d, &until, &total, &contact)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", command.ErrInvalid("that is not a quotation")
	}
	if err != nil {
		return nil, "", command.DB(err)
	}
	num := "DRAFT"
	if number != nil {
		num = *number
	}
	v := map[string]string{"contact.name": nz(contact), "quote.number": num, "quote.date": docs.OptDate(d), "quote.valid_until": docs.OptDate(until),
		"quote.total": docs.Money(total, cur)}
	return v, label(num, nz(contact)), nil
}

// ReceiptValues returns receipt.* and contact.name for a receipt voucher note.
func ReceiptValues(c *command.Ctx, noteID string) (map[string]string, error) {
	v, _, err := receiptValues(c, noteID)
	return v, err
}

func receiptValues(c *command.Ctx, id string) (map[string]string, string, error) {
	var number string
	var contact *string
	var got time.Time
	var amount int64
	err := c.Tx.QueryRow(c.Context,
		`select r.number, r.received_at, r.amount_minor, cn.title
		 from billing.receipts r join core.notes n on n.id = r.note_id left join core.notes cn on cn.id = r.contact_id
		 where r.note_id = $1::uuid and r.kind = 'receipt' and n.deleted_at is null`, id).Scan(&number, &got, &amount, &contact)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", command.ErrInvalid("that is not a receipt")
	}
	if err != nil {
		return nil, "", command.DB(err)
	}
	v := map[string]string{"contact.name": nz(contact), "receipt.number": number, "receipt.date": docs.Date(got),
		"receipt.amount": docs.Money(amount, "INR")}
	return v, label(number, nz(contact)), nil
}

func label(number, contact string) string {
	if contact == "" {
		return number
	}
	return number + " · " + contact
}

// sampleValues is every field's example, used when the workspace has no record of the kind yet.
func sampleValues(ev EventInfo) map[string]string {
	v := map[string]string{}
	for _, f := range ev.Fields {
		v[f.Key] = f.Example
	}
	return v
}

// previewLinkBase: a sample link in the preview; nothing is created until a real send.
func sampleLink() string {
	if b := strings.TrimRight(os.Getenv("PUBLIC_WEB_URL"), "/"); strings.HasPrefix(b, "https://") && safeLink(b) {
		return b + "/p/sample-link"
	}
	return "https://app.example.com/p/sample-link"
}

// dueOrIssue: an invoice without a due date is due on receipt, so the wording "due on …" never ends blank.
func dueOrIssue(issued, due *time.Time) string {
	if s := docs.OptDate(due); s != "" {
		return s
	}
	return docs.OptDate(issued)
}
