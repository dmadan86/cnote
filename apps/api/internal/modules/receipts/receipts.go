// Package receipts: advance receipt vouchers (GST Rule 50) and refund vouchers (Rule 51). Go-only: Rust has no receipt.*.
// A voucher is a typed note ['receipt_voucher'] plus a billing.receipts row, immutable once issued.
//
// Postings (CA review: sources are CBIC Rule 50/51, Notification 66/2017-CT for goods, and the research doc WP4):
//
//	receive  Dr bank G / Cr customer advances G; services also Dr GST paid on advances T / Cr output CGST+SGST|IGST T
//	apply A  Dr customer advances A / Cr receivables A; Dr output GST t / Cr GST paid on advances t (t = T x A / G)
//	refund R Dr customer advances R / Cr bank R; Dr output GST t / Cr GST paid on advances t (exact reversal)
//
// The invoice the advance is applied to already posted its full output GST, so the tax share released on apply or
// refund is what keeps GST paid on advances at zero once the advance is consumed. Account codes default to 2300 and
// 1310 and can be set per workspace in settings (advance_account, advance_gst_account).
//
// Flow: issued -> partly_applied -> applied | refunded (the status follows applied_minor and refunded_minor).
package receipts

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/billing"
	"github.com/cnote/api/internal/modules/notes"
)

const (
	defaultAdvances = "2300"
	defaultGSTPaid  = "1310"
)

func Commands() []command.Command {
	mi, rd := command.GroupManageInvoices, command.GroupRead
	confirm := func(c command.Command) command.Command { c.Confirm = true; return c }
	admin := func(c command.Command) command.Command { c.MinRole = command.Admin; return c }
	nc := command.NewCommand
	return []command.Command{
		confirm(nc("receipt.create", mi, create)),
		confirm(nc("receipt.apply", mi, apply)),
		admin(confirm(nc("receipt.refund", mi, refund))),
		nc("receipt.get", rd, get),
		nc("receipt.list", rd, list),
	}
}

var (
	uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	codeRe = regexp.MustCompile(`^[0-9]{4,10}$`)
)

type in struct {
	ID            string  `json:"id"`
	SpaceID       string  `json:"space_id"`
	ContactID     *string `json:"contact_id"`
	QuotationID   *string `json:"quotation_id"`
	InvoiceID     *string `json:"invoice_id"`
	AmountMinor   *int64  `json:"amount_minor"`
	SupplyType    *string `json:"supply_type"`
	GSTRateBP     *int64  `json:"gst_rate_bp"`
	PlaceOfSupply *string `json:"place_of_supply"`
	Description   *string `json:"description"`
	Method        *string `json:"method"`
	Reference     *string `json:"reference"`
	ReceivedAt    *string `json:"received_at"`
	Status        *string `json:"status"`
	Kind          *string `json:"kind"`
	Limit         *int64  `json:"limit"`
}

func decode(raw json.RawMessage) (in, error) {
	var v in
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, command.ErrInvalid("invalid input")
	}
	for _, id := range []string{v.ID, v.SpaceID} {
		if id != "" && !uuidRe.MatchString(id) {
			return v, command.ErrInvalid("invalid id")
		}
	}
	for name, p := range map[string]*string{"contact_id": v.ContactID, "quotation_id": v.QuotationID, "invoice_id": v.InvoiceID} {
		if p != nil {
			if !uuidRe.MatchString(*p) {
				return v, command.ErrInvalid("invalid " + name)
			}
			*p = strings.ToLower(*p)
		}
	}
	if v.ReceivedAt != nil {
		if _, err := time.Parse("2006-01-02", *v.ReceivedAt); err != nil {
			return v, command.ErrInvalid("received_at must be YYYY-MM-DD")
		}
	}
	for _, f := range []struct {
		p   *string
		max int
		n   string
	}{{v.Method, 40, "method"}, {v.Reference, 200, "reference"}, {v.Description, 500, "description"}} {
		if f.p != nil && len(*f.p) > f.max {
			return v, command.ErrInvalid(f.n + " is too long")
		}
	}
	v.ID, v.SpaceID = strings.ToLower(v.ID), strings.ToLower(v.SpaceID)
	return v, nil
}

