package billing

import (
	"encoding/json"
	"time"

	"github.com/cnote/api/internal/command"
)

// Exports for the quotes module: the smallest surface that lets quotations reuse invoice line parsing, storage
// and GST math without copying them. Quotations never post to the ledger.

// Lines is a validated, opaque set of document lines.
type Lines struct{ l []ln }

// QuoteTax is the GST of a quotation as invoice.issue would compute it, plus the frozen parties.
type QuoteTax struct {
	Subtotal, CGST, SGST, IGST, Total   int64
	Place, SupplierGSTIN, SupplierState string
	BuyerGSTIN                          *string
	t                                   invoiceTax
}

const (
	quoteLines = "billing.quotation_lines"
	quoteCol   = "quotation_id"
)

// DecodeLines reads and validates input[key] like invoice.create_draft does.
func DecodeLines(input json.RawMessage, key string, required bool) (Lines, bool, error) {
	o, err := parseObj(input)
	if err != nil {
		return Lines{}, false, err
	}
	raw, have, err := decodeLines(o, key, required)
	if err != nil || !have {
		return Lines{}, have, err
	}
	l, err := parseLines(raw)
	return Lines{l}, true, err
}

func LoadQuoteLines(c *command.Ctx, quoteID string) (Lines, error) {
	l, err := loadLinesOf(c, quoteLines, quoteCol, quoteID)
	return Lines{l}, err
}

// WriteQuoteLines replaces the lines; with tax the GST is stored, without only taxable amounts. Returns the subtotal.
func WriteQuoteLines(c *command.Ctx, quoteID string, l Lines, tax *QuoteTax) (int64, error) {
	if tax == nil {
		return writeLinesTo(c, quoteLines, quoteCol, quoteID, l.l, nil)
	}
	return writeLinesTo(c, quoteLines, quoteCol, quoteID, l.l, &tax.t)
}

// ComputeQuoteTax needs the workspace GSTIN. The place of supply is the given one, else the contact's state,
// else the state of the buyer GSTIN.
func ComputeQuoteTax(c *command.Ctx, l Lines, place, contactID, buyer *string, reverse bool) (QuoteTax, error) {
	s, err := loadSeller(c)
	if err != nil {
		return QuoteTax{}, err
	}
	if contactID != nil {
		ci, err := loadContact(c, *contactID)
		if err != nil {
			return QuoteTax{}, err
		}
		if ci.gstin != nil {
			buyer = ci.gstin
		}
		if place == nil {
			place = ci.state
		}
	}
	if place == nil && buyer != nil {
		p := gstinState(*buyer)
		place = &p
	}
	if place == nil {
		return QuoteTax{}, command.ErrInvalid("place of supply is unknown: set it or give the contact a state code")
	}
	t, err := computeInvoice(gstLines(l.l), s.state, *place, reverse)
	if err != nil {
		return QuoteTax{}, gstErrToCmd(err)
	}
	return QuoteTax{t.subtotal, t.cgst, t.sgst, t.igst, t.total, *place, s.gstin, s.state, buyer, t}, nil
}

// LoadContact checks the caller can read the contact and returns its name, GSTIN and state.
func LoadContact(c *command.Ctx, id string) (name string, gstin, state *string, err error) {
	ci, err := loadContact(c, id)
	return ci.name, ci.gstin, ci.state, err
}

func ValidateState(code string) error { return gstErrToCmd(validateState(code)) }

func NextNumber(c *command.Ctx, series, prefix, fy string) (string, error) {
	return nextNumber(c, series, prefix, fy)
}

func FinancialYear(d time.Time) string { return financialYear(d) }
func TodayIST() time.Time              { return todayIST() }

// WriteRecurringLines shares the validated invoice line representation with recurring templates.
func WriteRecurringLines(c *command.Ctx, id string, l Lines) error {
	_, err := writeLinesTo(c, "billing.recurring_invoice_lines", "recurring_invoice_id", id, l.l, nil)
	return err
}
