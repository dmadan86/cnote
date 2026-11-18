package billing

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

// Import-only commands for the data importers (docs/design/importers.md): opening documents and the opening journal entry.
// An opening document is an unpaid invoice carried over from another system. It is inserted issued, with one "Opening
// balance" line at 0% GST, and never posts to the ledger on its own: the opening entry holds Dr 1100 for all of them.
// Every tax report leaves `opening` rows out; credit notes, cancel and e-invoicing refuse them.

var errOpeningDoc = command.ErrConflict("this is an opening balance document carried over from another system: it has no GST, credit note, cancel or e-invoice; record payments as usual")

const (
	acctPayable = "2100"
	acctOpening = "3900"
)

func openingCommands() []command.Command {
	adm := func(name string, h command.Handler) command.Command {
		c := command.NewCommand(name, command.GroupAdmin, h)
		c.MinRole = command.Admin
		c.Internal = true
		return c
	}
	rd := func(name string, h command.Handler) command.Command { // read helpers of opening.get: the caller is a read command
		c := adm(name, h)
		c.Group = command.GroupRead
		c.Internal = c.Name != "ledger.account_list"
		return c
	}
	return []command.Command{
		adm("invoice.import_opening", invoiceImportOpening),
		adm("invoice.import_opening_cancel", invoiceImportOpeningCancel),
		rd("invoice.import_opening_summary", openingSummary),
		rd("ledger.account_list", accountList), // the web grid reads it directly
		adm("ledger.account_ensure", accountEnsure),
		adm("ledger.opening_post", openingPost),
		adm("ledger.opening_reverse", openingReverse),
	}
}

// openingPosted refuses changes to the opening documents once the opening entry holds them.
func openingPosted(c *command.Ctx) error {
	var st string
	if err := c.Tx.QueryRow(c.Context, "select status from core.opening_balances").Scan(&st); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return command.DB(err)
	}
	if st == "posted" {
		return command.ErrConflict("opening balances are posted: reverse them before changing opening documents")
	}
	return nil
}

func decodeTo(input json.RawMessage, v any) error {
	if json.Unmarshal(input, v) != nil {
		return command.ErrInvalid("input does not match the command's fields")
	}
	return nil
}

func validDay(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil && dateRe.MatchString(s)
}

// invoice.import_opening {space_id, contact_id, number, invoice_date, due_date?, amount_minor} -> the invoice
func invoiceImportOpening(c *command.Ctx, input json.RawMessage) (any, error) {
	var in struct {
		SpaceID     string  `json:"space_id"`
		ContactID   string  `json:"contact_id"`
		Number      string  `json:"number"`
		InvoiceDate string  `json:"invoice_date"`
		DueDate     *string `json:"due_date"`
		AmountMinor int64   `json:"amount_minor"`
	}
	if err := decodeTo(input, &in); err != nil {
		return nil, err
	}
	in.Number = strings.TrimSpace(in.Number)
	if in.SpaceID == "" || in.ContactID == "" || in.Number == "" || len(in.Number) > 64 {
		return nil, command.ErrInvalid("space_id, contact_id and a number of up to 64 characters are required")
	}
	if in.AmountMinor <= 0 || in.AmountMinor > 1_000_000_000_000 {
		return nil, command.ErrInvalid("amount_minor must be positive")
	}
	if !validDay(in.InvoiceDate) || (in.DueDate != nil && !validDay(*in.DueDate)) {
		return nil, command.ErrInvalid("dates must be YYYY-MM-DD")
	}
	if err := openingPosted(c); err != nil {
		return nil, err
	}
	ci, err := loadContact(c, in.ContactID)
	if err != nil {
		return nil, err
	}
	var dup bool
	if err := c.Tx.QueryRow(c.Context, "select exists(select 1 from billing.invoices where number = $1 and not (opening and status = 'cancelled'))", in.Number).Scan(&dup); err != nil {
		return nil, command.DB(err)
	}
	if dup {
		return nil, command.ErrConflict(fmt.Sprintf("invoice number %q already exists", in.Number))
	}
	note, err := notes.InsertNote(c, in.SpaceID, "Opening invoice "+in.Number+" for "+ci.name, nil, nil, []string{"invoice"})
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context,
		`insert into billing.invoices (note_id, workspace_id, kind, status, contact_id, buyer_gstin, place_of_supply, invoice_date, due_date, opening)
		 values ($1::uuid,$2::uuid,'invoice','draft',$3::uuid,$4,$5,$6::date,coalesce($7::date, $6::date + 30),true)`,
		note.ID, c.WorkspaceID(), in.ContactID, nilStr(ci.gstin), nilStr(ci.state), in.InvoiceDate, nilStr(in.DueDate)); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Tx.Exec(c.Context,
		`insert into billing.invoice_lines (workspace_id, invoice_id, position, description, quantity, unit_price_minor, gst_rate_bp, taxable_minor)
		 values ($1::uuid,$2::uuid,1,'Opening balance',1,$3,0,$3)`, c.WorkspaceID(), note.ID, in.AmountMinor); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Tx.Exec(c.Context,
		`update billing.invoices set status = 'issued', number = $2, issued_at = now(), subtotal_minor = $3, total_minor = $3 where note_id = $1::uuid`,
		note.ID, in.Number, in.AmountMinor); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("invoice.opening_imported", &note.ID, map[string]any{"number": in.Number, "total_minor": in.AmountMinor, "contact_id": in.ContactID}); err != nil {
		return nil, err
	}
	return invoiceDetail(c, note.ID)
}

