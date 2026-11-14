// Package purchases: vendors and purchase bills (accounts payable). Go-only: Rust has no bill.* commands.
// A bill is a typed note plus billing.purchase_bills rows. Recording posts Dr expense + Dr input GST (ITC) / Cr accounts
// payable; paying posts Dr payable / Cr bank; cancelling a recorded, unpaid bill posts the exact reverse.
//
// Flow: draft -> recorded -> partly_paid -> paid; draft | recorded (unpaid) -> cancelled.
package purchases

import (
	"encoding/json"
	"errors"
	"fmt"
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
	acctPayable  = "2100"
	acctITCCGST  = "1301"
	acctITCSGST  = "1302"
	acctITCIGST  = "1303"
	acctExpenses = "5000"
	linesTable   = "billing.purchase_bill_lines"
	linesCol     = "bill_id"
)

// chart is seeded per workspace on the first recorded bill (Rust seeds the original seven at workspace creation).
var chart = [][3]string{
	{acctITCCGST, "CGST input tax credit", "asset"},
	{acctITCSGST, "SGST input tax credit", "asset"},
	{acctITCIGST, "IGST input tax credit", "asset"},
	{acctPayable, "Accounts payable", "liability"},
	{acctExpenses, "Purchases and expenses", "expense"},
}