func requireID(v in) error {
	if v.ID == "" {
		return command.ErrInvalid("id is required")
	}
	return nil
}

func nilStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// ---------- rows ----------

type row struct {
	ID                string  `json:"id"`
	Kind              string  `json:"kind"`
	Number            string  `json:"number"`
	Status            string  `json:"status"`
	ContactID         string  `json:"contact_id"`
	QuotationID       *string `json:"quotation_id"`
	OriginalReceiptID *string `json:"original_receipt_id"`
	SupplyType        string  `json:"supply_type"`
	SupplierGSTIN     string  `json:"supplier_gstin"`
	SupplierState     string  `json:"supplier_state"`
	BuyerGSTIN        *string `json:"buyer_gstin"`
	PlaceOfSupply     *string `json:"place_of_supply"`
	Description       *string `json:"description"`
	GSTRateBP         int32   `json:"gst_rate_bp"`
	AmountMinor       int64   `json:"amount_minor"`
	TaxableMinor      int64   `json:"taxable_minor"`
	CGSTMinor         int64   `json:"cgst_minor"`
	SGSTMinor         int64   `json:"sgst_minor"`
	IGSTMinor         int64   `json:"igst_minor"`
	AppliedMinor      int64   `json:"applied_minor"`
	RefundedMinor     int64   `json:"refunded_minor"`
	UnappliedMinor    int64   `json:"unapplied_minor"`
	Method            string  `json:"method"`
	Reference         *string `json:"reference"`
	ReceivedAt        string  `json:"received_at"`
	SpaceID           string  `json:"space_id"`
}

func (r row) tax() int64 { return r.CGSTMinor + r.SGSTMinor + r.IGSTMinor }

const cols = `r.note_id::text, r.kind, r.number, r.status, r.contact_id::text, r.quotation_id::text, r.original_receipt_id::text,
	r.supply_type, r.supplier_gstin, r.supplier_state, r.buyer_gstin, r.place_of_supply, r.description, r.gst_rate_bp,
	r.amount_minor, r.taxable_minor, r.cgst_minor, r.sgst_minor, r.igst_minor, r.applied_minor, r.refunded_minor,
	case when r.kind = 'receipt' then r.amount_minor - r.applied_minor - r.refunded_minor else 0 end,
	r.method, r.reference, r.received_at::text, n.space_id::text`

func scan(r pgx.Row) (row, error) {
	var x row
	err := r.Scan(&x.ID, &x.Kind, &x.Number, &x.Status, &x.ContactID, &x.QuotationID, &x.OriginalReceiptID, &x.SupplyType,
		&x.SupplierGSTIN, &x.SupplierState, &x.BuyerGSTIN, &x.PlaceOfSupply, &x.Description, &x.GSTRateBP, &x.AmountMinor,
		&x.TaxableMinor, &x.CGSTMinor, &x.SGSTMinor, &x.IGSTMinor, &x.AppliedMinor, &x.RefundedMinor, &x.UnappliedMinor,
		&x.Method, &x.Reference, &x.ReceivedAt, &x.SpaceID)
	return x, err
}

