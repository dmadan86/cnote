// Package bank: bank accounts, statement CSV import, and matching statement lines to recorded money movements
// (invoice payments, receipt vouchers, bill payments, expenses). Go-only. Matching links, it never posts: the ledger
// already has each movement. An unmatched line can be recorded as a new direct expense (debit) or advance receipt (credit),
// which does post, and is linked at once. No bank feeds or aggregator: statements are uploaded by hand.
package bank

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/expenses"
	"github.com/cnote/api/internal/modules/receipts"
)

const (
	maxCSV   = 2 << 20
	maxLines = 5000
	window   = 3 // days either side of the statement date
)

func Commands() []command.Command {
	mi, rd := command.GroupManageInvoices, command.GroupRead
	confirm := func(c command.Command) command.Command { c.Confirm = true; return c }
	member := func(c command.Command) command.Command { c.MinRole = command.Member; return c } // reads too: guests never see bank data
	nc := command.NewCommand
	return []command.Command{
		nc("bank.account_create", mi, accountCreate),
		nc("bank.account_update", mi, accountUpdate),
		member(nc("bank.account_list", rd, accountList)),
		nc("bank.import", mi, importCSV),
		member(nc("bank.line_list", rd, lineList)),
		nc("bank.match", mi, match),
		nc("bank.unmatch", mi, unmatch),
		confirm(nc("bank.record_expense", mi, recordExpense)),
		confirm(nc("bank.record_receipt", mi, recordReceipt)),
		member(nc("bank.summary", rd, summary)),
	}
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type in struct {
	ID          string   `json:"id"`
	AccountID   string   `json:"account_id"`
	LineID      string   `json:"line_id"`
	MatchKind   string   `json:"match_kind"`
	MatchID     string   `json:"match_id"`
	Name        *string  `json:"name"`
	AccountHint *string  `json:"account_hint"`
	Active      *bool    `json:"active"`
	Mapping     *mapping `json:"mapping"`
	CSV         *string  `json:"csv"`
	Status      *string  `json:"status"`
	From        *string  `json:"from"`
	To          *string  `json:"to"`
	Limit       *int64   `json:"limit"`
	// record_expense / record_receipt
	SpaceID     string  `json:"space_id"`
	Category    *string `json:"category"`
	VendorName  *string `json:"vendor_name"`
	Description *string `json:"description"`
	ContactID   *string `json:"contact_id"`
	SupplyType  *string `json:"supply_type"`
	GSTRateBP   *int64  `json:"gst_rate_bp"`
}

func decode(raw json.RawMessage) (in, error) {
	var v in
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, command.ErrInvalid("invalid input")
	}
	for _, id := range []string{v.ID, v.AccountID, v.LineID, v.MatchID, v.SpaceID} {
		if id != "" && !uuidRe.MatchString(id) {
			return v, command.ErrInvalid("invalid id")
		}
	}
	if v.ContactID != nil && !uuidRe.MatchString(*v.ContactID) {
		return v, command.ErrInvalid("invalid id")
	}
	for _, d := range []*string{v.From, v.To} {
		if d != nil {
			if _, err := time.Parse("2006-01-02", *d); err != nil {
				return v, command.ErrInvalid("dates must be YYYY-MM-DD")
			}
		}
	}
	if v.Name != nil {
		n := strings.TrimSpace(*v.Name)
		v.Name = &n
		if n == "" || len(n) > 120 {
			return v, command.ErrInvalid("name must be 1 to 120 characters")
		}
	}
	if v.AccountHint != nil && len(*v.AccountHint) > 20 {
		return v, command.ErrInvalid("account_hint is too long; give the last digits only")
	}
	if v.CSV != nil && len(*v.CSV) > maxCSV {
		return v, command.ErrInvalid("csv is larger than 2 MB; import one period at a time")
	}
	v.ID, v.AccountID, v.LineID, v.MatchID = strings.ToLower(v.ID), strings.ToLower(v.AccountID), strings.ToLower(v.LineID), strings.ToLower(v.MatchID)
	return v, nil
}

func nilStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// ---------- accounts ----------

type account struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	AccountHint *string         `json:"account_hint"`
	Mapping     json.RawMessage `json:"csv_mapping"`
	Active      bool            `json:"active"`
	Unmatched   int64           `json:"unmatched_lines"`
	Lines       int64           `json:"lines"`
	CreatedAt   command.Ts      `json:"created_at"`
}

