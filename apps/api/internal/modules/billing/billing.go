package billing

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

func Commands() []command.Command {
	mi, rd := command.GroupManageInvoices, command.GroupRead
	confirm := func(c command.Command) command.Command { c.Confirm = true; return c }
	role := func(c command.Command, r command.Role) command.Command { c.MinRole = r; return c }
	nc := command.NewCommand
	return append([]command.Command{
		nc("invoice.create_draft", mi, invoiceCreateDraft),
		nc("invoice.update_draft", mi, invoiceUpdateDraft),
		nc("invoice.get", rd, invoiceGet),
		nc("invoice.list", rd, invoiceList),
		nc("invoice.preview", rd, invoicePreview),
		nc("invoice.upi_qr", rd, invoiceUPIQR),
		confirm(nc("invoice.issue", mi, invoiceIssue)),
		nc("invoice.generate_irn", mi, invoiceGenerateIRN),
		confirm(nc("invoice.cancel_irn", mi, invoiceCancelIRN)),
		confirm(nc("invoice.cancel", mi, invoiceCancel)),
		confirm(nc("credit_note.create", mi, creditNoteCreate)),
		confirm(nc("payment.record", mi, paymentRecord)),
		role(confirm(nc("invoice.write_off", mi, invoiceWriteOff)), command.Admin),
	}, reportCommands()...)
}

// ---------- dates ----------