func load(c *command.Ctx, id string, lock bool) (row, error) {
	q := "select " + cols + ` from billing.receipts r join core.notes n on n.id = r.note_id
		where r.note_id = $1::uuid and n.deleted_at is null `
	if lock {
		q += "for update of r"
	}
	r, err := scan(c.Tx.QueryRow(c.Context, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, command.ErrNotFound("receipt")
	}
	return r, command.DB(err)
}

type appOut struct {
	ID            string     `json:"id"`
	InvoiceID     string     `json:"invoice_id"`
	InvoiceNumber *string    `json:"invoice_number"`
	AmountMinor   int64      `json:"amount_minor"`
	AppliedAt     command.Ts `json:"applied_at"`
}

type refundOut struct {
	ID          string `json:"id"`
	Number      string `json:"number"`
	AmountMinor int64  `json:"amount_minor"`
	ReceivedAt  string `json:"received_at"`
}

type detail struct {
	row
	ContactName  *string     `json:"contact_name"`
	Applications []appOut    `json:"applications"`
	Refunds      []refundOut `json:"refunds"`
}

func detailOf(c *command.Ctx, id string) (detail, error) {
	r, err := load(c, id, false)
	if err != nil {
		return detail{}, err
	}
	d := detail{row: r, Applications: []appOut{}, Refunds: []refundOut{}}
	// The contact's name only when the caller may read the contact page.
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return d, err
	}
	uid, admin := access.Viewer(c)
	err = c.Tx.QueryRow(c.Context,
		"select title from core.notes where id = $1::uuid and core.note_access(id, $2::uuid, $3, $4::text[]::uuid[]) <> 'none'",
		r.ContactID, uid, admin, spaces).Scan(&d.ContactName)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return d, command.DB(err)
	}
	rows, err := c.Tx.Query(c.Context,
		`select a.id::text, a.invoice_id::text, i.number, a.amount_minor, a.applied_at
		 from billing.receipt_applications a join billing.invoices i on i.note_id = a.invoice_id
		 where a.receipt_id = $1::uuid order by a.applied_at, a.id`, id)
	if err != nil {
		return d, command.DB(err)
	}
	for rows.Next() {
		var a appOut
		var at time.Time
		if err := rows.Scan(&a.ID, &a.InvoiceID, &a.InvoiceNumber, &a.AmountMinor, &at); err != nil {
			rows.Close()
			return d, command.DB(err)
		}
		a.AppliedAt = command.Ts(at)
		d.Applications = append(d.Applications, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return d, command.DB(err)
	}
	rrows, err := c.Tx.Query(c.Context,
		`select note_id::text, number, amount_minor, received_at::text from billing.receipts
		 where original_receipt_id = $1::uuid order by created_at, note_id`, id)
	if err != nil {
		return d, command.DB(err)
	}
	defer rrows.Close()
	for rrows.Next() {
		var f refundOut
		if err := rrows.Scan(&f.ID, &f.Number, &f.AmountMinor, &f.ReceivedAt); err != nil {
			return d, command.DB(err)
		}
		d.Refunds = append(d.Refunds, f)
	}
	return d, command.DB(rrows.Err())
}

// ---------- ledger ----------

type accounts struct{ advances, gstPaid string }

// ledgerAccounts reads the configured codes, seeds the two accounts when the workspace has none yet and refuses a
// code that already belongs to an account of the wrong kind (so an advance can never post into revenue).
func ledgerAccounts(c *command.Ctx) (accounts, error) {
	a := accounts{defaultAdvances, defaultGSTPaid}
	var adv, gst *string
	if err := c.Tx.QueryRow(c.Context,
		"select settings->>'advance_account', settings->>'advance_gst_account' from core.workspaces where id = $1::uuid",
		c.WorkspaceID()).Scan(&adv, &gst); err != nil {
		return a, command.DB(err)
	}
	for _, x := range []struct {
		set        *string
		dst        *string
		name, kind string
	}{{adv, &a.advances, "Customer advances", "liability"}, {gst, &a.gstPaid, "GST paid on advances", "asset"}} {
		if x.set != nil && *x.set != "" {
			if !codeRe.MatchString(*x.set) {
				return a, command.ErrInvalid("advance account codes in settings must be 4 to 10 digits")
			}
			*x.dst = *x.set
		}
		if _, err := c.Tx.Exec(c.Context,
			"insert into billing.accounts (workspace_id, code, name, kind) values ($1::uuid,$2,$3,$4) on conflict do nothing",
			c.WorkspaceID(), *x.dst, x.name, x.kind); err != nil {
			return a, command.DB(err)
		}
		var kind string
		if err := c.Tx.QueryRow(c.Context, "select kind from billing.accounts where code = $1", *x.dst).Scan(&kind); err != nil {
			return a, command.DB(err)
		}
		if kind != x.kind {
			return a, command.ErrConflict(fmt.Sprintf("account %s is a %s account; %s needs a %s account", *x.dst, kind, x.name, x.kind))
		}
	}
	return a, nil
}