const accountCols = `a.id::text, a.name, a.account_hint, a.csv_mapping, a.active, a.created_at,
	(select count(*) from bank.lines l where l.account_id = a.id and l.status = 'unmatched'), (select count(*) from bank.lines l where l.account_id = a.id)`

func scanAccount(r pgx.Row) (account, error) {
	var a account
	var created time.Time
	err := r.Scan(&a.ID, &a.Name, &a.AccountHint, &a.Mapping, &a.Active, &created, &a.Unmatched, &a.Lines)
	a.CreatedAt = command.Ts(created)
	return a, err
}

func loadAccount(c *command.Ctx, id string) (account, error) {
	if id == "" {
		return account{}, command.ErrInvalid("account_id is required")
	}
	a, err := scanAccount(c.Tx.QueryRow(c.Context, "select "+accountCols+" from bank.accounts a where a.id = $1::uuid", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return a, command.ErrNotFound("bank account")
	}
	return a, command.DB(err)
}

func isUnique(err error) bool {
	var pe interface{ SQLState() string }
	return errors.As(err, &pe) && pe.SQLState() == "23505"
}

func accountCreate(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	if v.Name == nil {
		return nil, command.ErrInvalid("name is required")
	}
	var id string
	err = c.Tx.QueryRow(c.Context, "insert into bank.accounts (workspace_id, name, account_hint, csv_mapping) values ($1::uuid,$2,$3,$4) returning id::text",
		c.WorkspaceID(), *v.Name, nilStr(v.AccountHint), mappingJSON(v.Mapping)).Scan(&id)
	if isUnique(err) {
		return nil, command.ErrConflict("a bank account with this name exists")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	return loadAccount(c, id)
}

func mappingJSON(m *mapping) any {
	if m == nil || *m == (mapping{}) {
		return nil
	}
	b, _ := json.Marshal(m)
	return b
}

func accountUpdate(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	if _, err := loadAccount(c, v.ID); err != nil {
		return nil, err
	}
	// an explicit all-empty mapping clears it, so the next import detects afresh
	var m any
	if v.Mapping != nil {
		m = mappingJSON(v.Mapping)
	}
	_, err = c.Tx.Exec(c.Context, `update bank.accounts set name = coalesce($2, name), account_hint = coalesce($3, account_hint), active = coalesce($4, active),
		csv_mapping = case when $5 then $6::jsonb else csv_mapping end where id = $1::uuid`,
		v.ID, nilStr(v.Name), nilStr(v.AccountHint), v.Active, v.Mapping != nil, m)
	if isUnique(err) {
		return nil, command.ErrConflict("a bank account with this name exists")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	return loadAccount(c, v.ID)
}

func accountList(c *command.Ctx, input json.RawMessage) (any, error) {
	rows, err := c.Tx.Query(c.Context, "select "+accountCols+" from bank.accounts a order by a.active desc, a.name")
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, command.DB(err)
		}
		out = append(out, a)
	}
	return out, command.DB(rows.Err())
}

// ---------- import ----------

func importCSV(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	a, err := loadAccount(c, v.AccountID)
	if err != nil {
		return nil, err
	}
	if !a.Active {
		return nil, command.ErrConflict("this bank account is inactive")
	}
	if v.CSV == nil || strings.TrimSpace(*v.CSV) == "" {
		return nil, command.ErrInvalid("csv is required")
	}
	given := v.Mapping
	if given == nil && len(a.Mapping) > 0 {
		var saved mapping
		if json.Unmarshal(a.Mapping, &saved) == nil {
			given = &saved
		}
	}
	res, err := parseStatement(*v.CSV, given)
	if err != nil {
		return nil, command.ErrInvalid(err.Error())
	}
	if res.Errors == nil {
		res.Errors = []string{}
	}
	if len(res.Lines) > maxLines {
		return nil, command.ErrInvalid(fmt.Sprintf("more than %d lines; import one period at a time", maxLines))
	}
	today := time.Now().In(time.FixedZone("IST", 5*3600+1800)).AddDate(0, 0, 1)
	batch := &pgx.Batch{}
	for _, l := range res.Lines {
		if l.Date.After(today) {
			return nil, command.ErrInvalid(fmt.Sprintf("row %d: date %s is in the future; check the day/month order", l.Row, l.Date.Format("2006-01-02")))
		}
		batch.Queue(`insert into bank.lines (workspace_id, account_id, line_date, amount_minor, narration, reference, balance_minor, hash)
			values ($1::uuid,$2::uuid,$3::date,$4,$5,$6,$7,$8) on conflict (workspace_id, account_id, hash) do nothing`,
			c.WorkspaceID(), a.ID, l.Date.Format("2006-01-02"), l.Amount, l.Narration, nilStr(&l.Reference), l.Balance, l.Hash)
	}
	br := c.Tx.SendBatch(c.Context, batch)
	imported := 0
	for range res.Lines {
		ct, err := br.Exec()
		if err != nil {
			_ = br.Close()
			return nil, command.DB(err)
		}
		imported += int(ct.RowsAffected())
	}
	if err := br.Close(); err != nil {
		return nil, command.DB(err)
	}
	if imported > 0 || v.Mapping != nil {
		if _, err := c.Tx.Exec(c.Context, "update bank.accounts set csv_mapping = $2::jsonb where id = $1::uuid", a.ID, mappingJSON(&res.Used)); err != nil {
			return nil, command.DB(err)
		}
	}
	if _, err := c.Emit("bank.imported", nil, map[string]any{"account_id": a.ID, "imported": imported, "duplicates": len(res.Lines) - imported}); err != nil {
		return nil, err
	}
	return map[string]any{"imported": imported, "duplicates": len(res.Lines) - imported, "skipped": res.Skipped, "errors": res.Errors, "mapping": res.Used}, nil
}

// ---------- candidates ----------

// The recorded money movements a statement line can be matched to. dir is +1 money in, -1 money out. Cash is never on a statement.
const candidateCTE = `with cand as (
  select 'payment' kind, p.id, p.amount_minor amt, 1 dir, (p.received_at at time zone 'Asia/Kolkata')::date d, coalesce(p.reference,'') ref,
    'Payment on invoice ' || coalesce(i.number, '') label
    from billing.payments p join billing.invoices i on i.note_id = p.invoice_id where lower(p.method) <> 'cash'
  union all
  select 'receipt', r.note_id, r.amount_minor, case r.kind when 'receipt' then 1 else -1 end, r.received_at, coalesce(r.reference,''),
    (case r.kind when 'receipt' then 'Receipt voucher ' else 'Refund voucher ' end) || r.number
    from billing.receipts r where lower(r.method) <> 'cash'
  union all
  select 'bill_payment', pp.id, pp.amount_minor, -1, (pp.paid_at at time zone 'Asia/Kolkata')::date, coalesce(pp.reference,''), 'Payment of bill ' || b.vendor_bill_number
    from billing.purchase_payments pp join billing.purchase_bills b on b.note_id = pp.bill_id where lower(pp.method) <> 'cash'
  union all
  select 'expense', e.note_id, e.amount_minor, -1, e.spent_on, coalesce(e.reference,''), n.title
    from billing.expenses e join core.notes n on n.id = e.note_id where e.kind = 'direct' and n.deleted_at is null and lower(e.method) <> 'cash'
  union all
  select 'expense', e.note_id, e.amount_minor, -1, (e.reimbursed_at at time zone 'Asia/Kolkata')::date, coalesce(e.reimbursed_reference,''), n.title || ' (reimbursed)'
    from billing.expenses e join core.notes n on n.id = e.note_id where e.status = 'reimbursed' and n.deleted_at is null and lower(coalesce(e.reimbursed_method,'bank')) <> 'cash'
)`

type suggestion struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Label    string `json:"label"`
	Date     string `json:"date"`
	Amount   int64  `json:"amount_minor"`
	DayDiff  int    `json:"day_diff"`
	RefMatch bool   `json:"reference_match"`
	Score    int    `json:"score"`
}