func Commands() []command.Command {
	mi, rd := command.GroupManageInvoices, command.GroupRead
	confirm := func(c command.Command) command.Command { c.Confirm = true; return c }
	member := func(c command.Command) command.Command { c.MinRole = command.Member; return c }
	nc := command.NewCommand
	return []command.Command{
		nc("bill.create_draft", mi, createDraft),
		nc("bill.update_draft", mi, updateDraft),
		nc("bill.get", rd, get),
		nc("bill.list", rd, list),
		nc("bill.record", mi, record),
		confirm(nc("bill.pay", mi, pay)),
		confirm(nc("bill.cancel", mi, cancel)),
		nc("vendor.list", rd, vendorList),
		member(nc("report.payables_ageing", rd, payablesAgeing)),
		member(nc("report.itc_summary", rd, itcSummary)),
	}
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type in struct {
	ID               string          `json:"id"`
	SpaceID          string          `json:"space_id"`
	VendorID         *string         `json:"vendor_id"`
	VendorBillNumber *string         `json:"vendor_bill_number"`
	Status           *string         `json:"status"`
	Limit            *int64          `json:"limit"`
	BillDate         *string         `json:"bill_date"`
	DueDate          *string         `json:"due_date"`
	PlaceOfSupply    *string         `json:"place_of_supply"`
	Notes            *string         `json:"notes"`
	Lines            json.RawMessage `json:"lines"`
	AmountMinor      *int64          `json:"amount_minor"`
	Method           *string         `json:"method"`
	Reference        *string         `json:"reference"`
	AsOf             *string         `json:"as_of"`
	From             *string         `json:"from"`
	To               *string         `json:"to"`
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
	if v.VendorID != nil && !uuidRe.MatchString(*v.VendorID) {
		return v, command.ErrInvalid("invalid vendor_id")
	}
	for _, d := range []*string{v.BillDate, v.DueDate, v.AsOf, v.From, v.To} {
		if d != nil {
			if _, err := time.Parse("2006-01-02", *d); err != nil {
				return v, command.ErrInvalid("dates must be YYYY-MM-DD")
			}
		}
	}
	if v.VendorBillNumber != nil {
		t := strings.TrimSpace(*v.VendorBillNumber)
		v.VendorBillNumber = &t
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

// wrap: lines serde would reject have no Rust to answer them here, so they are plain invalid input.
func wrap(err error) error {
	if errors.Is(err, command.ErrFallback) {
		return command.ErrInvalid("invalid lines")
	}
	return err
}

// ---------- rows ----------

type row struct {
	ID               string      `json:"id"`
	VendorID         string      `json:"vendor_id"`
	VendorBillNumber string      `json:"vendor_bill_number"`
	BillDate         string      `json:"bill_date"`
	DueDate          string      `json:"due_date"`
	SupplierGSTIN    *string     `json:"supplier_gstin"`
	SupplierState    *string     `json:"supplier_state"`
	PlaceOfSupply    *string     `json:"place_of_supply"`
	Status           string      `json:"status"`
	Currency         string      `json:"currency"`
	SubtotalMinor    int64       `json:"subtotal_minor"`
	CGSTMinor        int64       `json:"cgst_minor"`
	SGSTMinor        int64       `json:"sgst_minor"`
	IGSTMinor        int64       `json:"igst_minor"`
	TotalMinor       int64       `json:"total_minor"`
	PaidMinor        int64       `json:"paid_minor"`
	OutstandingMinor int64       `json:"outstanding_minor"`
	RecordedAt       *command.Ts `json:"recorded_at"`
	SpaceID          string      `json:"space_id"`
}

const cols = `b.note_id::text, b.vendor_id::text, b.vendor_bill_number, b.bill_date::text, b.due_date::text, b.supplier_gstin,
	b.supplier_state, b.place_of_supply, b.status, b.currency::text, b.subtotal_minor, b.cgst_minor, b.sgst_minor, b.igst_minor,
	b.total_minor, b.paid_minor, case when b.status in ('recorded','partly_paid') then b.total_minor - b.paid_minor else 0 end,
	b.recorded_at, n.space_id::text`

func scan(r pgx.Row) (row, error) {
	var x row
	var rec *time.Time
	err := r.Scan(&x.ID, &x.VendorID, &x.VendorBillNumber, &x.BillDate, &x.DueDate, &x.SupplierGSTIN, &x.SupplierState,
		&x.PlaceOfSupply, &x.Status, &x.Currency, &x.SubtotalMinor, &x.CGSTMinor, &x.SGSTMinor, &x.IGSTMinor, &x.TotalMinor,
		&x.PaidMinor, &x.OutstandingMinor, &rec, &x.SpaceID)
	x.RecordedAt = command.TsPtr(rec)
	return x, err
}

func load(c *command.Ctx, id string, lock bool) (row, error) {
	q := "select " + cols + ` from billing.purchase_bills b join core.notes n on n.id = b.note_id
		where b.note_id = $1::uuid and n.deleted_at is null `
	if lock {
		q += "for update of b"
	}
	r, err := scan(c.Tx.QueryRow(c.Context, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, command.ErrNotFound("purchase bill")
	}
	return r, command.DB(err)
}

type lineOut struct {
	ID           string  `json:"id"`
	Position     int32   `json:"position"`
	Description  string  `json:"description"`
	HsnSac       *string `json:"hsn_sac"`
	Quantity     string  `json:"quantity"`
	UnitPrice    int64   `json:"unit_price_minor"`
	GSTRateBP    int32   `json:"gst_rate_bp"`
	TaxableMinor int64   `json:"taxable_minor"`
	CGSTMinor    int64   `json:"cgst_minor"`
	SGSTMinor    int64   `json:"sgst_minor"`
	IGSTMinor    int64   `json:"igst_minor"`
	AccountCode  *string `json:"account_code"`
}

type paymentOut struct {
	ID          string     `json:"id"`
	AmountMinor int64      `json:"amount_minor"`
	Method      string     `json:"method"`
	Reference   *string    `json:"reference"`
	PaidAt      command.Ts `json:"paid_at"`
}

type detail struct {
	row
	VendorName *string      `json:"vendor_name"`
	Lines      []lineOut    `json:"lines"`
	Payments   []paymentOut `json:"payments"`
}

func detailOf(c *command.Ctx, id string) (detail, error) {
	b, err := load(c, id, false)
	if err != nil {
		return detail{}, err
	}
	d := detail{row: b, Lines: []lineOut{}, Payments: []paymentOut{}}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return d, err
	}
	uid, admin := access.Viewer(c)
	// The vendor's name only when the caller may read the contact page.
	err = c.Tx.QueryRow(c.Context,
		"select title from core.notes where id = $1::uuid and core.note_access(id, $2::uuid, $3, $4::text[]::uuid[]) <> 'none'",
		b.VendorID, uid, admin, spaces).Scan(&d.VendorName)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return d, command.DB(err)
	}
	rows, err := c.Tx.Query(c.Context,
		`select id::text, position, description, hsn_sac, quantity::text, unit_price_minor, gst_rate_bp, taxable_minor,
		   cgst_minor, sgst_minor, igst_minor, account_code
		 from billing.purchase_bill_lines where bill_id = $1::uuid order by position`, id)
	if err != nil {
		return d, command.DB(err)
	}
	for rows.Next() {
		var l lineOut
		if err := rows.Scan(&l.ID, &l.Position, &l.Description, &l.HsnSac, &l.Quantity, &l.UnitPrice, &l.GSTRateBP,
			&l.TaxableMinor, &l.CGSTMinor, &l.SGSTMinor, &l.IGSTMinor, &l.AccountCode); err != nil {
			rows.Close()
			return d, command.DB(err)
		}
		d.Lines = append(d.Lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return d, command.DB(err)
	}
	prows, err := c.Tx.Query(c.Context,
		"select id::text, amount_minor, method, reference, paid_at from billing.purchase_payments where bill_id = $1::uuid order by paid_at, id", id)
	if err != nil {
		return d, command.DB(err)
	}
	defer prows.Close()
	for prows.Next() {
		var p paymentOut
		var at time.Time
		if err := prows.Scan(&p.ID, &p.AmountMinor, &p.Method, &p.Reference, &at); err != nil {
			return d, command.DB(err)
		}
		p.PaidAt = *command.TsPtr(&at)
		d.Payments = append(d.Payments, p)
	}
	return d, command.DB(prows.Err())
}

// ---------- vendors and lines ----------

type vendor struct{ name, gstin, state string }

// loadVendor checks the caller can read the contact and that it is flagged as a vendor.
func loadVendor(c *command.Ctx, id string) (vendor, error) {
	name, gstin, state, err := billing.LoadContact(c, id)
	if err != nil {
		return vendor{}, err
	}
	var is bool
	if err := c.Tx.QueryRow(c.Context, "select is_vendor from crm.contacts where note_id = $1::uuid", id).Scan(&is); err != nil {
		return vendor{}, command.DB(err)
	}
	if !is {
		return vendor{}, command.ErrInvalid("contact is not a vendor: set is_vendor on the contact first")
	}
	v := vendor{name: name}
	if gstin != nil {
		v.gstin = *gstin
	}
	if state != nil {
		v.state = *state
	}
	if v.state == "" && v.gstin != "" {
		v.state = billing.GSTINState(v.gstin)
	}
	return v, nil
}

// lineCodes reads the optional per-line expense account from the raw input lines (same order as the lines).
func lineCodes(raw json.RawMessage) []*string {
	var ls []struct {
		AccountCode *string `json:"account_code"`
	}
	_ = json.Unmarshal(raw, &ls)
	out := make([]*string, len(ls))
	for i, l := range ls {
		out[i] = l.AccountCode
	}
	return out
}

func setCodes(c *command.Ctx, id string, codes []*string) error {
	for i, code := range codes {
		if code == nil {
			continue
		}
		if _, err := c.Tx.Exec(c.Context, "update billing.purchase_bill_lines set account_code = $3 where bill_id = $1::uuid and position = $2",
			id, int32(i+1), *code); err != nil {
			return command.DB(err)
		}
	}
	return nil
}

func storedCodes(c *command.Ctx, id string) ([]*string, error) {
	rows, err := c.Tx.Query(c.Context, "select account_code from billing.purchase_bill_lines where bill_id = $1::uuid order by position", id)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	var out []*string
	for rows.Next() {
		var s *string
		if err := rows.Scan(&s); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, s)
	}
	return out, command.DB(rows.Err())
}

// checkCodes: every line account must be an expense account of this workspace (5000 is always allowed: it is seeded on record).
func checkCodes(c *command.Ctx, codes []*string) error {
	for _, code := range codes {
		if code == nil || *code == acctExpenses {
			continue
		}
		var ok bool
		if err := c.Tx.QueryRow(c.Context, "select exists(select 1 from billing.accounts where code = $1 and kind = 'expense')", *code).Scan(&ok); err != nil {
			return command.DB(err)
		}
		if !ok {
			return command.ErrInvalid(fmt.Sprintf("account %s is not an expense account", *code))
		}
	}
	return nil
}

func ensureChart(c *command.Ctx) error {
	for _, a := range chart {
		if _, err := c.Tx.Exec(c.Context,
			"insert into billing.accounts (workspace_id, code, name, kind) values ($1::uuid,$2,$3,$4) on conflict do nothing",
			c.WorkspaceID(), a[0], a[1], a[2]); err != nil {
			return command.DB(err)
		}
	}
	return nil
}

func checkDuplicate(c *command.Ctx, vendorID, number, self string) error {
	var dup bool
	if err := c.Tx.QueryRow(c.Context,
		`select exists(select 1 from billing.purchase_bills where vendor_id = $1::uuid and vendor_bill_number = $2
		   and status <> 'cancelled' and note_id <> coalesce(nullif($3, '')::uuid, '00000000-0000-0000-0000-000000000000'))`,
		vendorID, number, self).Scan(&dup); err != nil {
		return command.DB(err)
	}
	if dup {
		return command.ErrConflict(fmt.Sprintf("vendor bill number %q already exists for this vendor", number))
	}
	return nil
}

func setSubtotal(c *command.Ctx, id string, subtotal int64) error {
	_, err := c.Tx.Exec(c.Context, "update billing.purchase_bills set subtotal_minor = $2, total_minor = $2 where note_id = $1::uuid", id, subtotal)
	return command.DB(err)
}

// ---------- drafts ----------

func createDraft(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	if v.SpaceID == "" || v.VendorID == nil || v.VendorBillNumber == nil || *v.VendorBillNumber == "" {
		return nil, command.ErrInvalid("space_id, vendor_id and vendor_bill_number are required")
	}
	lines, _, err := billing.DecodeLines(input, "lines", true)
	if err != nil {
		return nil, wrap(err)
	}
	codes := lineCodes(v.Lines)
	if err := checkCodes(c, codes); err != nil {
		return nil, err
	}
	ven, err := loadVendor(c, *v.VendorID)
	if err != nil {
		return nil, err
	}
	if v.PlaceOfSupply != nil {
		if err := billing.ValidateState(*v.PlaceOfSupply); err != nil {
			return nil, err
		}
	}
	if err := checkDuplicate(c, *v.VendorID, *v.VendorBillNumber, ""); err != nil {
		return nil, err
	}
	date := billing.TodayIST().Format("2006-01-02")
	if v.BillDate != nil {
		date = *v.BillDate
	}
	note, err := notes.InsertNote(c, v.SpaceID, "Bill "+*v.VendorBillNumber+" from "+ven.name, nil, v.Notes, []string{"purchase_bill"})
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context,
		`insert into billing.purchase_bills (note_id, workspace_id, vendor_id, vendor_bill_number, bill_date, due_date,
		   supplier_gstin, supplier_state, place_of_supply)
		 values ($1::uuid,$2::uuid,$3::uuid,$4,$5::date,coalesce($6::date, $5::date + 30),$7,$8,$9)`,
		note.ID, c.WorkspaceID(), *v.VendorID, *v.VendorBillNumber, date, nilStr(v.DueDate), nilIf(ven.gstin), nilIf(ven.state),
		nilStr(v.PlaceOfSupply)); err != nil {
		return nil, command.DB(err)
	}
	subtotal, err := billing.WriteLines(c, linesTable, linesCol, note.ID, lines, nil)
	if err != nil {
		return nil, err
	}
	if err := setCodes(c, note.ID, codes); err != nil {
		return nil, err
	}
	if err := setSubtotal(c, note.ID, subtotal); err != nil {
		return nil, err
	}
	if _, err := c.Emit("bill.created", &note.ID, map[string]any{"vendor_id": *v.VendorID, "vendor_bill_number": *v.VendorBillNumber, "subtotal_minor": subtotal}); err != nil {
		return nil, err
	}
	return detailOf(c, note.ID)
}

func nilIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func updateDraft(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	if err := requireID(v); err != nil {
		return nil, err
	}
	lines, haveLines, err := billing.DecodeLines(input, "lines", false)
	if err != nil {
		return nil, wrap(err)
	}
	if _, err := access.AssertNoteAccess(c, v.ID, true); err != nil {
		return nil, err
	}
	b, err := load(c, v.ID, true)
	if err != nil {
		return nil, err
	}
	if b.Status != "draft" {
		return nil, command.ErrConflict("only drafts can be edited")
	}
	if v.PlaceOfSupply != nil {
		if err := billing.ValidateState(*v.PlaceOfSupply); err != nil {
			return nil, err
		}
	}
	vendorID, number := b.VendorID, b.VendorBillNumber
	var ven *vendor
	if v.VendorID != nil {
		vendorID = *v.VendorID
		x, err := loadVendor(c, vendorID)
		if err != nil {
			return nil, err
		}
		ven = &x
	}
	if v.VendorBillNumber != nil {
		if *v.VendorBillNumber == "" {
			return nil, command.ErrInvalid("vendor_bill_number cannot be empty")
		}
		number = *v.VendorBillNumber
	}
	if err := checkDuplicate(c, vendorID, number, b.ID); err != nil {
		return nil, err
	}
	var codes []*string
	if haveLines {
		codes = lineCodes(v.Lines)
		if err := checkCodes(c, codes); err != nil {
			return nil, err
		}
	}
	var gstin, state any
	if ven != nil {
		gstin, state = nilIf(ven.gstin), nilIf(ven.state)
	}
	if _, err := c.Tx.Exec(c.Context,
		`update billing.purchase_bills set vendor_id = $2::uuid, vendor_bill_number = $3,
		   supplier_gstin = case when $4::boolean then $5::text else supplier_gstin end,
		   supplier_state = case when $4::boolean then $6::text else supplier_state end,
		   place_of_supply = coalesce($7::text, place_of_supply), bill_date = coalesce($8::date, bill_date),
		   due_date = coalesce($9::date, due_date) where note_id = $1::uuid`,
		b.ID, vendorID, number, ven != nil, gstin, state, nilStr(v.PlaceOfSupply), nilStr(v.BillDate), nilStr(v.DueDate)); err != nil {
		return nil, command.DB(err)
	}
	if haveLines {
		subtotal, err := billing.WriteLines(c, linesTable, linesCol, b.ID, lines, nil)
		if err != nil {
			return nil, err
		}
		if err := setCodes(c, b.ID, codes); err != nil {
			return nil, err
		}
		if err := setSubtotal(c, b.ID, subtotal); err != nil {
			return nil, err
		}
	}
	return detailOf(c, b.ID)
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
		"select "+cols+` from billing.purchase_bills b join core.notes n on n.id = b.note_id
		 where n.deleted_at is null and n.space_id = any($1::text[]::uuid[]) and ($2::text is null or b.status = $2)
		   and core.note_access(n.id, $5::uuid, $6, $1::text[]::uuid[]) <> 'none'
		   and ($3::uuid is null or b.vendor_id = $3::uuid)
		 order by b.bill_date desc, n.created_at desc limit $4`,
		spaces, nilStr(v.Status), nilStr(v.VendorID), lim, uid, admin)
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

// ---------- transitions ----------

func lockFor(c *command.Ctx, input json.RawMessage, verb string, from ...string) (row, in, error) {
	v, err := decode(input)
	if err != nil {
		return row{}, v, err
	}
	if err := requireID(v); err != nil {
		return row{}, v, err
	}
	if _, err := access.AssertNoteAccess(c, v.ID, true); err != nil {
		return row{}, v, err
	}
	b, err := load(c, v.ID, true)
	if err != nil {
		return b, v, err
	}
	for _, s := range from {
		if b.Status == s {
			return b, v, nil
		}
	}
	return b, v, command.ErrConflict(fmt.Sprintf("bill is %s and cannot be %s", b.Status, verb))
}

func record(c *command.Ctx, input json.RawMessage) (any, error) {
	b, _, err := lockFor(c, input, "recorded", "draft")
	if err != nil {
		return nil, err
	}
	ven, err := loadVendor(c, b.VendorID)
	if err != nil {
		return nil, err
	}
	if err := checkDuplicate(c, b.VendorID, b.VendorBillNumber, b.ID); err != nil {
		return nil, err
	}
	_, wsState, err := billing.WorkspaceState(c)
	if err != nil {
		return nil, err
	}
	place := wsState
	if b.PlaceOfSupply != nil {
		place = *b.PlaceOfSupply
	}
	if ven.state == "" {
		return nil, command.ErrInvalid("vendor state is unknown: give the vendor a GSTIN or state code")
	}
	lines, err := billing.LoadLines(c, linesTable, linesCol, b.ID)
	if err != nil {
		return nil, err
	}
	tax, err := billing.ComputePurchaseTax(lines, ven.state, place)
	if err != nil {
		return nil, err
	}
	codes, err := storedCodes(c, b.ID)
	if err != nil {
		return nil, err
	}
	if err := ensureChart(c); err != nil {
		return nil, err
	}
	if err := checkCodes(c, codes); err != nil {
		return nil, err
	}
	if _, err := billing.WriteLines(c, linesTable, linesCol, b.ID, lines, &tax); err != nil {
		return nil, err
	}
	if err := setCodes(c, b.ID, codes); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context,
		"select coalesce(account_code, $2), sum(taxable_minor)::bigint from billing.purchase_bill_lines where bill_id = $1::uuid group by 1 order by 1", b.ID, acctExpenses)
	if err != nil {
		return nil, command.DB(err)
	}
	legs := []billing.Leg{}
	for rows.Next() {
		var code string
		var amt int64
		if err := rows.Scan(&code, &amt); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		legs = append(legs, billing.L(code, amt, 0))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	legs = append(legs, billing.L(acctITCCGST, tax.CGST, 0), billing.L(acctITCSGST, tax.SGST, 0),
		billing.L(acctITCIGST, tax.IGST, 0), billing.L(acctPayable, 0, tax.Total))
	if err := billing.Post(c, "purchase_bill", b.ID, "Bill "+b.VendorBillNumber+" from "+ven.name, legs); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context,
		`update billing.purchase_bills set status = 'recorded', recorded_at = now(), supplier_gstin = $2, supplier_state = $3,
		   place_of_supply = $4, subtotal_minor = $5, cgst_minor = $6, sgst_minor = $7, igst_minor = $8, total_minor = $9
		 where note_id = $1::uuid`,
		b.ID, nilIf(ven.gstin), ven.state, place, tax.Subtotal, tax.CGST, tax.SGST, tax.IGST, tax.Total); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("bill.recorded", &b.ID, map[string]any{"vendor_id": b.VendorID, "vendor_bill_number": b.VendorBillNumber,
		"total_minor": tax.Total, "itc_minor": tax.CGST + tax.SGST + tax.IGST}); err != nil {
		return nil, err
	}
	return detailOf(c, b.ID)
}