func parseDate(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

func fmtDate(t time.Time) string { return t.Format("2006-01-02") }

// todayIST is today in India (UTC+5:30).
func todayIST() time.Time {
	t := time.Now().UTC().Add(330 * time.Minute)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func daysBetween(a, b time.Time) int64 { return int64(a.Sub(b) / (24 * time.Hour)) }

func gstErrToCmd(err error) error {
	var g *gstErr
	if errors.As(err, &g) {
		return command.ErrInvalid(g.msg)
	}
	return err
}

// ---------- rows ----------

type invoiceRow struct {
	ID                string      `json:"id"`
	Kind              string      `json:"kind"`
	Number            *string     `json:"number"`
	Status            string      `json:"status"`
	ContactID         *string     `json:"contact_id"`
	OriginalInvoiceID *string     `json:"original_invoice_id"`
	Currency          string      `json:"currency"`
	SupplierGSTIN     *string     `json:"supplier_gstin"`
	SupplierState     *string     `json:"supplier_state"`
	BuyerGSTIN        *string     `json:"buyer_gstin"`
	PlaceOfSupply     *string     `json:"place_of_supply"`
	ReverseCharge     bool        `json:"reverse_charge"`
	InvoiceDate       *string     `json:"invoice_date"`
	DueDate           *string     `json:"due_date"`
	SubtotalMinor     int64       `json:"subtotal_minor"`
	CGSTMinor         int64       `json:"cgst_minor"`
	SGSTMinor         int64       `json:"sgst_minor"`
	IGSTMinor         int64       `json:"igst_minor"`
	TotalMinor        int64       `json:"total_minor"`
	PaidMinor         int64       `json:"paid_minor"`
	CreditedMinor     int64       `json:"credited_minor"`
	WrittenOffMinor   int64       `json:"written_off_minor"`
	IRN               *string     `json:"irn"`
	AckNo             *string     `json:"ack_no"`
	AckDate           *command.Ts `json:"ack_date"`
	SignedQR          *string     `json:"signed_qr"`
	IRNCancelledAt    *command.Ts `json:"irn_cancelled_at"`
	IssuedAt          *command.Ts `json:"issued_at"`
	CancelledAt       *command.Ts `json:"cancelled_at"`
	BranchID          *string     `json:"branch_id"`
	ackTime           *time.Time  // not serialized
}

func (i invoiceRow) outstanding() int64 {
	return i.TotalMinor - i.PaidMinor - i.CreditedMinor - i.WrittenOffMinor
}

const invCols = `i.note_id::text, i.kind, i.number, i.status, i.contact_id::text, i.original_invoice_id::text, i.currency::text,
	i.supplier_gstin, i.supplier_state, i.buyer_gstin, i.place_of_supply, i.reverse_charge, i.invoice_date::text, i.due_date::text,
	i.subtotal_minor, i.cgst_minor, i.sgst_minor, i.igst_minor, i.total_minor, i.paid_minor, i.credited_minor,
	i.written_off_minor, i.irn, i.ack_no, i.ack_date, i.signed_qr, i.irn_cancelled_at, i.issued_at, i.cancelled_at, i.branch_id::text`

func scanInvoice(row pgx.Row) (invoiceRow, error) {
	var r invoiceRow
	var ack, irnC, issued, canc *time.Time
	err := row.Scan(&r.ID, &r.Kind, &r.Number, &r.Status, &r.ContactID, &r.OriginalInvoiceID, &r.Currency,
		&r.SupplierGSTIN, &r.SupplierState, &r.BuyerGSTIN, &r.PlaceOfSupply, &r.ReverseCharge, &r.InvoiceDate, &r.DueDate,
		&r.SubtotalMinor, &r.CGSTMinor, &r.SGSTMinor, &r.IGSTMinor, &r.TotalMinor, &r.PaidMinor, &r.CreditedMinor,
		&r.WrittenOffMinor, &r.IRN, &r.AckNo, &ack, &r.SignedQR, &irnC, &issued, &canc, &r.BranchID)
	r.AckDate, r.IRNCancelledAt, r.IssuedAt, r.CancelledAt = command.TsPtr(ack), command.TsPtr(irnC), command.TsPtr(issued), command.TsPtr(canc)
	r.ackTime = ack
	return r, err
}

func loadInvoice(c *command.Ctx, id string, lock bool) (invoiceRow, error) {
	q := "select " + invCols + ` from billing.invoices i join core.notes n on n.id = i.note_id
		where i.note_id = $1::uuid and n.deleted_at is null `
	if lock {
		q += "for update of i"
	}
	r, err := scanInvoice(c.Tx.QueryRow(c.Context, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, command.ErrNotFound("invoice")
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
	ItemNoteID   *string `json:"item_note_id"`
}

type paymentOut struct {
	ID          string     `json:"id"`
	AmountMinor int64      `json:"amount_minor"`
	Method      string     `json:"method"`
	Reference   *string    `json:"reference"`
	ReceivedAt  command.Ts `json:"received_at"`
}

type invoiceOut struct {
	invoiceRow
	ContactName *string      `json:"contact_name"`
	Outstanding int64        `json:"outstanding_minor"`
	Lines       []lineOut    `json:"lines"`
	Payments    []paymentOut `json:"payments"`
}

// ---------- lines ----------

type lineIn struct {
	description string
	hsn         *string
	qty         *string // canonical decimal text
	price       int64
	rateBP      *int64
	item        *string
}

func decodeLines(o obj, key string, required bool) ([]lineIn, bool, error) {
	v, ok := o.get(key)
	if !ok {
		if required {
			return nil, false, command.ErrFallback
		}
		return nil, false, nil // Option<Vec>: missing or null
	}
	var raw []json.RawMessage
	if json.Unmarshal(v, &raw) != nil || raw == nil {
		return nil, false, command.ErrFallback
	}
	out := make([]lineIn, 0, len(raw))
	for _, r := range raw {
		lo, err := parseObj(r)
		if err != nil {
			return nil, false, err
		}
		var l lineIn
		if l.description, err = lo.reqStr("description"); err != nil {
			return nil, false, err
		}
		if l.hsn, err = lo.optStr("hsn_sac"); err != nil {
			return nil, false, err
		}
		if q, ok := lo.get("quantity"); ok {
			t, err := quantityText(q)
			if err != nil {
				return nil, false, err
			}
			l.qty = &t
		}
		if l.price, err = lo.reqInt("unit_price_minor"); err != nil {
			return nil, false, err
		}
		if l.rateBP, err = lo.optInt("gst_rate_bp", 32, true); err != nil {
			return nil, false, err
		}
		if l.item, err = lo.optUUID("item_note_id"); err != nil {
			return nil, false, err
		}
		out = append(out, l)
	}
	return out, true, nil
}

type ln struct {
	description string
	item        *string
	g           gstLine
}

func parseLines(in []lineIn) ([]ln, error) {
	if len(in) == 0 {
		return nil, command.ErrInvalid("at least one line is required")
	}
	out := make([]ln, 0, len(in))
	for _, l := range in {
		if strings.TrimSpace(l.description) == "" {
			return nil, command.ErrInvalid("line description is required")
		}
		qtext := "1"
		if l.qty != nil {
			qtext = *l.qty
		}
		q, ok := parseDec(qtext)
		if !ok {
			return nil, command.ErrInvalid("invalid quantity")
		}
		g := gstLine{qty: q, qtyText: qtext, price: l.price, rateBP: 1800}
		if l.rateBP != nil {
			g.rateBP = uint32(*l.rateBP)
		}
		if l.hsn != nil && strings.TrimSpace(*l.hsn) != "" {
			g.hsn = l.hsn
		}
		if _, err := taxablePaise(g.qty, g.price); err != nil {
			return nil, gstErrToCmd(err)
		}
		if g.rateBP > 2800 {
			return nil, gstErrToCmd(errRate)
		}
		if g.hsn != nil {
			if err := validateHSN(*g.hsn); err != nil {
				return nil, gstErrToCmd(err)
			}
		}
		out = append(out, ln{strings.TrimSpace(l.description), l.item, g})
	}
	return out, nil
}

func loadLines(c *command.Ctx, invoiceID string) ([]ln, error) {
	return loadLinesOf(c, "billing.invoice_lines", "invoice_id", invoiceID)
}

// loadLinesOf reads lines from an invoice-shaped line table (table and col are constants, never user input).
func loadLinesOf(c *command.Ctx, table, col, id string) ([]ln, error) {
	rows, err := c.Tx.Query(c.Context,
		`select description, hsn_sac, quantity::text, unit_price_minor, gst_rate_bp, item_note_id::text
		 from `+table+` where `+col+` = $1::uuid order by position`, id)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	var out []ln
	for rows.Next() {
		var l ln
		var qty string
		var rate int32
		if err := rows.Scan(&l.description, &l.g.hsn, &qty, &l.g.price, &rate, &l.item); err != nil {
			return nil, command.DB(err)
		}
		q, ok := parseDec(qty)
		if !ok {
			return nil, command.NewError(command.Internal, "bad stored quantity")
		}
		l.g.qty, l.g.qtyText, l.g.rateBP = q, qty, uint32(rate)
		out = append(out, l)
	}
	return out, command.DB(rows.Err())
}

// writeLines replaces an invoice's lines. With tax the computed GST is stored; without, only taxable amounts (drafts).
func writeLines(c *command.Ctx, invoiceID string, lines []ln, tax *invoiceTax) (int64, error) {
	return writeLinesTo(c, "billing.invoice_lines", "invoice_id", invoiceID, lines, tax)
}

func writeLinesTo(c *command.Ctx, table, col, invoiceID string, lines []ln, tax *invoiceTax) (int64, error) {
	if _, err := c.Tx.Exec(c.Context, "delete from "+table+" where "+col+" = $1::uuid", invoiceID); err != nil {
		return 0, command.DB(err)
	}
	var subtotal int64
	for pos, l := range lines {
		var t lineTax
		if tax != nil {
			t = tax.lines[pos]
		} else {
			tp, err := taxablePaise(l.g.qty, l.g.price)
			if err != nil {
				return 0, gstErrToCmd(err)
			}
			t = lineTax{taxable: tp}
		}
		subtotal += t.taxable
		if _, err := c.Tx.Exec(c.Context,
			`insert into `+table+` (workspace_id, `+col+`, position, description, hsn_sac, quantity,
			   unit_price_minor, gst_rate_bp, taxable_minor, cgst_minor, sgst_minor, igst_minor, item_note_id)
			 values ($1::uuid,$2::uuid,$3,$4,$5,$6::numeric,$7,$8,$9,$10,$11,$12,$13::uuid)`,
			c.WorkspaceID(), invoiceID, int32(pos+1), l.description, l.g.hsn, l.g.qtyText, l.g.price, int32(l.g.rateBP),
			t.taxable, t.cgst, t.sgst, t.igst, l.item); err != nil {
			return 0, command.DB(err)
		}
	}
	return subtotal, nil
}

// linkItems relates the invoice to every item it bills, after checking the caller can read them.
func linkItems(c *command.Ctx, invoiceID string, lines []ln) error {
	if _, err := c.Tx.Exec(c.Context, "delete from core.relations where from_id = $1::uuid and kind = 'bills'", invoiceID); err != nil {
		return command.DB(err)
	}
	for _, l := range lines {
		if l.item == nil {
			continue
		}
		if _, err := access.AssertNoteAccess(c, *l.item, false); err != nil {
			return err
		}
		if err := notes.AddRelation(c, invoiceID, *l.item, "bills"); err != nil {
			return command.DB(err)
		}
	}
	return nil
}

// ---------- helpers ----------

type contactInfo struct {
	name         string
	gstin, state *string
}

func loadContact(c *command.Ctx, id string) (contactInfo, error) {
	if _, err := access.AssertNoteAccess(c, id, false); err != nil {
		return contactInfo{}, err
	}
	var ci contactInfo
	err := c.Tx.QueryRow(c.Context,
		`select n.title, c.gstin, c.state_code from crm.contacts c join core.notes n on n.id = c.note_id
		 where c.note_id = $1::uuid and n.deleted_at is null`, id).Scan(&ci.name, &ci.gstin, &ci.state)
	if errors.Is(err, pgx.ErrNoRows) {
		return ci, command.ErrNotFound("contact")
	}
	return ci, command.DB(err)
}

type seller struct {
	gstin, state string
	aato         bool
}

func workspaceSettings(c *command.Ctx) (map[string]json.RawMessage, error) {
	var raw []byte
	if err := c.Tx.QueryRow(c.Context, "select settings from core.workspaces where id = $1::uuid", c.WorkspaceID()).Scan(&raw); err != nil {
		return nil, command.DB(err)
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m) // non-object settings behave like serde's Value indexing: every key is Null
	return m, nil
}

// str and boolean mirror serde's Value::as_str / as_bool: null, missing and wrong types are None.
func (s settingsMap) str(k string) (string, bool) {
	var v *string
	if json.Unmarshal(s[k], &v) != nil || v == nil {
		return "", false
	}
	return *v, true
}

func (s settingsMap) boolean(k string) (bool, bool) {
	var v *bool
	if json.Unmarshal(s[k], &v) != nil || v == nil {
		return false, false
	}
	return *v, true
}

type settingsMap map[string]json.RawMessage

// loadSeller is the supplier on a document: the branch's own GSTIN and state when the document belongs to a branch
// that has one, else the workspace's.
func loadSeller(c *command.Ctx, branchID *string) (seller, error) {
	m, err := workspaceSettings(c)
	if err != nil {
		return seller{}, err
	}
	s := settingsMap(m)
	aato, _ := s.boolean("aato_above_10cr")
	if branchID != nil {
		var g, st *string
		if err := c.Tx.QueryRow(c.Context, "select gstin, state_code from core.branches where id = $1::uuid", *branchID).Scan(&g, &st); err == nil && g != nil && st != nil {
			return seller{*g, *st, aato}, nil
		}
	}
	g, ok := s.str("gstin")
	if !ok {
		return seller{}, command.ErrInvalid("set the workspace GSTIN (workspace.update settings.gstin) before issuing invoices")
	}
	g, err = validateGSTIN(g)
	if err != nil {
		return seller{}, gstErrToCmd(err)
	}
	state, ok := s.str("state_code")
	if !ok {
		state = gstinState(g)
	}
	if err := validateState(state); err != nil {
		return seller{}, gstErrToCmd(err)
	}
	return seller{g, state, aato}, nil
}

func formatNumber(prefix, fy string, n int32) string { return fmt.Sprintf("%s/%s/%04d", prefix, fy, n) }

// nextNumber is the next gap-free number for (series, FY). The UPDATE row-locks the counter until commit.
func nextNumber(c *command.Ctx, series, prefix, fy string) (string, error) {
	name := series + ":" + fy
	if _, err := c.Tx.Exec(c.Context, "insert into billing.sequences (workspace_id, name) values ($1::uuid,$2) on conflict do nothing", c.WorkspaceID(), name); err != nil {
		return "", command.DB(err)
	}
	var n int32
	if err := c.Tx.QueryRow(c.Context,
		"update billing.sequences set next_value = next_value + 1 where workspace_id = $1::uuid and name = $2 returning next_value - 1",
		c.WorkspaceID(), name).Scan(&n); err != nil {
		return "", command.DB(err)
	}
	return formatNumber(prefix, fy, n), nil
}

// settledStatus: nothing outstanding means paid (settled).
func settledStatus(total, paid, credited, writtenOff int64, current string) string {
	switch {
	case total-paid-credited-writtenOff == 0:
		return "paid"
	case paid > 0:
		return "partly_paid"
	}
	return current
}

func requireOpen(i invoiceRow) error {
	switch i.Status {
	case "issued", "irn_generated", "sent", "partly_paid":
		if i.Kind == "invoice" {
			return nil
		}
	}
	return command.ErrConflict(fmt.Sprintf("invoice is %s and cannot take this action", i.Status))
}

func nilStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// ---------- drafts ----------

func invoiceCreateDraft(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	spaceID, err := o.reqUUID("space_id")
	if err != nil {
		return nil, err
	}
	contactID, err := o.reqUUID("contact_id")
	if err != nil {
		return nil, err
	}
	rawLines, _, err := decodeLines(o, "lines", true)
	if err != nil {
		return nil, err
	}
	var invDate, dueDate *string
	if invDate, err = o.optDate("invoice_date"); err != nil {
		return nil, err
	}
	if dueDate, err = o.optDate("due_date"); err != nil {
		return nil, err
	}
	place, err := o.optStr("place_of_supply")
	if err != nil {
		return nil, err
	}
	reverse, err := o.optBool("reverse_charge")
	if err != nil {
		return nil, err
	}
	notesMD, err := o.optStr("notes")
	if err != nil {
		return nil, err
	}
	branchIn, err := o.optUUID("branch_id")
	if err != nil {
		return nil, err
	}

	lines, err := parseLines(rawLines)
	if err != nil {
		return nil, err
	}
	ci, err := loadContact(c, contactID)
	if err != nil {
		return nil, err
	}
	if place != nil {
		if err := validateState(*place); err != nil {
			return nil, gstErrToCmd(err)
		}
	}
	branch, err := ResolveBranch(c, branchIn)
	if err != nil {
		return nil, err
	}
	date := fmtDate(todayIST())
	if invDate != nil {
		date = *invDate
	}
	note, err := notes.InsertNote(c, spaceID, "Invoice for "+ci.name, nil, notesMD, []string{"invoice"})
	if err != nil {
		return nil, err
	}
	noteID := note.ID
	pos := place
	if pos == nil {
		pos = ci.state
	}
	rc := false
	if reverse != nil {
		rc = *reverse
	}
	if _, err := c.Tx.Exec(c.Context,
		`insert into billing.invoices (note_id, workspace_id, kind, status, contact_id, buyer_gstin, place_of_supply,
		   reverse_charge, invoice_date, due_date, branch_id)
		 values ($1::uuid,$2::uuid,'invoice','draft',$3::uuid,$4,$5,$6,$7::date,coalesce($8::date, $7::date + 30),$9::uuid)`,
		noteID, c.WorkspaceID(), contactID, nilStr(ci.gstin), nilStr(pos), rc, date, nilStr(dueDate), nilStr(branch)); err != nil {
		return nil, command.DB(err)
	}
	subtotal, err := writeLines(c, noteID, lines, nil)
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "update billing.invoices set subtotal_minor = $2, total_minor = $2 where note_id = $1::uuid", noteID, subtotal); err != nil {
		return nil, command.DB(err)
	}
	if err := linkItems(c, noteID, lines); err != nil {
		return nil, err
	}
	if _, err := c.Emit("invoice.drafted", &noteID, map[string]any{"contact_id": contactID, "subtotal_minor": subtotal}); err != nil {
		return nil, err
	}
	return invoiceDetail(c, noteID)
}

func invoiceUpdateDraft(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.reqUUID("id")
	if err != nil {
		return nil, err
	}
	contactID, err := o.optUUID("contact_id")
	if err != nil {
		return nil, err
	}
	rawLines, haveLines, err := decodeLines(o, "lines", false)
	if err != nil {
		return nil, err
	}
	invDate, err := o.optDate("invoice_date")
	if err != nil {
		return nil, err
	}
	dueDate, err := o.optDate("due_date")
	if err != nil {
		return nil, err
	}
	place, err := o.optStr("place_of_supply")
	if err != nil {
		return nil, err
	}
	reverse, err := o.optBool("reverse_charge")
	if err != nil {
		return nil, err
	}
	branchIn, err := o.optUUID("branch_id")
	if err != nil {
		return nil, err
	}

	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	inv, err := loadInvoice(c, id, true)
	if err != nil {
		return nil, err
	}
	if inv.Status != "draft" {
		return nil, command.ErrConflict("only drafts can be edited; issued invoices change through credit notes")
	}
	if place != nil {
		if err := validateState(*place); err != nil {
			return nil, gstErrToCmd(err)
		}
	}
	var branch *string
	if branchIn != nil {
		if branch, err = ResolveBranch(c, branchIn); err != nil {
			return nil, err
		}
	}
	var buyer, contactState *string
	if contactID != nil {
		ci, err := loadContact(c, *contactID)
		if err != nil {
			return nil, err
		}
		buyer, contactState = ci.gstin, ci.state
	}
	if _, err := c.Tx.Exec(c.Context,
		`update billing.invoices set contact_id = coalesce($2::uuid, contact_id), buyer_gstin = case when $2::uuid is null then buyer_gstin else $3::text end,
		   place_of_supply = coalesce($4::text, $5::text, place_of_supply), reverse_charge = coalesce($6::boolean, reverse_charge),
		   invoice_date = coalesce($7::date, invoice_date), due_date = coalesce($8::date, due_date), branch_id = coalesce($9::uuid, branch_id)
		 where note_id = $1::uuid`,
		id, nilStr(contactID), nilStr(buyer), nilStr(place), nilStr(contactState), reverse, nilStr(invDate), nilStr(dueDate), nilStr(branch)); err != nil {
		return nil, command.DB(err)
	}
	if haveLines {
		lines, err := parseLines(rawLines)
		if err != nil {
			return nil, err
		}
		subtotal, err := writeLines(c, id, lines, nil)
		if err != nil {
			return nil, err
		}
		if _, err := c.Tx.Exec(c.Context, "update billing.invoices set subtotal_minor = $2, total_minor = $2 where note_id = $1::uuid", id, subtotal); err != nil {
			return nil, command.DB(err)
		}
		if err := linkItems(c, id, lines); err != nil {
			return nil, err
		}
	}
	if _, err := c.Emit("invoice.draft_updated", &id, map[string]any{}); err != nil {
		return nil, err
	}
	return invoiceDetail(c, id)
}

// ---------- read ----------

func byID(input json.RawMessage) (string, error) {
	o, err := parseObj(input)
	if err != nil {
		return "", err
	}
	return o.reqUUID("id")
}

func invoiceDetail(c *command.Ctx, id string) (invoiceOut, error) {
	inv, err := loadInvoice(c, id, false)
	if err != nil {
		return invoiceOut{}, err
	}
	out := invoiceOut{invoiceRow: inv, Outstanding: inv.outstanding(), Lines: []lineOut{}, Payments: []paymentOut{}}
	if inv.ContactID != nil {
		// The contact's name only when the caller may read the contact page.
		spaces, err := access.ReadableSpaceIDs(c)
		if err != nil {
			return out, err
		}
		uid, admin := access.Viewer(c)
		err = c.Tx.QueryRow(c.Context,
			"select title from core.notes where id = $1::uuid and core.note_access(id, $2::uuid, $3, $4::text[]::uuid[]) <> 'none'",
			*inv.ContactID, uid, admin, spaces).Scan(&out.ContactName)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return out, command.DB(err)
		}
	}
	rows, err := c.Tx.Query(c.Context,
		`select id::text, position, description, hsn_sac, quantity::text, unit_price_minor, gst_rate_bp,
		   taxable_minor, cgst_minor, sgst_minor, igst_minor, item_note_id::text
		 from billing.invoice_lines where invoice_id = $1::uuid order by position`, id)
	if err != nil {
		return out, command.DB(err)
	}
	for rows.Next() {
		var l lineOut
		if err := rows.Scan(&l.ID, &l.Position, &l.Description, &l.HsnSac, &l.Quantity, &l.UnitPrice, &l.GSTRateBP,
			&l.TaxableMinor, &l.CGSTMinor, &l.SGSTMinor, &l.IGSTMinor, &l.ItemNoteID); err != nil {
			rows.Close()
			return out, command.DB(err)
		}
		out.Lines = append(out.Lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, command.DB(err)
	}
	prows, err := c.Tx.Query(c.Context,
		"select id::text, amount_minor, method, reference, received_at from billing.payments where invoice_id = $1::uuid order by received_at", id)
	if err != nil {
		return out, command.DB(err)
	}
	defer prows.Close()
	for prows.Next() {
		var p paymentOut
		var at time.Time
		if err := prows.Scan(&p.ID, &p.AmountMinor, &p.Method, &p.Reference, &at); err != nil {
			return out, command.DB(err)
		}
		p.ReceivedAt = command.Ts(at)
		out.Payments = append(out.Payments, p)
	}
	return out, command.DB(prows.Err())
}

func invoiceGet(c *command.Ctx, input json.RawMessage) (any, error) {
	id, err := byID(input)
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, false); err != nil {
		return nil, err
	}
	return invoiceDetail(c, id)
}

func invoiceList(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	status, err := o.optStr("status")
	if err != nil {
		return nil, err
	}
	contact, err := o.optUUID("contact_id")
	if err != nil {
		return nil, err
	}
	kind, err := o.optStr("kind")
	if err != nil {
		return nil, err
	}
	branch, err := o.optUUID("branch_id")
	if err != nil {
		return nil, err
	}
	limit, err := o.optInt("limit", 64, false)
	if err != nil {
		return nil, err
	}
	lim := int64(100)
	if limit != nil {
		lim = min(max(*limit, 1), 500)
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		"select "+invCols+` from billing.invoices i join core.notes n on n.id = i.note_id
		 where n.deleted_at is null and n.space_id = any($1::text[]::uuid[]) and ($2::text is null or i.status = $2)
		   and core.note_access(n.id, $6::uuid, $7, $1::text[]::uuid[]) <> 'none'
		   and ($3::uuid is null or i.contact_id = $3::uuid) and ($4::text is null or i.kind = $4)
		   and ($8::uuid is null or i.branch_id = $8::uuid)
		 order by n.created_at desc limit $5`,
		spaces, nilStr(status), nilStr(contact), nilStr(kind), lim, uid, admin, nilStr(branch))
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []invoiceRow{}
	for rows.Next() {
		r, err := scanInvoice(rows)
		if err != nil {
			return nil, command.DB(err)
		}
		out = append(out, r)
	}
	return out, command.DB(rows.Err())
}

// draftTax: lines and GST for a draft as issue would compute them; also the buyer GSTIN and place of supply.
func draftTax(c *command.Ctx, inv invoiceRow, s seller) ([]ln, invoiceTax, string, *string, error) {
	var contact *contactInfo
	if inv.ContactID != nil {
		ci, err := loadContact(c, *inv.ContactID)
		if err != nil {
			return nil, invoiceTax{}, "", nil, err
		}
		contact = &ci
	}
	buyer := inv.BuyerGSTIN
	if contact != nil && contact.gstin != nil {
		buyer = contact.gstin
	}
	var place *string
	switch {
	case inv.PlaceOfSupply != nil:
		place = inv.PlaceOfSupply
	case contact != nil && contact.state != nil:
		place = contact.state
	case buyer != nil:
		p := gstinState(*buyer)
		place = &p
	}
	if place == nil {
		return nil, invoiceTax{}, "", nil, command.ErrInvalid("place of supply is unknown: set it or give the contact a state code")
	}
	lines, err := loadLines(c, inv.ID)
	if err != nil {
		return nil, invoiceTax{}, "", nil, err
	}
	if len(lines) == 0 {
		return nil, invoiceTax{}, "", nil, command.ErrInvalid("an invoice needs at least one line")
	}
	tax, err := computeInvoice(gstLines(lines), s.state, *place, inv.ReverseCharge)
	if err != nil {
		return nil, invoiceTax{}, "", nil, gstErrToCmd(err)
	}
	return lines, tax, *place, buyer, nil
}

func gstLines(l []ln) []gstLine {
	g := make([]gstLine, len(l))
	for i := range l {
		g[i] = l[i].g
	}
	return g
}

// invoicePreview is read-only: the totals invoice.issue would produce for this draft.
func invoicePreview(c *command.Ctx, input json.RawMessage) (any, error) {
	id, err := byID(input)
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, false); err != nil {
		return nil, err
	}
	inv, err := loadInvoice(c, id, false)
	if err != nil {
		return nil, err
	}
	if inv.Status != "draft" {
		return nil, command.ErrConflict("only a draft can be previewed")
	}
	s, err := loadSeller(c, inv.BranchID)
	if err != nil {
		return nil, err
	}
	_, t, place, _, err := draftTax(c, inv, s)
	if err != nil {
		return nil, err
	}
	return map[string]any{"intra_state": t.supply == intra, "place_of_supply": place, "supplier_state": s.state,
		"reverse_charge": t.reverse, "subtotal_minor": t.subtotal, "cgst_minor": t.cgst,
		"sgst_minor": t.sgst, "igst_minor": t.igst, "total_minor": t.total}, nil
}

// ---------- issue ----------

func invoiceIssue(c *command.Ctx, input json.RawMessage) (any, error) {
	id, err := byID(input)
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	inv, err := loadInvoice(c, id, true)
	if err != nil {
		return nil, err
	}
	if inv.Kind != "invoice" || inv.Status != "draft" {
		return nil, command.ErrConflict("only a draft invoice can be issued")
	}
	s, err := loadSeller(c, inv.BranchID)
	if err != nil {
		return nil, err
	}
	lines, tax, place, buyer, err := draftTax(c, inv, s)
	if err != nil {
		return nil, err
	}
	if _, err := writeLines(c, id, lines, &tax); err != nil {
		return nil, err
	}
	date := todayIST()
	if inv.InvoiceDate != nil {
		date = parseDate(*inv.InvoiceDate)
	}
	number, err := nextNumber(c, "invoice", "INV", financialYear(date))
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context,
		`update billing.invoices set status = 'issued', number = $2, issued_at = now(), supplier_gstin = $3,
		   supplier_state = $4, buyer_gstin = $5, place_of_supply = $6, invoice_date = $7::date,
		   due_date = coalesce(due_date, $7::date + 30), subtotal_minor = $8, cgst_minor = $9, sgst_minor = $10,
		   igst_minor = $11, total_minor = $12 where note_id = $1::uuid`,
		id, number, s.gstin, s.state, nilStr(buyer), place, fmtDate(date), tax.subtotal, tax.cgst, tax.sgst, tax.igst, tax.total); err != nil {
		return nil, command.DB(err)
	}
	if err := post(c, "invoice", id, "Invoice "+number, saleLegs(tax.subtotal, tax.cgst, tax.sgst, tax.igst, false)); err != nil {
		return nil, err
	}
	if _, err := c.Emit("invoice.issued", &id, map[string]any{"number": number, "total_minor": tax.total, "contact_id": inv.ContactID,
		"reverse_charge": tax.reverse, "intra_state": tax.supply == intra}); err != nil {
		return nil, err
	}
	return invoiceDetail(c, id)
}