// invoice.import_opening_cancel {id}: undo of an import. Only an untouched opening invoice (no payment, credit or write-off).
func invoiceImportOpeningCancel(c *command.Ctx, input json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := decodeTo(input, &in); err != nil {
		return nil, err
	}
	if err := openingPosted(c); err != nil {
		return nil, err
	}
	inv, err := loadInvoice(c, in.ID, true)
	if err != nil {
		return nil, err
	}
	if !inv.Opening {
		return nil, command.ErrConflict("not an opening balance invoice")
	}
	if inv.Status == "cancelled" {
		return map[string]any{"id": inv.ID, "status": "cancelled"}, nil
	}
	if inv.PaidMinor+inv.CreditedMinor+inv.WrittenOffMinor > 0 {
		return nil, command.ErrConflict("a payment, credit or write-off is recorded on this opening invoice")
	}
	if _, err := c.Tx.Exec(c.Context, "update billing.invoices set status = 'cancelled', cancelled_at = now() where note_id = $1::uuid", inv.ID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("invoice.opening_cancelled", &inv.ID, map[string]any{"number": inv.Number}); err != nil {
		return nil, err
	}
	return map[string]any{"id": inv.ID, "status": "cancelled"}, nil
}

// ledger.account_list {} -> [{code,name,kind}]
func accountList(c *command.Ctx, _ json.RawMessage) (any, error) {
	rows, err := c.Tx.Query(c.Context, "select code, name, kind from billing.accounts order by code")
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var code, name, kind string
		if err := rows.Scan(&code, &name, &kind); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, map[string]string{"code": code, "name": name, "kind": kind})
	}
	return out, command.DB(rows.Err())
}

// New accounts from another system get a code in the range of their kind, above the seeded chart.
var rangeStart = map[string]int{"asset": 1400, "liability": 2400, "equity": 3100, "revenue": 4100, "expense": 5200}