// share is x x c / g rounded down: the part of a tax amount x released once c of the gross g has been consumed.
// Rounding down keeps the released taxable value non-negative; at c = g the whole of x is released.
func share(x, c, g int64) int64 {
	n := new(big.Int).Mul(big.NewInt(x), big.NewInt(c))
	return n.Div(n, big.NewInt(g)).Int64()
}

// release is the tax (and taxable value) of the slice of r between consumption before and after.
func release(r row, before, after int64) (cg, sg, ig, taxable int64) {
	cg = share(r.CGSTMinor, after, r.AmountMinor) - share(r.CGSTMinor, before, r.AmountMinor)
	sg = share(r.SGSTMinor, after, r.AmountMinor) - share(r.SGSTMinor, before, r.AmountMinor)
	ig = share(r.IGSTMinor, after, r.AmountMinor) - share(r.IGSTMinor, before, r.AmountMinor)
	return cg, sg, ig, after - before - cg - sg - ig
}

// releaseLegs moves the released tax from GST paid on advances back to the output GST accounts.
func releaseLegs(a accounts, cg, sg, ig int64) []billing.Leg {
	return []billing.Leg{billing.L(billing.AcctCGST, cg, 0), billing.L(billing.AcctSGST, sg, 0),
		billing.L(billing.AcctIGST, ig, 0), billing.L(a.gstPaid, 0, cg+sg+ig)}
}

func status(amount, applied, refunded int64) string {
	switch {
	case applied+refunded == 0:
		return "issued"
	case applied+refunded < amount:
		return "partly_applied"
	case applied > 0:
		return "applied"
	}
	return "refunded"
}

// ---------- create ----------