// ---------- IRN ----------

func buildIRNRequest(inv invoiceRow) (irnRequest, error) {
	date := todayIST()
	if inv.InvoiceDate != nil {
		date = parseDate(*inv.InvoiceDate)
	}
	doc := "INV"
	if inv.Kind == "credit_note" {
		doc = "CRN"
	}
	if inv.BuyerGSTIN == nil {
		return irnRequest{}, command.ErrInvalid("e-invoicing needs the buyer's GSTIN (B2B only)")
	}
	r := irnRequest{DocType: doc, FinancialYear: financialYear(date), InvoiceDate: date, BuyerGSTIN: *inv.BuyerGSTIN, TotalMinor: inv.TotalMinor}
	if inv.SupplierGSTIN != nil {
		r.SupplierGSTIN = *inv.SupplierGSTIN
	}
	if inv.Number != nil {
		r.Number = *inv.Number
	}
	return r, nil
}

func invoiceGenerateIRN(c *command.Ctx, input json.RawMessage) (any, error) {
	id, err := byID(input)
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	inv, err := loadInvoice(c, id, true)
	if err != nil {
		return nil, err
	}
	if inv.Number == nil || inv.Status == "draft" || inv.Status == "cancelled" {
		return nil, command.ErrConflict("invoice must be issued and not cancelled")
	}
	if inv.IRN != nil {
		return nil, command.ErrConflict("an IRN already exists for this document")
	}
	req, err := buildIRNRequest(inv)
	if err != nil {
		return nil, err
	}
	s, err := loadSeller(c, inv.BranchID)
	if err != nil {
		return nil, err
	}
	var warning *string
	switch w, left := irnWindow(req.InvoiceDate, todayIST(), s.aato); w {
	case windowBlocked:
		return nil, command.ErrConflict("IRN reporting window closed: invoices must be reported within 30 days of the invoice date")
	case windowWarn:
		t := warnText(left)
		warning = &t
	}
	r, err := gsp.GenerateIRN(c.Context, req)
	if err != nil {
		return nil, command.NewError(command.Internal, "GSP error: "+err.Error())
	}
	if _, err := c.Tx.Exec(c.Context,
		`update billing.invoices set irn = $2, ack_no = $3, ack_date = $4, signed_qr = $5,
		   status = case when status = 'issued' then 'irn_generated' else status end where note_id = $1::uuid`,
		id, r.IRN, r.AckNo, r.AckDate, r.SignedQR); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("invoice.irn_generated", &id, map[string]any{"irn": r.IRN, "ack_no": r.AckNo}); err != nil {
		return nil, err
	}
	d, err := invoiceDetail(c, id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"invoice": d, "warning": warning}, nil
}