// ledger.account_ensure {name, kind} -> {code,name,kind,created}: the account with this name (case-insensitive), else a new one.
func accountEnsure(c *command.Ctx, input json.RawMessage) (any, error) {
	var in struct{ Name, Kind string }
	if err := decodeTo(input, &in); err != nil {
		return nil, err
	}
	in.Name = strings.TrimSpace(in.Name)
	start, ok := rangeStart[in.Kind]
	if !ok || in.Name == "" || len(in.Name) > 120 {
		return nil, command.ErrInvalid("name and a kind of asset, liability, equity, revenue or expense are required")
	}
	var code, kind string
	err := c.Tx.QueryRow(c.Context, "select code, kind from billing.accounts where lower(name) = lower($1) order by code limit 1", in.Name).Scan(&code, &kind)
	if err == nil {
		return map[string]any{"code": code, "name": in.Name, "kind": kind, "created": false}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, command.DB(err)
	}
	var next int
	if err := c.Tx.QueryRow(c.Context,
		"select coalesce(max(code::int) + 1, $2) from billing.accounts where kind = $1 and code ~ '^[0-9]{4}$' and code::int >= $2 and code::int < $2 + 500", in.Kind, start).Scan(&next); err != nil {
		return nil, command.DB(err)
	}
	code = fmt.Sprint(next)
	if _, err := c.Tx.Exec(c.Context, "insert into billing.accounts (workspace_id, code, name, kind) values ($1::uuid,$2,$3,$4)", c.WorkspaceID(), code, in.Name, in.Kind); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"code": code, "name": in.Name, "kind": in.Kind, "created": true}, nil
}

var codeRe = regexp.MustCompile(`^[0-9]{4}$`)

type gridLine struct {
	AccountCode string `json:"account_code"`
	DrMinor     int64  `json:"dr_minor"`
	CrMinor     int64  `json:"cr_minor"`
}

// ledger.opening_post {go_live, lines:[{account_code,dr_minor,cr_minor}]} posts ONE entry dated the day before go-live:
// the grid lines, Dr 1100 for all live opening invoices, Cr 2100 for all live opening bills, and the balance to 3900.
func openingPost(c *command.Ctx, input json.RawMessage) (any, error) {
	var in struct {
		GoLive string     `json:"go_live"`
		Lines  []gridLine `json:"lines"`
	}
	if err := decodeTo(input, &in); err != nil {
		return nil, err
	}
	golive, err := time.Parse("2006-01-02", in.GoLive)
	if err != nil || !validDay(in.GoLive) {
		return nil, command.ErrInvalid("go_live must be YYYY-MM-DD")
	}
	var st string // the importers module holds this row locked; only a draft (or a reversed one, to post again) posts
	if err := c.Tx.QueryRow(c.Context, "select status from core.opening_balances").Scan(&st); err != nil || st == "posted" {
		return nil, command.ErrConflict("opening balances are already posted, or have no draft to post")
	}
	for _, a := range [][2]string{{acctPayable, "Accounts payable"}, {acctOpening, "Opening balance adjustments"}} {
		kind := "liability"
		if a[0] == acctOpening {
			kind = "equity"
		}
		if _, err := c.Tx.Exec(c.Context, "insert into billing.accounts (workspace_id, code, name, kind) values ($1::uuid,$2,$3,$4) on conflict do nothing", c.WorkspaceID(), a[0], a[1], kind); err != nil {
			return nil, command.DB(err)
		}
	}
	legs := []leg{}
	seen := map[string]bool{}
	for _, l := range in.Lines {
		switch {
		case !codeRe.MatchString(l.AccountCode):
			return nil, command.ErrInvalid("account_code must be a 4 digit account code")
		case l.AccountCode == acctReceivables || l.AccountCode == acctPayable:
			return nil, command.ErrInvalid("customer and vendor balances come from the opening invoices and bills; leave 1100 and 2100 out of the grid")
		case l.AccountCode == acctOpening:
			return nil, command.ErrInvalid("3900 is the balancing account and is filled in for you")
		case seen[l.AccountCode]:
			return nil, command.ErrInvalid("account " + l.AccountCode + " appears twice")
		case l.DrMinor < 0 || l.CrMinor < 0 || (l.DrMinor > 0 && l.CrMinor > 0) || l.DrMinor > 1_000_000_000_000 || l.CrMinor > 1_000_000_000_000:
			return nil, command.ErrInvalid("account " + l.AccountCode + ": give either a debit or a credit amount")
		}
		seen[l.AccountCode] = true
		var ok bool
		if err := c.Tx.QueryRow(c.Context, "select exists(select 1 from billing.accounts where code = $1)", l.AccountCode).Scan(&ok); err != nil {
			return nil, command.DB(err)
		}
		if !ok {
			return nil, command.ErrInvalid("unknown account " + l.AccountCode)
		}
		legs = append(legs, leg{l.AccountCode, l.DrMinor, l.CrMinor})
	}
	var invs, bills int64
	if err := c.Tx.QueryRow(c.Context,
		`select coalesce((select sum(total_minor) from billing.invoices where opening and status <> 'cancelled'), 0)::bigint,
		        coalesce((select sum(total_minor) from billing.purchase_bills where opening and status <> 'cancelled'), 0)::bigint`).Scan(&invs, &bills); err != nil {
		return nil, command.DB(err)
	}
	legs = append(legs, leg{acctReceivables, invs, 0}, leg{acctPayable, 0, bills})
	var dr, cr int64
	for _, l := range legs {
		dr += l.debit
		cr += l.credit
	}
	diff := dr - cr // debits over credits: the adjustment is a credit
	if diff > 0 {
		legs = append(legs, leg{acctOpening, 0, diff})
	} else if diff < 0 {
		legs = append(legs, leg{acctOpening, -diff, 0})
	}
	if dr == 0 && cr == 0 {
		return nil, command.ErrInvalid("there is nothing to post: add balances or import opening invoices and bills")
	}
	// the day before go-live at noon IST: the same calendar date in every reporting time zone
	at := golive.AddDate(0, 0, -1).Add(12*time.Hour - 330*time.Minute)
	entry, err := postAt(c, "opening_balance", c.WorkspaceID(), "Opening balances as at "+in.GoLive, legs, &at)
	if err != nil {
		return nil, err
	}
	return map[string]any{"entry_id": entry, "difference_minor": diff, "invoices_minor": invs, "bills_minor": bills, "dated": at.Add(330 * time.Minute).Format("2006-01-02")}, nil
}