func pay(c *command.Ctx, input json.RawMessage) (any, error) {
	b, v, err := lockFor(c, input, "paid", "recorded", "partly_paid")
	if err != nil {
		return nil, err
	}
	if v.AmountMinor == nil || *v.AmountMinor <= 0 {
		return nil, command.ErrInvalid("amount_minor must be positive")
	}
	amount := *v.AmountMinor
	if amount > b.OutstandingMinor {
		return nil, command.ErrInvalid(fmt.Sprintf("payment %d exceeds the outstanding amount %d", amount, b.OutstandingMinor))
	}
	method := "bank"
	if v.Method != nil && strings.TrimSpace(*v.Method) != "" {
		method = strings.TrimSpace(*v.Method)
	}
	var pid string
	if err := c.Tx.QueryRow(c.Context,
		`insert into billing.purchase_payments (workspace_id, bill_id, amount_minor, method, reference)
		 values ($1::uuid,$2::uuid,$3,$4,$5) returning id::text`,
		c.WorkspaceID(), b.ID, amount, method, nilStr(v.Reference)).Scan(&pid); err != nil {
		return nil, command.DB(err)
	}
	paid := b.PaidMinor + amount
	status := "partly_paid"
	if paid == b.TotalMinor {
		status = "paid"
	}
	if _, err := c.Tx.Exec(c.Context, "update billing.purchase_bills set paid_minor = $2, status = $3 where note_id = $1::uuid", b.ID, paid, status); err != nil {
		return nil, command.DB(err)
	}
	if err := billing.Post(c, "purchase_payment", pid, "Payment for bill "+b.VendorBillNumber,
		[]billing.Leg{billing.L(acctPayable, amount, 0), billing.L(billing.AcctBank, 0, amount)}); err != nil {
		return nil, err
	}
	if _, err := c.Emit("bill.payment_recorded", &b.ID, map[string]any{"payment_id": pid, "amount_minor": amount, "status": status}); err != nil {
		return nil, err
	}
	if status == "paid" {
		if _, err := c.Emit("bill.paid", &b.ID, map[string]any{"vendor_bill_number": b.VendorBillNumber, "total_minor": b.TotalMinor}); err != nil {
			return nil, err
		}
	}
	return detailOf(c, b.ID)
}