func invoiceCancelIRN(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.reqUUID("id")
	if err != nil {
		return nil, err
	}
	reason, err := o.reqStr("reason")
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	inv, err := loadInvoice(c, id, true)
	if err != nil {
		return nil, err
	}
	if inv.IRN == nil || inv.ackTime == nil {
		return nil, command.ErrConflict("invoice has no IRN")
	}
	if inv.IRNCancelledAt != nil {
		return nil, command.ErrConflict("IRN is already cancelled")
	}
	if time.Now().UTC().Sub(*inv.ackTime) > 24*time.Hour {
		return nil, command.ErrConflict("an IRN can only be cancelled within 24 hours of acknowledgement; issue a credit note instead")
	}
	if err := gsp.CancelIRN(c.Context, *inv.IRN, reason); err != nil {
		return nil, command.NewError(command.Internal, "GSP error: "+err.Error())
	}
	if _, err := c.Tx.Exec(c.Context,
		`update billing.invoices set irn_cancelled_at = now(),
		   status = case when status = 'irn_generated' then 'issued' else status end where note_id = $1::uuid`, id); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("invoice.irn_cancelled", &id, map[string]any{"reason": reason}); err != nil {
		return nil, err
	}
	return invoiceDetail(c, id)
}

// ---------- credit notes ----------