func create(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	if v.ContactID == nil || v.AmountMinor == nil || v.SupplyType == nil {
		return nil, command.ErrInvalid("contact_id, amount_minor and supply_type are required")
	}
	amount := *v.AmountMinor
	if amount <= 0 {
		return nil, command.ErrInvalid("amount must be positive")
	}
	if *v.SupplyType != "services" && *v.SupplyType != "goods" {
		return nil, command.ErrInvalid("supply_type must be services or goods")
	}
	rate := int64(1800) // Rule 50: 18% when the rate is not known at receipt
	if v.GSTRateBP != nil {
		rate = *v.GSTRateBP
	}
	if rate < 0 || rate > 2800 {
		return nil, command.ErrInvalid("gst_rate_bp must be between 0 and 2800")
	}
	today := billing.TodayIST()
	date := today
	if v.ReceivedAt != nil {
		date, _ = time.Parse("2006-01-02", *v.ReceivedAt)
		if date.After(today) {
			return nil, command.ErrInvalid("received_at cannot be in the future")
		}
	}
	method := "bank"
	if v.Method != nil && strings.TrimSpace(*v.Method) != "" {
		method = strings.TrimSpace(*v.Method)
	}
	name, gstin, state, err := billing.LoadContact(c, *v.ContactID)
	if err != nil {
		return nil, err
	}
	space := v.SpaceID
	if v.QuotationID != nil {
		var qstatus, qcontact, qspace string
		if _, err := access.AssertNoteAccess(c, *v.QuotationID, false); err != nil {
			return nil, err
		}
		err := c.Tx.QueryRow(c.Context,
			`select q.status, q.contact_id::text, n.space_id::text from billing.quotations q join core.notes n on n.id = q.note_id
			 where q.note_id = $1::uuid and n.deleted_at is null`, *v.QuotationID).Scan(&qstatus, &qcontact, &qspace)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, command.ErrNotFound("quotation")
		}
		if err != nil {
			return nil, command.DB(err)
		}
		if qstatus != "accepted" && qstatus != "converted" {
			return nil, command.ErrConflict(fmt.Sprintf("quotation is %s; take an advance against an accepted quotation", qstatus))
		}
		if qcontact != *v.ContactID {
			return nil, command.ErrInvalid("the quotation belongs to a different customer")
		}
		if space == "" {
			space = qspace
		}
	}
	if space == "" {
		if err := c.Tx.QueryRow(c.Context, "select space_id::text from core.notes where id = $1::uuid", *v.ContactID).Scan(&space); err != nil {
			return nil, command.DB(err)
		}
	}
	place := ""
	switch {
	case v.PlaceOfSupply != nil && *v.PlaceOfSupply != "":
		place = *v.PlaceOfSupply
		if err := billing.ValidateState(place); err != nil {
			return nil, err
		}
	case state != nil:
		place = *state
	case gstin != nil:
		place = billing.GSTINState(*gstin)
	}
	supplierGSTIN, supplierState, err := billing.WorkspaceState(c)
	if err != nil {
		return nil, err
	}
	sp := billing.AdvanceSplit{Taxable: amount} // goods: no GST on an advance (Notification 66/2017-CT)
	if *v.SupplyType == "services" {
		if sp, err = billing.AdvanceTax(amount, int(rate), supplierState, place); err != nil {
			return nil, err
		}
	} else {
		rate = 0
	}
	acc, err := ledgerAccounts(c)
	if err != nil {
		return nil, err
	}
	number, err := billing.NextNumber(c, "receipt", "RV", billing.FinancialYear(date))
	if err != nil {
		return nil, err
	}
	note, err := notes.InsertNote(c, space, "Receipt voucher "+number+" for "+name, nil, nil, []string{"receipt_voucher"})
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context,
		`insert into billing.receipts (note_id, workspace_id, kind, number, contact_id, quotation_id, supply_type, supplier_gstin,
		   supplier_state, buyer_gstin, place_of_supply, description, gst_rate_bp, amount_minor, taxable_minor, cgst_minor,
		   sgst_minor, igst_minor, method, reference, received_at)
		 values ($1::uuid,$2::uuid,'receipt',$3,$4::uuid,$5::uuid,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20::date)`,
		note.ID, c.WorkspaceID(), number, *v.ContactID, nilStr(v.QuotationID), *v.SupplyType, supplierGSTIN, supplierState,
		nilStr(gstin), nilStr(optStr(place)), nilStr(v.Description), rate, amount, sp.Taxable, sp.CGST, sp.SGST, sp.IGST,
		method, nilStr(v.Reference), date.Format("2006-01-02")); err != nil {
		return nil, command.DB(err)
	}
	legs := []billing.Leg{billing.L(billing.AcctBank, amount, 0), billing.L(acc.advances, 0, amount)}
	if tax := sp.CGST + sp.SGST + sp.IGST; tax > 0 {
		legs = append(legs, billing.L(acc.gstPaid, tax, 0), billing.L(billing.AcctCGST, 0, sp.CGST),
			billing.L(billing.AcctSGST, 0, sp.SGST), billing.L(billing.AcctIGST, 0, sp.IGST))
	}
	if err := billing.Post(c, "receipt", note.ID, "Receipt voucher "+number, legs); err != nil {
		return nil, err
	}
	if _, err := c.Emit("receipt.issued", &note.ID, map[string]any{"number": number, "amount_minor": amount,
		"contact_id": *v.ContactID, "quotation_id": v.QuotationID, "supply_type": *v.SupplyType}); err != nil {
		return nil, err
	}
	return detailOf(c, note.ID)
}

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ---------- apply ----------

