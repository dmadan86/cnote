package billing

import "github.com/cnote/api/internal/command"

// Exports for the purchases module: line parsing, storage and GST math are reused, and so is journal posting.

// Leg is one side of a journal entry in paise.
type Leg = leg

func L(code string, debit, credit int64) Leg { return leg{code, debit, credit} }

// Post writes one balanced journal entry (zero legs dropped).
func Post(c *command.Ctx, sourceType, sourceID, memo string, legs []Leg) error {
	return post(c, sourceType, sourceID, memo, legs)
}

const AcctBank = acctBank

// LoadLines and WriteLines work on any invoice-shaped line table (constants only, never user input).
func LoadLines(c *command.Ctx, table, col, id string) (Lines, error) {
	l, err := loadLinesOf(c, table, col, id)
	return Lines{l}, err
}

func WriteLines(c *command.Ctx, table, col, id string, l Lines, tax *QuoteTax) (int64, error) {
	if tax == nil {
		return writeLinesTo(c, table, col, id, l.l, nil)
	}
	return writeLinesTo(c, table, col, id, l.l, &tax.t)
}

// WorkspaceState is the workspace's GSTIN and state: where input tax credit is claimed.
func WorkspaceState(c *command.Ctx) (gstin, state string, err error) {
	s, err := loadSeller(c, nil)
	return s.gstin, s.state, err
}

// ComputePurchaseTax is the GST charged by a vendor in vendorState for a supply received in place:
// same state is CGST+SGST, else IGST.
func ComputePurchaseTax(l Lines, vendorState, place string) (QuoteTax, error) {
	t, err := computeInvoice(gstLines(l.l), vendorState, place, false)
	if err != nil {
		return QuoteTax{}, gstErrToCmd(err)
	}
	return QuoteTax{Subtotal: t.subtotal, CGST: t.cgst, SGST: t.sgst, IGST: t.igst, Total: t.total, Place: place, SupplierState: vendorState, t: t}, nil
}

func ValidateGSTIN(s string) (string, error) {
	g, err := validateGSTIN(s)
	return g, gstErrToCmd(err)
}

func GSTINState(g string) string { return gstinState(g) }

// Ageing helpers shared with payables ageing.
var Buckets = buckets

func AgeBucket(days int64) string { return ageBucket(days) }

func DaysBetween(a, b string) int64 { return daysBetween(parseDate(a), parseDate(b)) }