// issueCreditNote issues a credit note against orig for lines: tax uses the original's supply, the ledger is
// reversed and the original's outstanding amount drops. The caller holds the lock on orig.
func issueCreditNote(c *command.Ctx, orig invoiceRow, lines []ln, reason string) (invoiceRow, error) {
	if orig.SupplierState == nil || orig.PlaceOfSupply == nil || orig.Number == nil {
		return invoiceRow{}, command.ErrConflict("invoice has not been issued")
	}
	sstate, place, onum := *orig.SupplierState, *orig.PlaceOfSupply, *orig.Number
	tax, err := computeInvoice(gstLines(lines), sstate, place, orig.ReverseCharge)
	if err != nil {
		return invoiceRow{}, gstErrToCmd(err)
	}
	if tax.total <= 0 {
		return invoiceRow{}, command.ErrInvalid("credit note total must be positive")
	}
	if tax.total > orig.outstanding() {
		return invoiceRow{}, command.ErrInvalid(fmt.Sprintf("credit note total %d exceeds the outstanding amount %d", tax.total, orig.outstanding()))
	}
	var space string
	if err := c.Tx.QueryRow(c.Context, "select space_id::text from core.notes where id = $1::uuid", orig.ID).Scan(&space); err != nil {
		return invoiceRow{}, command.DB(err)
	}
	note, err := notes.InsertNote(c, space, "Credit note for "+onum, nil, &reason, []string{"credit_note"})
	if err != nil {
		return invoiceRow{}, err
	}
	noteID := note.ID
	date := todayIST()
	if _, err := c.Tx.Exec(c.Context,
		`insert into billing.invoices (note_id, workspace_id, kind, status, contact_id, original_invoice_id, supplier_gstin,
		   supplier_state, buyer_gstin, place_of_supply, reverse_charge, invoice_date, branch_id)
		 values ($1::uuid,$2::uuid,'credit_note','draft',$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10::date,$11::uuid)`,
		noteID, c.WorkspaceID(), nilStr(orig.ContactID), orig.ID, nilStr(orig.SupplierGSTIN), sstate, nilStr(orig.BuyerGSTIN),
		place, orig.ReverseCharge, fmtDate(date), nilStr(orig.BranchID)); err != nil {
		return invoiceRow{}, command.DB(err)
	}
	if _, err := writeLines(c, noteID, lines, &tax); err != nil {
		return invoiceRow{}, err
	}
	number, err := nextNumber(c, "credit_note", "CN", financialYear(date))
	if err != nil {
		return invoiceRow{}, err
	}
	if _, err := c.Tx.Exec(c.Context,
		`update billing.invoices set status = 'issued', number = $2, issued_at = now(), subtotal_minor = $3,
		   cgst_minor = $4, sgst_minor = $5, igst_minor = $6, total_minor = $7 where note_id = $1::uuid`,
		noteID, number, tax.subtotal, tax.cgst, tax.sgst, tax.igst, tax.total); err != nil {
		return invoiceRow{}, command.DB(err)
	}
	if err := post(c, "credit_note", noteID, fmt.Sprintf("Credit note %s against %s", number, onum),
		saleLegs(tax.subtotal, tax.cgst, tax.sgst, tax.igst, true)); err != nil {
		return invoiceRow{}, err
	}
	credited := orig.CreditedMinor + tax.total
	if _, err := c.Tx.Exec(c.Context, "update billing.invoices set credited_minor = $2, status = $3 where note_id = $1::uuid",
		orig.ID, credited, settledStatus(orig.TotalMinor, orig.PaidMinor, credited, orig.WrittenOffMinor, orig.Status)); err != nil {
		return invoiceRow{}, command.DB(err)
	}
	if _, err := c.Emit("credit_note.issued", &noteID, map[string]any{"number": number, "original_invoice_id": orig.ID,
		"total_minor": tax.total, "reason": reason}); err != nil {
		return invoiceRow{}, err
	}
	return loadInvoice(c, noteID, false)
}