// ledger.opening_reverse {entry_id} posts the exact negation of an opening entry, once.
func openingReverse(c *command.Ctx, input json.RawMessage) (any, error) {
	var in struct {
		EntryID string `json:"entry_id"`
	}
	if err := decodeTo(input, &in); err != nil {
		return nil, err
	}
	var memo *string
	var at time.Time // the reversal carries the original's date, so a report never sees both
	if err := c.Tx.QueryRow(c.Context, "select memo, posted_at from billing.journal_entries where id = $1::uuid and source_type = 'opening_balance'", in.EntryID).Scan(&memo, &at); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, command.ErrNotFound("opening entry")
		}
		return nil, command.DB(err)
	}
	var done bool
	if err := c.Tx.QueryRow(c.Context, "select exists(select 1 from billing.journal_entries where source_type = 'opening_balance_reversal' and source_id = $1::uuid)", in.EntryID).Scan(&done); err != nil {
		return nil, command.DB(err)
	}
	if done {
		return nil, command.ErrConflict("this opening entry was already reversed")
	}
	rows, err := c.Tx.Query(c.Context, "select account_code, debit_minor, credit_minor from billing.journal_lines where entry_id = $1::uuid order by id", in.EntryID)
	if err != nil {
		return nil, command.DB(err)
	}
	var legs []leg
	for rows.Next() {
		var l leg
		if err := rows.Scan(&l.code, &l.credit, &l.debit); err != nil { // swapped: the negation
			rows.Close()
			return nil, command.DB(err)
		}
		legs = append(legs, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	entry, err := postAt(c, "opening_balance_reversal", in.EntryID, "Reversal of: "+deref(memo), legs, &at)
	if err != nil {
		return nil, err
	}
	return map[string]any{"entry_id": entry}, nil
}

// invoice.import_opening_summary {} -> {count, total_minor} of live opening invoices.
func openingSummary(c *command.Ctx, _ json.RawMessage) (any, error) {
	var n, total int64
	if err := c.Tx.QueryRow(c.Context, "select count(*), coalesce(sum(total_minor), 0)::bigint from billing.invoices where opening and status <> 'cancelled'").Scan(&n, &total); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"count": n, "total_minor": total}, nil
}