func apply(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	if err := requireID(v); err != nil {
		return nil, err
	}
	if v.InvoiceID == nil || v.AmountMinor == nil {
		return nil, command.ErrInvalid("invoice_id and amount_minor are required")
	}
	amount := *v.AmountMinor
	if amount <= 0 {
		return nil, command.ErrInvalid("amount must be positive")
	}
	if _, err := access.AssertNoteAccess(c, v.ID, true); err != nil {
		return nil, err
	}
	r, err := load(c, v.ID, true) // the receipt first, then the invoice: one lock order for every caller
	if err != nil {
		return nil, err
	}
	if r.Kind != "receipt" {
		return nil, command.ErrConflict("a refund voucher cannot be applied")
	}
	if amount > r.UnappliedMinor {
		return nil, command.ErrInvalid(fmt.Sprintf("amount %d exceeds the unapplied advance %d", amount, r.UnappliedMinor))
	}
	acc, err := ledgerAccounts(c)
	if err != nil {
		return nil, err
	}
	inv, err := billing.SettleFromAdvance(c, *v.InvoiceID, r.ContactID, amount, r.Number)
	if err != nil {
		return nil, err
	}
	consumed := r.AppliedMinor + r.RefundedMinor
	cg, sg, ig, taxable := release(r, consumed, consumed+amount)
	var appID string
	if err := c.Tx.QueryRow(c.Context,
		`insert into billing.receipt_applications (workspace_id, receipt_id, invoice_id, amount_minor, taxable_minor, cgst_minor, sgst_minor, igst_minor)
		 values ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8) returning id::text`,
		c.WorkspaceID(), r.ID, *v.InvoiceID, amount, taxable, cg, sg, ig).Scan(&appID); err != nil {
		return nil, command.DB(err)
	}
	applied := r.AppliedMinor + amount
	if _, err := c.Tx.Exec(c.Context, "update billing.receipts set applied_minor = $2, status = $3 where note_id = $1::uuid",
		r.ID, applied, status(r.AmountMinor, applied, r.RefundedMinor)); err != nil {
		return nil, command.DB(err)
	}
	legs := append([]billing.Leg{billing.L(acc.advances, amount, 0), billing.L(billing.AcctReceivables, 0, amount)}, releaseLegs(acc, cg, sg, ig)...)
	if err := billing.Post(c, "receipt_apply", appID, "Advance "+r.Number+" applied to "+inv.Number, legs); err != nil {
		return nil, err
	}
	if _, err := c.Emit("receipt.applied", &r.ID, map[string]any{"number": r.Number, "invoice_id": inv.InvoiceID,
		"invoice_number": inv.Number, "amount_minor": amount}); err != nil {
		return nil, err
	}
	d, err := detailOf(c, r.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"receipt": d, "invoice": inv}, nil
}

// ---------- refund ----------