func creditNoteCreate(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.reqUUID("invoice_id")
	if err != nil {
		return nil, err
	}
	rawLines, _, err := decodeLines(o, "lines", true)
	if err != nil {
		return nil, err
	}
	reason, err := o.reqStr("reason")
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	orig, err := loadInvoice(c, id, true)
	if err != nil {
		return nil, err
	}
	if err := requireOpen(orig); err != nil {
		return nil, err
	}
	lines, err := parseLines(rawLines)
	if err != nil {
		return nil, err
	}
	return issueCreditNote(c, orig, lines, reason)
}

// invoiceCancel is a full credit note plus status cancelled; refused once anything was paid, credited or written off.
func invoiceCancel(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.reqUUID("id")
	if err != nil {
		return nil, err
	}
	reason, err := o.reqStr("reason")
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	inv, err := loadInvoice(c, id, true)
	if err != nil {
		return nil, err
	}
	if err := requireOpen(inv); err != nil {
		return nil, err
	}
	if inv.Status == "partly_paid" || inv.PaidMinor+inv.CreditedMinor+inv.WrittenOffMinor > 0 {
		return nil, command.ErrConflict("invoice has payments, credits or write-offs; use a partial credit note instead")
	}
	if inv.IRN != nil && inv.IRNCancelledAt == nil {
		return nil, command.ErrConflict("cancel the IRN first (allowed within 24 hours of acknowledgement)")
	}
	lines, err := loadLines(c, id)
	if err != nil {
		return nil, err
	}
	cn, err := issueCreditNote(c, inv, lines, reason)
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "update billing.invoices set status = 'cancelled', cancelled_at = now() where note_id = $1::uuid", id); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("invoice.cancelled", &id, map[string]any{"credit_note_id": cn.ID, "reason": reason}); err != nil {
		return nil, err
	}
	return invoiceDetail(c, id)
}

