package billing

import (
	"fmt"
	"math/big"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// Exports for the receipts module (advance receipt vouchers): the GST split of a tax-inclusive advance and the
// settlement of an open invoice from an advance. Ledger posting stays in receipts (it uses Post).

// AdvanceSplit is a gross advance divided into taxable value and tax.
type AdvanceSplit struct{ Taxable, CGST, SGST, IGST int64 }

// AdvanceTax treats the gross as tax-inclusive (GST on an advance is paid out of what was received): tax =
// gross x rate / (100 + rate), rounded half up like invoice tax. An unknown place of supply ("") is inter-state
// (Rule 50 default); otherwise the split is the invoice rule: same state CGST+SGST (CGST the floor half), else IGST.
func AdvanceTax(gross int64, rateBP int, supplierState, place string) (AdvanceSplit, error) {
	if gross <= 0 || rateBP < 0 || rateBP > 2800 {
		return AdvanceSplit{}, command.ErrInvalid("amount must be positive and the GST rate between 0 and 28%")
	}
	s := inter
	if place != "" {
		var err error
		if s, err = supplyKind(supplierState, place); err != nil {
			return AdvanceSplit{}, gstErrToCmd(err)
		}
	}
	d := big.NewInt(int64(10000 + rateBP))
	x := new(big.Int).Mul(big.NewInt(gross), big.NewInt(int64(rateBP)))
	tax := x.Add(x, new(big.Int).Div(d, big.NewInt(2))).Div(x, d).Int64()
	out := AdvanceSplit{Taxable: gross - tax}
	if s == intra {
		out.CGST, out.SGST = tax/2, tax-tax/2
	} else {
		out.IGST = tax
	}
	return out, nil
}

// Advanced is the invoice after an advance was applied to it.
type Advanced struct {
	PaymentID        string `json:"payment_id"`
	InvoiceID        string `json:"invoice_id"`
	Number           string `json:"number"`
	Status           string `json:"status"`
	PaidMinor        int64  `json:"paid_minor"`
	OutstandingMinor int64  `json:"outstanding_minor"`
}

// SettleFromAdvance reduces an open invoice of the given customer by amount, as invoice payment.record does but
// with method 'advance' and no bank leg (the money came in with the receipt). It emits payment.recorded and,
// when nothing is left, invoice.paid. The caller posts the ledger entry.
func SettleFromAdvance(c *command.Ctx, invoiceID, contactID string, amount int64, ref string) (Advanced, error) {
	var a Advanced
	if _, err := access.AssertNoteAccess(c, invoiceID, true); err != nil {
		return a, err
	}
	inv, err := loadInvoice(c, invoiceID, true)
	if err != nil {
		return a, err
	}
	if err := requireOpen(inv); err != nil {
		return a, err
	}
	if inv.ContactID == nil || *inv.ContactID != contactID {
		return a, command.ErrInvalid("the invoice belongs to a different customer than the receipt")
	}
	if amount <= 0 {
		return a, command.ErrInvalid("amount must be positive")
	}
	if amount > inv.outstanding() {
		return a, command.ErrInvalid(fmt.Sprintf("amount %d exceeds the outstanding amount %d", amount, inv.outstanding()))
	}
	if err := c.Tx.QueryRow(c.Context,
		`insert into billing.payments (workspace_id, invoice_id, amount_minor, method, reference)
		 values ($1::uuid,$2::uuid,$3,'advance',$4) returning id::text`,
		c.WorkspaceID(), invoiceID, amount, ref).Scan(&a.PaymentID); err != nil {
		return a, command.DB(err)
	}
	paid := inv.PaidMinor + amount
	status := settledStatus(inv.TotalMinor, paid, inv.CreditedMinor, inv.WrittenOffMinor, inv.Status)
	if _, err := c.Tx.Exec(c.Context, "update billing.invoices set paid_minor = $2, status = $3 where note_id = $1::uuid", invoiceID, paid, status); err != nil {
		return a, command.DB(err)
	}
	if _, err := c.Emit("payment.recorded", &invoiceID, map[string]any{"payment_id": a.PaymentID, "amount_minor": amount, "status": status, "method": "advance"}); err != nil {
		return a, err
	}
	if status == "paid" {
		if _, err := c.Emit("invoice.paid", &invoiceID, map[string]any{"number": inv.Number, "total_minor": inv.TotalMinor}); err != nil {
			return a, err
		}
	}
	a.InvoiceID, a.Number, a.Status, a.PaidMinor = invoiceID, deref(inv.Number), status, paid
	a.OutstandingMinor = inv.TotalMinor - paid - inv.CreditedMinor - inv.WrittenOffMinor
	return a, nil
}

// Account codes of the Rust-seeded chart that receipts post to.
const (
	AcctReceivables = acctReceivables
	AcctCGST        = acctCGST
	AcctSGST        = acctSGST
	AcctIGST        = acctIGST
)