func refund(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	if err := requireID(v); err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, v.ID, true); err != nil {
		return nil, err
	}
	r, err := load(c, v.ID, true)
	if err != nil {
		return nil, err
	}
	if r.Kind != "receipt" {
		return nil, command.ErrConflict("a refund voucher cannot be refunded")
	}
	amount := r.UnappliedMinor
	if v.AmountMinor != nil {
		amount = *v.AmountMinor
	}
	if amount <= 0 || amount > r.UnappliedMinor {
		return nil, command.ErrInvalid(fmt.Sprintf("refund must be positive and at most the unapplied advance %d", r.UnappliedMinor))
	}
	method := r.Method
	if v.Method != nil && strings.TrimSpace(*v.Method) != "" {
		method = strings.TrimSpace(*v.Method)
	}
	acc, err := ledgerAccounts(c)
	if err != nil {
		return nil, err
	}
	consumed := r.AppliedMinor + r.RefundedMinor
	cg, sg, ig, taxable := release(r, consumed, consumed+amount)
	date := billing.TodayIST()
	number, err := billing.NextNumber(c, "refund", "RFV", billing.FinancialYear(date))
	if err != nil {
		return nil, err
	}
	var name string
	if err := c.Tx.QueryRow(c.Context, "select title from core.notes where id = $1::uuid", r.ContactID).Scan(&name); err != nil {
		return nil, command.DB(err)
	}
	note, err := notes.InsertNote(c, r.SpaceID, "Refund voucher "+number+" for "+name, nil, nil, []string{"receipt_voucher"})
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context,
		`insert into billing.receipts (note_id, workspace_id, kind, number, contact_id, quotation_id, original_receipt_id, supply_type,
		   supplier_gstin, supplier_state, buyer_gstin, place_of_supply, description, gst_rate_bp, amount_minor, taxable_minor,
		   cgst_minor, sgst_minor, igst_minor, method, reference, received_at)
		 values ($1::uuid,$2::uuid,'refund',$3,$4::uuid,$5::uuid,$6::uuid,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21::date)`,
		note.ID, c.WorkspaceID(), number, r.ContactID, nilStr(r.QuotationID), r.ID, r.SupplyType, r.SupplierGSTIN, r.SupplierState,
		nilStr(r.BuyerGSTIN), nilStr(r.PlaceOfSupply), nil, r.GSTRateBP, amount, taxable, cg, sg, ig, method,
		nilStr(v.Reference), date.Format("2006-01-02")); err != nil {
		return nil, command.DB(err)
	}
	refunded := r.RefundedMinor + amount
	if _, err := c.Tx.Exec(c.Context, "update billing.receipts set refunded_minor = $2, status = $3 where note_id = $1::uuid",
		r.ID, refunded, status(r.AmountMinor, r.AppliedMinor, refunded)); err != nil {
		return nil, command.DB(err)
	}
	legs := append([]billing.Leg{billing.L(acc.advances, amount, 0), billing.L(billing.AcctBank, 0, amount)}, releaseLegs(acc, cg, sg, ig)...)
	if err := billing.Post(c, "receipt_refund", note.ID, "Refund voucher "+number+" for "+r.Number, legs); err != nil {
		return nil, err
	}
	if _, err := c.Emit("receipt.refunded", &r.ID, map[string]any{"number": r.Number, "refund_number": number,
		"refund_id": note.ID, "amount_minor": amount}); err != nil {
		return nil, err
	}
	return detailOf(c, note.ID)
}

// ---------- read ----------

func get(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	if err := requireID(v); err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, v.ID, false); err != nil {
		return nil, err
	}
	return detailOf(c, v.ID)
}

// list: filters contact_id, status ('open' = issued or partly applied: the advances a customer can still use) and kind.
func list(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	lim := int64(100)
	if v.Limit != nil {
		lim = min(max(*v.Limit, 1), 500)
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		"select "+cols+` from billing.receipts r join core.notes n on n.id = r.note_id
		 where n.deleted_at is null and n.space_id = any($1::text[]::uuid[])
		   and core.note_access(n.id, $5::uuid, $6, $1::text[]::uuid[]) <> 'none'
		   and ($2::text is null or r.status = $2 or ($2 = 'open' and r.status in ('issued','partly_applied') and r.kind = 'receipt'))
		   and ($3::uuid is null or r.contact_id = $3::uuid) and ($7::text is null or r.kind = $7)
		 order by r.created_at desc, r.note_id limit $4`,
		spaces, nilStr(v.Status), nilStr(v.ContactID), lim, uid, admin, nilStr(v.Kind))
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []row{}
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, command.DB(err)
		}
		out = append(out, r)
	}
	return out, command.DB(rows.Err())
}

// Create is receipt.create for another module (bank reconciliation) that already holds its own gate and confirmation.
func Create(c *command.Ctx, input json.RawMessage) (any, error) { return create(c, input) }