// suggest returns, per unmatched line, the free candidates with the exact amount in the right direction within +-3 days, best first.
func suggest(c *command.Ctx, lineIDs []string) (map[string][]suggestion, error) {
	rows, err := c.Tx.Query(c.Context, candidateCTE+`
		select l.id::text, c.kind, c.id::text, c.label, c.d::text, c.amt, (c.d - l.line_date), c.ref, l.narration || ' ' || coalesce(l.reference,'')
		from bank.lines l join cand c on c.amt = abs(l.amount_minor) and c.dir = sign(l.amount_minor) and c.d between l.line_date - $2::int and l.line_date + $2::int
		where l.id = any($1::uuid[]) and l.status = 'unmatched'
		  and not exists (select 1 from bank.lines m where m.match_kind = c.kind and m.match_id = c.id)`, lineIDs, window)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := map[string][]suggestion{}
	for rows.Next() {
		var lid, ref, text string
		var s suggestion
		if err := rows.Scan(&lid, &s.Kind, &s.ID, &s.Label, &s.Date, &s.Amount, &s.DayDiff, &ref, &text); err != nil {
			return nil, command.DB(err)
		}
		s.RefMatch = len(ref) >= 6 && strings.Contains(strings.ToUpper(text), strings.ToUpper(ref)) // a UPI / UTR number in the narration
		s.Score = 100 - 10*abs(s.DayDiff)
		if s.RefMatch {
			s.Score += 50
		}
		out[lid] = append(out[lid], s)
	}
	for _, s := range out {
		sort.SliceStable(s, func(i, j int) bool { return s[i].Score > s[j].Score })
	}
	return out, command.DB(rows.Err())
}

