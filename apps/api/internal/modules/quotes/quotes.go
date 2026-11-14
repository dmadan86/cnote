// Package quotes: quotations (estimates). Go-only: Rust has no quote.* commands. A quotation is a typed note plus
// billing.quotations rows; it never posts to the ledger and has its own gap-free number series ("QT"), assigned on send.
//
// Flow: draft -> sent -> accepted | rejected; accepted -> converted (to an invoice draft and/or a project item).
// A sent quotation past valid_until is reported as "expired" (computed on read; accept refuses it).
package quotes

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

func Commands() []command.Command {
	mi, rd := command.GroupManageInvoices, command.GroupRead
	confirm := func(c command.Command) command.Command { c.Confirm = true; return c }
	nc := command.NewCommand
	return []command.Command{
		nc("quote.create_draft", mi, createDraft),
		nc("quote.update_draft", mi, updateDraft),
		nc("quote.get", rd, get),
		nc("quote.list", rd, list),
		confirm(nc("quote.send", mi, send)),
		confirm(nc("quote.accept", mi, decide("accepted"))),
		nc("quote.reject", mi, decide("rejected")),
		confirm(nc("quote.convert_to_invoice", mi, convertToInvoice)),
		nc("quote.convert_to_project", mi, convertToProject),
	}
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// in is every field any quote command takes; pointers distinguish missing from empty.
type in struct {
	ID            string          `json:"id"`
	SpaceID       string          `json:"space_id"`
	ContactID     *string         `json:"contact_id"`
	Status        *string         `json:"status"`
	Limit         *int64          `json:"limit"`
	QuoteDate     *string         `json:"quote_date"`
	ValidUntil    *string         `json:"valid_until"`
	PlaceOfSupply *string         `json:"place_of_supply"`
	ReverseCharge *bool           `json:"reverse_charge"`
	Notes         *string         `json:"notes"`
	Lines         json.RawMessage `json:"lines"`
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
	if v.ContactID != nil && !uuidRe.MatchString(*v.ContactID) {
		return v, command.ErrInvalid("invalid contact_id")
	}
	for _, d := range []*string{v.QuoteDate, v.ValidUntil} {
		if d != nil {
			if _, err := time.Parse("2006-01-02", *d); err != nil {
				return v, command.ErrInvalid("dates must be YYYY-MM-DD")
			}
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

// ---------- rows ----------

type row struct {
	ID                 string      `json:"id"`
	Number             *string     `json:"number"`
	Status             string      `json:"status"`
	ContactID          *string     `json:"contact_id"`
	Currency           string      `json:"currency"`
	SupplierGSTIN      *string     `json:"supplier_gstin"`
	SupplierState      *string     `json:"supplier_state"`
	BuyerGSTIN         *string     `json:"buyer_gstin"`
	PlaceOfSupply      *string     `json:"place_of_supply"`
	ReverseCharge      bool        `json:"reverse_charge"`
	QuoteDate          *string     `json:"quote_date"`
	ValidUntil         *string     `json:"valid_until"`
	SubtotalMinor      int64       `json:"subtotal_minor"`
	CGSTMinor          int64       `json:"cgst_minor"`
	SGSTMinor          int64       `json:"sgst_minor"`
	IGSTMinor          int64       `json:"igst_minor"`
	TotalMinor         int64       `json:"total_minor"`
	SentAt             *command.Ts `json:"sent_at"`
	DecidedAt          *command.Ts `json:"decided_at"`
	ConvertedInvoiceID *string     `json:"converted_invoice_id"`
	ConvertedItemID    *string     `json:"converted_item_id"`
	SpaceID            string      `json:"space_id"`
}

const cols = `q.note_id::text, q.number, q.status, q.contact_id::text, q.currency::text, q.supplier_gstin, q.supplier_state,
	q.buyer_gstin, q.place_of_supply, q.reverse_charge, q.quote_date::text, q.valid_until::text, q.subtotal_minor,
	q.cgst_minor, q.sgst_minor, q.igst_minor, q.total_minor, q.sent_at, q.decided_at, q.converted_invoice_id::text,
	q.converted_item_id::text, n.space_id::text`

func scan(r pgx.Row) (row, error) {
	var x row
	var sent, decided *time.Time
	err := r.Scan(&x.ID, &x.Number, &x.Status, &x.ContactID, &x.Currency, &x.SupplierGSTIN, &x.SupplierState,
		&x.BuyerGSTIN, &x.PlaceOfSupply, &x.ReverseCharge, &x.QuoteDate, &x.ValidUntil, &x.SubtotalMinor,
		&x.CGSTMinor, &x.SGSTMinor, &x.IGSTMinor, &x.TotalMinor, &sent, &decided, &x.ConvertedInvoiceID,
		&x.ConvertedItemID, &x.SpaceID)
	x.SentAt, x.DecidedAt = command.TsPtr(sent), command.TsPtr(decided)
	if x.Status == "sent" && x.ValidUntil != nil && *x.ValidUntil < billing.TodayIST().Format("2006-01-02") {
		x.Status = "expired"
	}
	return x, err
}

func load(c *command.Ctx, id string, lock bool) (row, error) {
	q := "select " + cols + ` from billing.quotations q join core.notes n on n.id = q.note_id
		where q.note_id = $1::uuid and n.deleted_at is null `
	if lock {
		q += "for update of q"
	}
	r, err := scan(c.Tx.QueryRow(c.Context, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, command.ErrNotFound("quotation")
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

type detail struct {
	row
	ContactName *string   `json:"contact_name"`
	Lines       []lineOut `json:"lines"`
}

func linesOf(c *command.Ctx, id string) ([]lineOut, error) {
	rows, err := c.Tx.Query(c.Context,
		`select id::text, position, description, hsn_sac, quantity::text, unit_price_minor, gst_rate_bp, taxable_minor,
		   cgst_minor, sgst_minor, igst_minor, item_note_id::text
		 from billing.quotation_lines where quotation_id = $1::uuid order by position`, id)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []lineOut{}
	for rows.Next() {
		var l lineOut
		if err := rows.Scan(&l.ID, &l.Position, &l.Description, &l.HsnSac, &l.Quantity, &l.UnitPrice, &l.GSTRateBP,
			&l.TaxableMinor, &l.CGSTMinor, &l.SGSTMinor, &l.IGSTMinor, &l.ItemNoteID); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, l)
	}
	return out, command.DB(rows.Err())
}

func detailOf(c *command.Ctx, id string) (detail, error) {
	q, err := load(c, id, false)
	if err != nil {
		return detail{}, err
	}
	d := detail{row: q}
	if q.ContactID != nil {
		// The name only when the caller may read the contact page.
		spaces, err := access.ReadableSpaceIDs(c)
		if err != nil {
			return d, err
		}
		uid, admin := access.Viewer(c)
		err = c.Tx.QueryRow(c.Context,
			"select title from core.notes where id = $1::uuid and core.note_access(id, $2::uuid, $3, $4::text[]::uuid[]) <> 'none'",
			*q.ContactID, uid, admin, spaces).Scan(&d.ContactName)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return d, command.DB(err)
		}
	}
	d.Lines, err = linesOf(c, id)
	return d, err
}

func nilStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func setSubtotal(c *command.Ctx, id string, subtotal int64) error {
	_, err := c.Tx.Exec(c.Context, "update billing.quotations set subtotal_minor = $2, total_minor = $2 where note_id = $1::uuid", id, subtotal)
	return command.DB(err)
}

// ---------- drafts ----------

func createDraft(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	if v.SpaceID == "" || v.ContactID == nil {
		return nil, command.ErrInvalid("space_id and contact_id are required")
	}
	lines, _, err := billing.DecodeLines(input, "lines", true)
	if err != nil {
		return nil, wrap(err)
	}
	name, gstin, state, err := billing.LoadContact(c, *v.ContactID)
	if err != nil {
		return nil, err
	}
	if v.PlaceOfSupply != nil {
		if err := billing.ValidateState(*v.PlaceOfSupply); err != nil {
			return nil, err
		}
	}
	place := v.PlaceOfSupply
	if place == nil {
		place = state
	}
	date := billing.TodayIST().Format("2006-01-02")
	if v.QuoteDate != nil {
		date = *v.QuoteDate
	}
	note, err := notes.InsertNote(c, v.SpaceID, "Quotation for "+name, nil, v.Notes, []string{"quotation"})
	if err != nil {
		return nil, err
	}
	rc := v.ReverseCharge != nil && *v.ReverseCharge
	if _, err := c.Tx.Exec(c.Context,
		`insert into billing.quotations (note_id, workspace_id, status, contact_id, buyer_gstin, place_of_supply,
		   reverse_charge, quote_date, valid_until)
		 values ($1::uuid,$2::uuid,'draft',$3::uuid,$4,$5,$6,$7::date,coalesce($8::date, $7::date + 30))`,
		note.ID, c.WorkspaceID(), *v.ContactID, nilStr(gstin), nilStr(place), rc, date, nilStr(v.ValidUntil)); err != nil {
		return nil, command.DB(err)
	}
	subtotal, err := billing.WriteQuoteLines(c, note.ID, lines, nil)
	if err != nil {
		return nil, err
	}
	if err := setSubtotal(c, note.ID, subtotal); err != nil {
		return nil, err
	}
	if _, err := c.Emit("quote.created", &note.ID, map[string]any{"contact_id": *v.ContactID, "subtotal_minor": subtotal}); err != nil {
		return nil, err
	}
	return detailOf(c, note.ID)
}

// wrap turns billing's serde-fallback signal into a plain invalid-input error: Rust cannot answer quote.* either.
func wrap(err error) error {
	if errors.Is(err, command.ErrFallback) {
		return command.ErrInvalid("invalid lines")
	}
	return err
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
	q, err := load(c, v.ID, true)
	if err != nil {
		return nil, err
	}
	if q.Status != "draft" {
		return nil, command.ErrConflict("only drafts can be edited")
	}
	if v.PlaceOfSupply != nil {
		if err := billing.ValidateState(*v.PlaceOfSupply); err != nil {
			return nil, err
		}
	}
	var buyer, contactState *string
	if v.ContactID != nil {
		_, buyer, contactState, err = billing.LoadContact(c, *v.ContactID)
		if err != nil {
			return nil, err
		}
	}
	if _, err := c.Tx.Exec(c.Context,
		`update billing.quotations set contact_id = coalesce($2::uuid, contact_id),
		   buyer_gstin = case when $2::uuid is null then buyer_gstin else $3::text end,
		   place_of_supply = coalesce($4::text, $5::text, place_of_supply), reverse_charge = coalesce($6::boolean, reverse_charge),
		   quote_date = coalesce($7::date, quote_date), valid_until = coalesce($8::date, valid_until) where note_id = $1::uuid`,
		v.ID, nilStr(v.ContactID), nilStr(buyer), nilStr(v.PlaceOfSupply), nilStr(contactState), v.ReverseCharge,
		nilStr(v.QuoteDate), nilStr(v.ValidUntil)); err != nil {
		return nil, command.DB(err)
	}
	if haveLines {
		subtotal, err := billing.WriteQuoteLines(c, v.ID, lines, nil)
		if err != nil {
			return nil, err
		}
		if err := setSubtotal(c, v.ID, subtotal); err != nil {
			return nil, err
		}
	}
	return detailOf(c, v.ID)
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
		"select "+cols+` from billing.quotations q join core.notes n on n.id = q.note_id
		 where n.deleted_at is null and n.space_id = any($1::text[]::uuid[]) and ($2::text is null or q.status = $2)
		   and core.note_access(n.id, $5::uuid, $6, $1::text[]::uuid[]) <> 'none'
		   and ($3::uuid is null or q.contact_id = $3::uuid)
		 order by n.created_at desc limit $4`,
		spaces, nilStr(v.Status), nilStr(v.ContactID), lim, uid, admin)
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

// lockFor loads the quote for a write and requires one of the statuses.
func lockFor(c *command.Ctx, input json.RawMessage, verb string, from ...string) (row, error) {
	v, err := decode(input)
	if err != nil {
		return row{}, err
	}
	if err := requireID(v); err != nil {
		return row{}, err
	}
	if _, err := access.AssertNoteAccess(c, v.ID, true); err != nil {
		return row{}, err
	}
	q, err := load(c, v.ID, true)
	if err != nil {
		return q, err
	}
	for _, s := range from {
		if q.Status == s {
			return q, nil
		}
	}
	return q, command.ErrConflict(fmt.Sprintf("quotation is %s and cannot be %s", q.Status, verb))
}

func send(c *command.Ctx, input json.RawMessage) (any, error) {
	q, err := lockFor(c, input, "sent", "draft")
	if err != nil {
		return nil, err
	}
	lines, err := billing.LoadQuoteLines(c, q.ID)
	if err != nil {
		return nil, err
	}
	tax, err := billing.ComputeQuoteTax(c, lines, q.PlaceOfSupply, q.ContactID, q.BuyerGSTIN, q.ReverseCharge)
	if err != nil {
		return nil, err
	}
	if _, err := billing.WriteQuoteLines(c, q.ID, lines, &tax); err != nil {
		return nil, err
	}
	date := billing.TodayIST()
	if q.QuoteDate != nil {
		date, _ = time.Parse("2006-01-02", *q.QuoteDate)
	}
	number, err := billing.NextNumber(c, "quote", "QT", billing.FinancialYear(date))
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context,
		`update billing.quotations set status = 'sent', number = $2, sent_at = now(), supplier_gstin = $3, supplier_state = $4,
		   buyer_gstin = $5, place_of_supply = $6, subtotal_minor = $7, cgst_minor = $8, sgst_minor = $9, igst_minor = $10,
		   total_minor = $11 where note_id = $1::uuid`,
		q.ID, number, tax.SupplierGSTIN, tax.SupplierState, nilStr(tax.BuyerGSTIN), tax.Place, tax.Subtotal, tax.CGST,
		tax.SGST, tax.IGST, tax.Total); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("quote.sent", &q.ID, map[string]any{"number": number, "total_minor": tax.Total, "contact_id": q.ContactID}); err != nil {
		return nil, err
	}
	return detailOf(c, q.ID)
}

// decide records the customer's decision on a sent, unexpired quotation.
func decide(to string) command.Handler {
	return func(c *command.Ctx, input json.RawMessage) (any, error) {
		q, err := lockFor(c, input, to, "sent")
		if err != nil {
			return nil, err
		}
		if _, err := c.Tx.Exec(c.Context, "update billing.quotations set status = $2, decided_at = now() where note_id = $1::uuid", q.ID, to); err != nil {
			return nil, command.DB(err)
		}
		kind := map[string]string{"accepted": "quote.accepted", "rejected": "quote.rejected"}[to]
		if _, err := c.Emit(kind, &q.ID, map[string]any{"number": q.Number, "total_minor": q.TotalMinor}); err != nil {
			return nil, err
		}
		return detailOf(c, q.ID)
	}
}

// markConverted links the result and moves the quote to converted.
func markConverted(c *command.Ctx, q row, col, target, what string) error {
	if _, err := c.Tx.Exec(c.Context,
		"update billing.quotations set status = 'converted', "+col+" = $2::uuid where note_id = $1::uuid", q.ID, target); err != nil {
		return command.DB(err)
	}
	_, err := c.Emit("quote.converted", &q.ID, map[string]any{"number": q.Number, "to": what, "target_id": target})
	return err
}

func convertToInvoice(c *command.Ctx, input json.RawMessage) (any, error) {
	q, err := lockFor(c, input, "converted", "accepted", "converted")
	if err != nil {
		return nil, err
	}
	if q.ConvertedInvoiceID != nil {
		return nil, command.ErrConflict("quotation already converted to an invoice")
	}
	rows, err := c.Tx.Query(c.Context,
		`select description, hsn_sac, quantity::text, unit_price_minor, gst_rate_bp, item_note_id::text
		 from billing.quotation_lines where quotation_id = $1::uuid order by position`, q.ID)
	if err != nil {
		return nil, command.DB(err)
	}
	lines := []map[string]any{}
	for rows.Next() {
		var desc, qty string
		var hsn, item *string
		var price int64
		var rate int32
		if err := rows.Scan(&desc, &hsn, &qty, &price, &rate, &item); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		l := map[string]any{"description": desc, "quantity": json.Number(qty), "unit_price_minor": price, "gst_rate_bp": rate}
		if hsn != nil {
			l["hsn_sac"] = *hsn
		}
		if item != nil {
			l["item_note_id"] = *item
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	body := map[string]any{"space_id": q.SpaceID, "contact_id": q.ContactID, "lines": lines, "reverse_charge": q.ReverseCharge}
	if q.PlaceOfSupply != nil {
		body["place_of_supply"] = *q.PlaceOfSupply
	}
	out, err := c.Call("invoice.create_draft", body)
	if err != nil {
		return nil, err
	}
	var inv struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(out, &inv) != nil || inv.ID == "" {
		return nil, command.NewError(command.Internal, "invoice.create_draft returned no id")
	}
	if err := markConverted(c, q, "converted_invoice_id", inv.ID, "invoice"); err != nil {
		return nil, err
	}
	return map[string]any{"quotation": mustDetail(c, q.ID), "invoice": json.RawMessage(out)}, nil
}

func convertToProject(c *command.Ctx, input json.RawMessage) (any, error) {
	q, err := lockFor(c, input, "converted", "accepted", "converted")
	if err != nil {
		return nil, err
	}
	if q.ConvertedItemID != nil {
		return nil, command.ErrConflict("quotation already converted to a project item")
	}
	var title string
	if err := c.Tx.QueryRow(c.Context, "select title from core.notes where id = $1::uuid", q.ID).Scan(&title); err != nil {
		return nil, command.DB(err)
	}
	if q.Number != nil {
		title = *q.Number + " " + title
	}
	out, err := c.Call("item.create", map[string]any{"space_id": q.SpaceID, "title": title,
		"description": fmt.Sprintf("Created from quotation %s", deref(q.Number))})
	if err != nil {
		return nil, err
	}
	var item struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(out, &item) != nil || item.ID == "" {
		return nil, command.NewError(command.Internal, "item.create returned no id")
	}
	if err := markConverted(c, q, "converted_item_id", item.ID, "project"); err != nil {
		return nil, err
	}
	return map[string]any{"quotation": mustDetail(c, q.ID), "item": json.RawMessage(out)}, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func mustDetail(c *command.Ctx, id string) any {
	d, err := detailOf(c, id)
	if err != nil {
		return nil
	}
	return d
}