// ---------- payments and write-offs ----------

func paymentRecord(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.reqUUID("invoice_id")
	if err != nil {
		return nil, err
	}
	amount, err := o.reqInt("amount_minor")
	if err != nil {
		return nil, err
	}
	method, err := o.optStr("method")
	if err != nil {
		return nil, err
	}
	reference, err := o.optStr("reference")
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	inv, err := loadInvoice(c, id, true)
	if err != nil {
		return nil, err
	}
	if err := requireOpen(inv); err != nil {
		return nil, err
	}
	if amount <= 0 {
		return nil, command.ErrInvalid("amount must be positive")
	}
	if amount > inv.outstanding() {
		return nil, command.ErrInvalid(fmt.Sprintf("payment %d exceeds the outstanding amount %d", amount, inv.outstanding()))
	}
	m := "upi"
	if method != nil {
		m = *method
	}
	var pid string
	if err := c.Tx.QueryRow(c.Context,
		`insert into billing.payments (workspace_id, invoice_id, amount_minor, method, reference)
		 values ($1::uuid,$2::uuid,$3,$4,$5) returning id::text`,
		c.WorkspaceID(), id, amount, m, nilStr(reference)).Scan(&pid); err != nil {
		return nil, command.DB(err)
	}
	paid := inv.PaidMinor + amount
	status := settledStatus(inv.TotalMinor, paid, inv.CreditedMinor, inv.WrittenOffMinor, inv.Status)
	if _, err := c.Tx.Exec(c.Context, "update billing.invoices set paid_minor = $2, status = $3 where note_id = $1::uuid", id, paid, status); err != nil {
		return nil, command.DB(err)
	}
	if err := post(c, "payment", pid, "Payment for "+deref(inv.Number),
		[]leg{{acctBank, amount, 0}, {acctReceivables, 0, amount}}); err != nil {
		return nil, err
	}
	if _, err := c.Emit("payment.recorded", &id, map[string]any{"payment_id": pid, "amount_minor": amount, "status": status}); err != nil {
		return nil, err
	}
	if status == "paid" {
		if _, err := c.Emit("invoice.paid", &id, map[string]any{"number": inv.Number, "total_minor": inv.TotalMinor}); err != nil {
			return nil, err
		}
	}
	return invoiceDetail(c, id)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func invoiceWriteOff(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.reqUUID("id")
	if err != nil {
		return nil, err
	}
	amt, err := o.optInt("amount_minor", 64, false)
	if err != nil {
		return nil, err
	}
	reason, err := o.reqStr("reason")
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	inv, err := loadInvoice(c, id, true)
	if err != nil {
		return nil, err
	}
	if err := requireOpen(inv); err != nil {
		return nil, err
	}
	amount := inv.outstanding()
	if amt != nil {
		amount = *amt
	}
	if amount <= 0 || amount > inv.outstanding() {
		return nil, command.ErrInvalid("write-off must be positive and within the outstanding amount")
	}
	wo := inv.WrittenOffMinor + amount
	status := settledStatus(inv.TotalMinor, inv.PaidMinor, inv.CreditedMinor, wo, inv.Status)
	if _, err := c.Tx.Exec(c.Context, "update billing.invoices set written_off_minor = $2, status = $3 where note_id = $1::uuid", id, wo, status); err != nil {
		return nil, command.DB(err)
	}
	// GST on bad debt is not reversed here: that is a filing decision for the accountant.
	if err := post(c, "write_off", id, fmt.Sprintf("Write-off %s: %s", deref(inv.Number), reason),
		[]leg{{acctBadDebt, amount, 0}, {acctReceivables, 0, amount}}); err != nil {
		return nil, err
	}
	if _, err := c.Emit("invoice.written_off", &id, map[string]any{"amount_minor": amount, "reason": reason}); err != nil {
		return nil, err
	}
	return invoiceDetail(c, id)
}