func abs[T int | int64](v T) T {
	if v < 0 {
		return -v
	}
	return v
}

// ---------- lines ----------

type line struct {
	ID           string       `json:"id"`
	AccountID    string       `json:"account_id"`
	Date         string       `json:"date"`
	AmountMinor  int64        `json:"amount_minor"`
	Narration    string       `json:"narration"`
	Reference    *string      `json:"reference"`
	BalanceMinor *int64       `json:"balance_minor"`
	Status       string       `json:"status"`
	MatchKind    *string      `json:"match_kind"`
	MatchID      *string      `json:"match_id"`
	MatchLabel   *string      `json:"match_label"`
	Suggestions  []suggestion `json:"suggestions"`
}

const lineCols = `l.id::text, l.account_id::text, l.line_date::text, l.amount_minor, l.narration, l.reference, l.balance_minor, l.status, l.match_kind, l.match_id::text`

func scanLine(r pgx.Row) (line, error) {
	var l line
	err := r.Scan(&l.ID, &l.AccountID, &l.Date, &l.AmountMinor, &l.Narration, &l.Reference, &l.BalanceMinor, &l.Status, &l.MatchKind, &l.MatchID)
	l.Suggestions = []suggestion{}
	return l, err
}

func loadLine(c *command.Ctx, id string, lock bool) (line, error) {
	if id == "" {
		return line{}, command.ErrInvalid("line_id is required")
	}
	q := "select " + lineCols + " from bank.lines l where l.id = $1::uuid"
	if lock {
		q += " for update"
	}
	l, err := scanLine(c.Tx.QueryRow(c.Context, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return l, command.ErrNotFound("bank line")
	}
	return l, command.DB(err)
}

// lineList: filters account_id, status (unmatched|matched), from/to on the statement date. Unmatched lines carry their top suggestions.
func lineList(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	if v.Status != nil && *v.Status != "unmatched" && *v.Status != "matched" {
		return nil, command.ErrInvalid("status must be unmatched or matched")
	}
	lim := int64(200)
	if v.Limit != nil {
		lim = min(max(*v.Limit, 1), 1000)
	}
	acct := any(nil)
	if v.AccountID != "" {
		acct = v.AccountID
	}
	rows, err := c.Tx.Query(c.Context, `select `+lineCols+` from bank.lines l
		where ($1::uuid is null or l.account_id = $1::uuid) and ($2::text is null or l.status = $2) and ($3::date is null or l.line_date >= $3::date)
		  and ($4::date is null or l.line_date <= $4::date)
		order by l.line_date desc, l.seq desc limit $5`, acct, nilStr(v.Status), nilStr(v.From), nilStr(v.To), lim)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []line{}
	var open, matched []string
	for rows.Next() {
		l, err := scanLine(rows)
		if err != nil {
			return nil, command.DB(err)
		}
		if l.Status == "unmatched" {
			open = append(open, l.ID)
		} else {
			matched = append(matched, *l.MatchID)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	rows.Close()
	sugg := map[string][]suggestion{}
	if len(open) > 0 {
		if sugg, err = suggest(c, open); err != nil {
			return nil, err
		}
	}
	labels := map[string]string{}
	if len(matched) > 0 {
		lr, err := c.Tx.Query(c.Context, candidateCTE+" select kind || ':' || id::text, label from cand where id = any($1::uuid[])", matched)
		if err != nil {
			return nil, command.DB(err)
		}
		for lr.Next() {
			var k, lab string
			if err := lr.Scan(&k, &lab); err != nil {
				lr.Close()
				return nil, command.DB(err)
			}
			labels[k] = lab
		}
		lr.Close()
	}
	for i := range out {
		if s := sugg[out[i].ID]; len(s) > 0 {
			out[i].Suggestions = s[:min(len(s), 3)]
		}
		if out[i].MatchID != nil {
			if lab, ok := labels[*out[i].MatchKind+":"+*out[i].MatchID]; ok {
				out[i].MatchLabel = &lab
			}
		}
	}
	return out, nil
}

// ---------- match / unmatch ----------

func link(c *command.Ctx, l line, kind, id string) error {
	ct, err := c.Tx.Exec(c.Context, `update bank.lines set status = 'matched', match_kind = $2, match_id = $3::uuid, matched_by = $4::uuid, matched_at = now()
		where id = $1::uuid and status = 'unmatched'`, l.ID, kind, id, c.Principal.UserID)
	if isUnique(err) {
		return command.ErrConflict("that payment is already matched to another statement line")
	}
	if err != nil {
		return command.DB(err)
	}
	if ct.RowsAffected() == 0 {
		return command.ErrConflict("this line is already matched")
	}
	_, err = c.Emit("bank.matched", nil, map[string]any{"line_id": l.ID, "kind": kind, "match_id": id, "amount_minor": l.AmountMinor})
	return err
}

func match(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	l, err := loadLine(c, v.LineID, true)
	if err != nil {
		return nil, err
	}
	if l.Status != "unmatched" {
		return nil, command.ErrConflict("this line is already matched")
	}
	if v.MatchKind == "" || v.MatchID == "" {
		return nil, command.ErrInvalid("match_kind and match_id are required")
	}
	s, err := suggest(c, []string{l.ID})
	if err != nil {
		return nil, err
	}
	ok := false
	for _, x := range s[l.ID] {
		ok = ok || (x.Kind == v.MatchKind && x.ID == v.MatchID)
	}
	if !ok {
		return nil, command.ErrConflict("that is not a free payment of the same amount within 3 days of this line")
	}
	if err := link(c, l, v.MatchKind, v.MatchID); err != nil {
		return nil, err
	}
	return loadLine(c, l.ID, false)
}

func unmatch(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	l, err := loadLine(c, v.LineID, true)
	if err != nil {
		return nil, err
	}
	if l.Status != "matched" {
		return nil, command.ErrConflict("this line is not matched")
	}
	if _, err := c.Tx.Exec(c.Context, "update bank.lines set status = 'unmatched', match_kind = null, match_id = null, matched_by = null, matched_at = null where id = $1::uuid", l.ID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("bank.unmatched", nil, map[string]any{"line_id": l.ID, "kind": l.MatchKind, "match_id": l.MatchID}); err != nil {
		return nil, err
	}
	return loadLine(c, l.ID, false)
}

// ---------- record an unmatched line ----------

func unmatchedLine(c *command.Ctx, id string, debit bool) (line, error) {
	l, err := loadLine(c, id, true)
	if err != nil {
		return l, err
	}
	if l.Status != "unmatched" {
		return l, command.ErrConflict("this line is already matched")
	}
	if debit != (l.AmountMinor < 0) {
		if debit {
			return l, command.ErrInvalid("only a debit can be recorded as an expense; record a credit as a receipt")
		}
		return l, command.ErrInvalid("only a credit can be recorded as a receipt; record a debit as an expense")
	}
	return l, nil
}

func ref(l line) any {
	if l.Reference != nil && strings.TrimSpace(*l.Reference) != "" {
		return *l.Reference
	}
	return nil
}

// recordAndLink runs the other module's handler as this caller (the confirmation already happened here), then links its new row.
func recordAndLink(c *command.Ctx, l line, kind string, h command.Handler, body map[string]any) (any, error) {
	raw, err := command.Marshal(body)
	if err != nil {
		return nil, command.ErrInvalid(err.Error())
	}
	out, err := h(c, raw)
	if err != nil {
		return nil, err
	}
	b, err := command.Marshal(out)
	if err != nil {
		return nil, command.ErrInvalid(err.Error())
	}
	var made struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &made); err != nil || made.ID == "" {
		return nil, command.ErrInvalid("recorded row has no id")
	}
	if err := link(c, l, kind, made.ID); err != nil {
		return nil, err
	}
	ln, err := loadLine(c, l.ID, false)
	return map[string]any{"line": ln, "created": json.RawMessage(b)}, err
}

func recordExpense(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	l, err := unmatchedLine(c, v.LineID, true)
	if err != nil {
		return nil, err
	}
	if v.Category == nil || strings.TrimSpace(*v.Category) == "" {
		return nil, command.ErrInvalid("category is required")
	}
	return recordAndLink(c, l, "expense", expenses.RecordDirect, map[string]any{"spent_on": l.Date, "category": *v.Category, "amount_minor": -l.AmountMinor,
		"vendor_name": v.VendorName, "description": v.Description, "method": "bank", "reference": ref(l), "space_id": v.SpaceID})
}

func recordReceipt(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	l, err := unmatchedLine(c, v.LineID, false)
	if err != nil {
		return nil, err
	}
	if v.ContactID == nil || v.SupplyType == nil {
		return nil, command.ErrInvalid("contact_id and supply_type are required")
	}
	return recordAndLink(c, l, "receipt", receipts.Create, map[string]any{"contact_id": *v.ContactID, "supply_type": *v.SupplyType, "gst_rate_bp": v.GSTRateBP,
		"amount_minor": l.AmountMinor, "received_at": l.Date, "method": "bank", "reference": ref(l), "description": v.Description, "space_id": v.SpaceID})
}

// ---------- summary ----------

// summary: statement totals for an account and period, what is still unmatched, and the closing balance the statement shows.
func summary(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	a, err := loadAccount(c, v.AccountID)
	if err != nil {
		return nil, err
	}
	var s struct {
		Lines, Matched, Unmatched                      int64
		Credits, Debits, MatchedCredits, MatchedDebits int64
		UnmatchedCredits, UnmatchedDebits              int64
	}
	if err := c.Tx.QueryRow(c.Context, `select count(*), count(*) filter (where status='matched'), count(*) filter (where status='unmatched'),
		coalesce(sum(amount_minor) filter (where amount_minor > 0),0), coalesce(-sum(amount_minor) filter (where amount_minor < 0),0),
		coalesce(sum(amount_minor) filter (where amount_minor > 0 and status='matched'),0), coalesce(-sum(amount_minor) filter (where amount_minor < 0 and status='matched'),0),
		coalesce(sum(amount_minor) filter (where amount_minor > 0 and status='unmatched'),0), coalesce(-sum(amount_minor) filter (where amount_minor < 0 and status='unmatched'),0)
		from bank.lines where account_id = $1::uuid and ($2::date is null or line_date >= $2::date) and ($3::date is null or line_date <= $3::date)`,
		a.ID, nilStr(v.From), nilStr(v.To)).Scan(&s.Lines, &s.Matched, &s.Unmatched, &s.Credits, &s.Debits, &s.MatchedCredits, &s.MatchedDebits, &s.UnmatchedCredits, &s.UnmatchedDebits); err != nil {
		return nil, command.DB(err)
	}
	var closing *int64
	var closingOn *string
	err = c.Tx.QueryRow(c.Context, `select balance_minor, line_date::text from bank.lines where account_id = $1::uuid and balance_minor is not null
		and ($2::date is null or line_date <= $2::date) order by line_date desc, seq desc limit 1`, a.ID, nilStr(v.To)).Scan(&closing, &closingOn)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, command.DB(err)
	}
	return map[string]any{"account_id": a.ID, "name": a.Name, "from": v.From, "to": v.To, "lines": s.Lines, "matched": s.Matched, "unmatched": s.Unmatched,
		"credits_minor": s.Credits, "debits_minor": s.Debits, "matched_credits_minor": s.MatchedCredits, "matched_debits_minor": s.MatchedDebits,
		"unmatched_credits_minor": s.UnmatchedCredits, "unmatched_debits_minor": s.UnmatchedDebits,
		"closing_balance_minor": closing, "closing_balance_on": closingOn, "reconciled": s.Lines > 0 && s.Unmatched == 0}, nil
}