func cancel(c *command.Ctx, input json.RawMessage) (any, error) {
	b, _, err := lockFor(c, input, "cancelled", "draft", "recorded")
	if err != nil {
		return nil, err
	}
	if b.PaidMinor > 0 {
		return nil, command.ErrConflict("bill has payments and cannot be cancelled")
	}
	if b.Status == "recorded" {
		rows, err := c.Tx.Query(c.Context,
			`select l.account_code, l.debit_minor, l.credit_minor from billing.journal_lines l
			 join billing.journal_entries e on e.id = l.entry_id
			 where e.source_type = 'purchase_bill' and e.source_id = $1::uuid order by l.id`, b.ID)
		if err != nil {
			return nil, command.DB(err)
		}
		legs := []billing.Leg{}
		for rows.Next() {
			var code string
			var d, cr int64
			if err := rows.Scan(&code, &d, &cr); err != nil {
				rows.Close()
				return nil, command.DB(err)
			}
			legs = append(legs, billing.L(code, cr, d))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, command.DB(err)
		}
		if err := billing.Post(c, "purchase_bill_cancel", b.ID, "Cancelled bill "+b.VendorBillNumber, legs); err != nil {
			return nil, err
		}
	}
	if _, err := c.Tx.Exec(c.Context, "update billing.purchase_bills set status = 'cancelled', cancelled_at = now() where note_id = $1::uuid", b.ID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("bill.cancelled", &b.ID, map[string]any{"vendor_bill_number": b.VendorBillNumber}); err != nil {
		return nil, err
	}
	return detailOf(c, b.ID)
}

// ---------- vendors and reports ----------

func vendorList(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	lim := int64(200)
	if v.Limit != nil {
		lim = min(max(*v.Limit, 1), 500)
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		`select n.id::text, n.space_id::text, n.title, c.email, c.phone, c.company, c.gstin, c.state_code,
		   coalesce((select sum(b.total_minor - b.paid_minor) from billing.purchase_bills b
		             where b.vendor_id = n.id and b.status in ('recorded','partly_paid')), 0)::bigint
		 from crm.contacts c join core.notes n on n.id = c.note_id
		 where c.is_vendor and n.deleted_at is null and n.space_id = any($1::text[]::uuid[])
		   and core.note_access(n.id, $3::uuid, $4, $1::text[]::uuid[]) <> 'none'
		 order by n.title limit $2`, spaces, lim, uid, admin)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, space, name string
		var email, phone, company, gstin, state *string
		var due int64
		if err := rows.Scan(&id, &space, &name, &email, &phone, &company, &gstin, &state, &due); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, map[string]any{"id": id, "space_id": space, "name": name, "email": email, "phone": phone,
			"company": company, "gstin": gstin, "state_code": state, "outstanding_minor": due})
	}
	return out, command.DB(rows.Err())
}

func payablesAgeing(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	asOf := billing.TodayIST().Format("2006-01-02")
	if v.AsOf != nil {
		asOf = *v.AsOf
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		`select b.note_id::text, b.vendor_bill_number, b.vendor_id::text, vn.title, b.bill_date::text, b.due_date::text,
		   b.total_minor - b.paid_minor
		 from billing.purchase_bills b join core.notes n on n.id = b.note_id left join core.notes vn on vn.id = b.vendor_id
		 where b.status in ('recorded','partly_paid') and b.total_minor - b.paid_minor > 0 and b.bill_date <= $1::date
		   and n.space_id = any($2::text[]::uuid[]) and core.note_access(n.id, $3::uuid, $4, $2::text[]::uuid[]) <> 'none'
		 order by b.bill_date, b.note_id`, asOf, spaces, uid, admin)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	type tot struct{ amt, n int64 }
	totals := map[string]*tot{}
	for _, b := range billing.Buckets {
		totals[b] = &tot{}
	}
	bills := []map[string]any{}
	var total int64
	for rows.Next() {
		var id, number, vendorID, date, due string
		var vendorName *string
		var out int64
		if err := rows.Scan(&id, &number, &vendorID, &vendorName, &date, &due, &out); err != nil {
			return nil, command.DB(err)
		}
		days := billing.DaysBetween(asOf, date)
		bk := billing.AgeBucket(days)
		totals[bk].amt += out
		totals[bk].n++
		total += out
		bills = append(bills, map[string]any{"id": id, "vendor_bill_number": number, "vendor_id": vendorID, "vendor": vendorName,
			"bill_date": date, "due_date": due, "age_days": days, "bucket": bk, "outstanding_minor": out})
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	bs := []map[string]any{}
	for _, b := range billing.Buckets {
		bs = append(bs, map[string]any{"bucket": b, "outstanding_minor": totals[b].amt, "count": totals[b].n})
	}
	return map[string]any{"as_of": asOf, "buckets": bs, "total_minor": total, "bills": bills}, nil
}

// itcSummary is input tax credit by month and rate from recorded (not cancelled) bills.
func itcSummary(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	if v.From == nil || v.To == nil {
		return nil, command.ErrInvalid("from and to are required")
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		`select to_char(b.bill_date, 'YYYY-MM'), l.gst_rate_bp, sum(l.taxable_minor)::bigint, sum(l.cgst_minor)::bigint,
		   sum(l.sgst_minor)::bigint, sum(l.igst_minor)::bigint
		 from billing.purchase_bill_lines l join billing.purchase_bills b on b.note_id = l.bill_id join core.notes n on n.id = b.note_id
		 where b.status in ('recorded','partly_paid','paid') and b.bill_date between $1::date and $2::date
		   and n.space_id = any($3::text[]::uuid[]) and core.note_access(n.id, $4::uuid, $5, $3::text[]::uuid[]) <> 'none'
		 group by 1, 2 order by 1, 2`, *v.From, *v.To, spaces, uid, admin)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	var tt, tc, ts, ti int64
	for rows.Next() {
		var period string
		var rate int32
		var t, cg, sg, ig int64
		if err := rows.Scan(&period, &rate, &t, &cg, &sg, &ig); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, map[string]any{"period": period, "gst_rate_bp": rate, "taxable_minor": t, "cgst_minor": cg, "sgst_minor": sg, "igst_minor": ig})
		tt, tc, ts, ti = tt+t, tc+cg, ts+sg, ti+ig
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"from": *v.From, "to": *v.To, "rows": out,
		"totals": map[string]any{"taxable_minor": tt, "cgst_minor": tc, "sgst_minor": ts, "igst_minor": ti, "itc_minor": tc + ts + ti}}, nil
}
