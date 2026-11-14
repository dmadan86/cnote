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
		nc("bank.ignore", mi, ignore),
		nc("bank.unignore", mi, unignore),
		member(nc("bank.csv_preview", rd, csvPreview)),
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
	Matches     []ref    `json:"matches"` // a split: several movements that sum to the line
	Reason      *string  `json:"reason"`
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
	for _, m := range v.Matches {
		if !uuidRe.MatchString(m.ID) {
			return v, command.ErrInvalid("invalid id")
		}
	}
	if v.Mapping != nil && !validFormats[v.Mapping.DateFormat] {
		return v, command.ErrInvalid("date_format must be dmy, mdy or ymd")
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
	for i := range v.Matches {
		v.Matches[i].ID = strings.ToLower(v.Matches[i].ID)
	}
	v.ID, v.AccountID, v.LineID, v.MatchID = strings.ToLower(v.ID), strings.ToLower(v.AccountID), strings.ToLower(v.LineID), strings.ToLower(v.MatchID)
	return v, nil
}

func nilIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// view is one line as lineList shows it: matches, suggestions and combos included.
func view(c *command.Ctx, id string) (any, error) {
	out, err := lineList(c, json.RawMessage(fmt.Sprintf(`{"line_id":%q}`, id)))
	if err != nil {
		return nil, err
	}
	return out.([]line)[0], nil
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

// maxSplit bounds a split; maxPool bounds the candidates searched for exact-sum combinations per line (C(15,4) is about 1,400 subsets).
const (
	maxSplit = 10
	maxPool  = 15
)

// candidates returns, per unmatched line, the free movements in the right direction within +-3 days, best first. exact keeps those
// whose amount equals the line; otherwise those smaller than it (the parts of a split).
func candidates(c *command.Ctx, lineIDs []string, exact bool) (map[string][]suggestion, error) {
	op := "<"
	if exact {
		op = "="
	}
	rows, err := c.Tx.Query(c.Context, candidateCTE+`
		select l.id::text, c.kind, c.id::text, c.label, c.d::text, c.amt, (c.d - l.line_date), c.ref, l.narration || ' ' || coalesce(l.reference,'')
		from bank.lines l join cand c on c.amt `+op+` abs(l.amount_minor) and c.dir = sign(l.amount_minor) and c.d between l.line_date - $2::int and l.line_date + $2::int
		where l.id = any($1::uuid[]) and l.status = 'unmatched'
		  and not exists (select 1 from bank.line_matches m where m.match_kind = c.kind and m.match_id = c.id)`, lineIDs, window)
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

func suggest(c *command.Ctx, lineIDs []string) (map[string][]suggestion, error) {
	return candidates(c, lineIDs, true)
}

// combos finds up to three sets of 2 to 4 candidates whose amounts add up to target exactly, trying the best-scored maxPool
// candidates only, so the search stays small whatever the workspace holds. A set is ranked by the sum of its parts' scores.
func combos(target int64, pool []suggestion) [][]suggestion {
	if len(pool) > maxPool {
		pool = pool[:maxPool]
	}
	var out [][]suggestion
	var pick []suggestion
	var walk func(from int, left int64)
	walk = func(from int, left int64) {
		if left == 0 && len(pick) >= 2 {
			out = append(out, append([]suggestion(nil), pick...))
			return
		}
		if len(pick) == 4 {
			return
		}
		for i := from; i < len(pool); i++ {
			if pool[i].Amount <= left {
				pick = append(pick, pool[i])
				walk(i+1, left-pool[i].Amount)
				pick = pick[:len(pick)-1]
			}
		}
	}
	walk(0, target)
	score := func(x []suggestion) (t int) {
		for _, s := range x {
			t += s.Score
		}
		return t / len(x)
	}
	sort.SliceStable(out, func(i, j int) bool { return score(out[i]) > score(out[j]) })
	return out[:min(len(out), 3)]
}

func abs[T int | int64](v T) T {
	if v < 0 {
		return -v
	}
	return v
}

// ---------- lines ----------

type line struct {
	ID           string         `json:"id"`
	AccountID    string         `json:"account_id"`
	Date         string         `json:"date"`
	AmountMinor  int64          `json:"amount_minor"`
	Narration    string         `json:"narration"`
	Reference    *string        `json:"reference"`
	BalanceMinor *int64         `json:"balance_minor"`
	Status       string         `json:"status"`
	MatchKind    *string        `json:"match_kind"`
	MatchID      *string        `json:"match_id"`
	MatchLabel   *string        `json:"match_label"`
	Split        bool           `json:"split"`
	Matches      []matchOut     `json:"matches"`
	IgnoreReason *string        `json:"ignore_reason"`
	Suggestions  []suggestion   `json:"suggestions"`
	Combos       [][]suggestion `json:"combos"`
}

type ref struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type matchOut struct {
	ref
	Label  string `json:"label"`
	Amount int64  `json:"amount_minor"`
}

const lineCols = `l.id::text, l.account_id::text, l.line_date::text, l.amount_minor, l.narration, l.reference, l.balance_minor, l.status, l.match_kind, l.match_id::text, l.split, l.ignore_reason`

func scanLine(r pgx.Row) (line, error) {
	var l line
	err := r.Scan(&l.ID, &l.AccountID, &l.Date, &l.AmountMinor, &l.Narration, &l.Reference, &l.BalanceMinor, &l.Status, &l.MatchKind, &l.MatchID, &l.Split, &l.IgnoreReason)
	l.Suggestions, l.Combos, l.Matches = []suggestion{}, [][]suggestion{}, []matchOut{}
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
	if v.Status != nil && *v.Status != "unmatched" && *v.Status != "matched" && *v.Status != "ignored" {
		return nil, command.ErrInvalid("status must be unmatched, matched or ignored")
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
		  and ($4::date is null or l.line_date <= $4::date) and ($6::uuid is null or l.id = $6::uuid)
		order by l.line_date desc, l.seq desc limit $5`, acct, nilStr(v.Status), nilStr(v.From), nilStr(v.To), lim, nilIf(v.LineID))
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []line{}
	var open, matched []string
	byID := map[string]int{}
	for rows.Next() {
		l, err := scanLine(rows)
		if err != nil {
			return nil, command.DB(err)
		}
		if l.Status == "unmatched" {
			open = append(open, l.ID)
		} else if l.Status == "matched" {
			matched = append(matched, l.ID)
		}
		byID[l.ID] = len(out)
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
	if len(open) > 0 { // exact-sum combinations, only for lines that no single movement explains
		var none []string
		for _, id := range open {
			if len(sugg[id]) == 0 {
				none = append(none, id)
			}
		}
		if len(none) > 0 {
			parts, err := candidates(c, none, false)
			if err != nil {
				return nil, err
			}
			for _, id := range none {
				if cs := combos(abs(out[byID[id]].AmountMinor), parts[id]); len(cs) > 0 {
					out[byID[id]].Combos = cs
				}
			}
		}
	}
	if len(matched) > 0 {
		lr, err := c.Tx.Query(c.Context, candidateCTE+` select m.line_id::text, m.match_kind, m.match_id::text, m.amount_minor, coalesce(c.label, '')
			from bank.line_matches m left join cand c on c.kind = m.match_kind and c.id = m.match_id where m.line_id = any($1::uuid[]) order by m.created_at, m.id`, matched)
		if err != nil {
			return nil, command.DB(err)
		}
		for lr.Next() {
			var lid string
			var m matchOut
			if err := lr.Scan(&lid, &m.Kind, &m.ID, &m.Amount, &m.Label); err != nil {
				lr.Close()
				return nil, command.DB(err)
			}
			out[byID[lid]].Matches = append(out[byID[lid]].Matches, m)
		}
		lr.Close()
		if err := lr.Err(); err != nil {
			return nil, command.DB(err)
		}
	}
	for i := range out {
		if s := sugg[out[i].ID]; len(s) > 0 {
			out[i].Suggestions = s[:min(len(s), 3)]
		}
		if m := out[i].Matches; len(m) == 1 {
			out[i].MatchLabel = &m[0].Label
		} else if len(m) > 1 {
			lab := fmt.Sprintf("%d records: %s", len(m), strings.Join(labelsOf(m), "; "))
			out[i].MatchLabel = &lab
		}
	}
	return out, nil
}

func labelsOf(m []matchOut) []string {
	out := make([]string, len(m))
	for i, x := range m {
		out[i] = x.Label
	}
	return out
}

// ---------- match / unmatch ----------

// link matches a locked, unmatched line to one movement or, given several, to a split. The unique index on bank.line_matches
// refuses a movement already used by any line, so two concurrent matches of one payment cannot both succeed.
func link(c *command.Ctx, l line, items []matchOut) error {
	single := len(items) == 1
	var kind, id any
	if single {
		kind, id = items[0].Kind, items[0].ID
	}
	ct, err := c.Tx.Exec(c.Context, `update bank.lines set status = 'matched', split = $5, match_kind = $2, match_id = $3::uuid, matched_by = $4::uuid, matched_at = now()
		where id = $1::uuid and status = 'unmatched'`, l.ID, kind, id, c.Principal.UserID, !single)
	if isUnique(err) {
		return command.ErrConflict("that payment is already matched to another statement line")
	}
	if err != nil {
		return command.DB(err)
	}
	if ct.RowsAffected() == 0 {
		return command.ErrConflict("this line is already matched")
	}
	for _, m := range items {
		_, err := c.Tx.Exec(c.Context, `insert into bank.line_matches (workspace_id, line_id, match_kind, match_id, amount_minor) values ($1::uuid,$2::uuid,$3,$4::uuid,$5)`,
			c.WorkspaceID(), l.ID, m.Kind, m.ID, m.Amount)
		if isUnique(err) {
			return command.ErrConflict("that payment is already matched to another statement line")
		}
		if err != nil {
			return command.DB(err)
		}
	}
	_, err = c.Emit("bank.matched", nil, map[string]any{"line_id": l.ID, "matches": items, "amount_minor": l.AmountMinor})
	return err
}

// match: one movement (match_kind + match_id) or a split (matches: 2 to 10 of {kind, id}) whose amounts add up exactly to the line.
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
	want := v.Matches
	if len(want) == 0 {
		if v.MatchKind == "" || v.MatchID == "" {
			return nil, command.ErrInvalid("match_kind and match_id are required")
		}
		want = []ref{{v.MatchKind, v.MatchID}}
	}
	if len(want) > maxSplit {
		return nil, command.ErrInvalid(fmt.Sprintf("a split is at most %d records", maxSplit))
	}
	// every part must be free, same direction and inside the date window; an exact single is also one of the suggestions
	pool, err := candidates(c, []string{l.ID}, len(want) == 1)
	if err != nil {
		return nil, err
	}
	free := map[ref]suggestion{}
	for _, x := range pool[l.ID] {
		free[ref{x.Kind, x.ID}] = x
	}
	var items []matchOut
	var sum int64
	for _, w := range want {
		x, ok := free[w]
		if !ok {
			if len(want) == 1 {
				return nil, command.ErrConflict("that is not a free payment of the same amount within 3 days of this line")
			}
			return nil, command.ErrConflict("each part must be a free payment in the same direction within 3 days of this line, and no part may repeat")
		}
		delete(free, w) // a repeated part finds nothing the second time
		items = append(items, matchOut{ref: w, Label: x.Label, Amount: x.Amount})
		sum += x.Amount
	}
	if sum != abs(l.AmountMinor) {
		return nil, command.ErrConflict(fmt.Sprintf("the parts add up to %d paise but the line is %d", sum, abs(l.AmountMinor)))
	}
	if err := link(c, l, items); err != nil {
		return nil, err
	}
	return view(c, l.ID)
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
	if _, err := c.Tx.Exec(c.Context, "delete from bank.line_matches where line_id = $1::uuid", l.ID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Tx.Exec(c.Context, "update bank.lines set status = 'unmatched', split = false, match_kind = null, match_id = null, matched_by = null, matched_at = null where id = $1::uuid", l.ID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("bank.unmatched", nil, map[string]any{"line_id": l.ID}); err != nil {
		return nil, err
	}
	return view(c, l.ID)
}

// ignore sets an unmatched line aside with a reason (a bank charge not worth recording, an internal transfer). Reconciliation
// counts it as reconciled; nothing is posted. unignore puts it back.
func ignore(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	l, err := loadLine(c, v.LineID, true)
	if err != nil {
		return nil, err
	}
	if v.Reason == nil || strings.TrimSpace(*v.Reason) == "" || len(strings.TrimSpace(*v.Reason)) > 200 {
		return nil, command.ErrInvalid("reason is required (1 to 200 characters)")
	}
	if l.Status != "unmatched" {
		return nil, command.ErrConflict("only an unmatched line can be ignored")
	}
	reason := strings.TrimSpace(*v.Reason)
	if _, err := c.Tx.Exec(c.Context, "update bank.lines set status = 'ignored', ignore_reason = $2, matched_by = $3::uuid, matched_at = now() where id = $1::uuid", l.ID, reason, c.Principal.UserID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("bank.ignored", nil, map[string]any{"line_id": l.ID, "reason": reason, "amount_minor": l.AmountMinor}); err != nil {
		return nil, err
	}
	return view(c, l.ID)
}

func unignore(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	l, err := loadLine(c, v.LineID, true)
	if err != nil {
		return nil, err
	}
	if l.Status != "ignored" {
		return nil, command.ErrConflict("this line is not ignored")
	}
	if _, err := c.Tx.Exec(c.Context, "update bank.lines set status = 'unmatched', ignore_reason = null, matched_by = null, matched_at = null where id = $1::uuid", l.ID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("bank.unignored", nil, map[string]any{"line_id": l.ID}); err != nil {
		return nil, err
	}
	return view(c, l.ID)
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

func lineRef(l line) any {
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
	if err := link(c, l, []matchOut{{ref: ref{kind, made.ID}, Amount: abs(l.AmountMinor)}}); err != nil {
		return nil, err
	}
	ln, err := view(c, l.ID)
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
		"vendor_name": v.VendorName, "description": v.Description, "method": "bank", "reference": lineRef(l), "space_id": v.SpaceID})
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
		"amount_minor": l.AmountMinor, "received_at": l.Date, "method": "bank", "reference": lineRef(l), "description": v.Description, "space_id": v.SpaceID})
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
		Ignored, IgnoredCredits, IgnoredDebits         int64
	}
	if err := c.Tx.QueryRow(c.Context, `select count(*), count(*) filter (where status='matched'), count(*) filter (where status='unmatched'),
		coalesce(sum(amount_minor) filter (where amount_minor > 0),0), coalesce(-sum(amount_minor) filter (where amount_minor < 0),0),
		coalesce(sum(amount_minor) filter (where amount_minor > 0 and status='matched'),0), coalesce(-sum(amount_minor) filter (where amount_minor < 0 and status='matched'),0),
		coalesce(sum(amount_minor) filter (where amount_minor > 0 and status='unmatched'),0), coalesce(-sum(amount_minor) filter (where amount_minor < 0 and status='unmatched'),0),
		count(*) filter (where status='ignored'), coalesce(sum(amount_minor) filter (where amount_minor > 0 and status='ignored'),0), coalesce(-sum(amount_minor) filter (where amount_minor < 0 and status='ignored'),0)
		from bank.lines where account_id = $1::uuid and ($2::date is null or line_date >= $2::date) and ($3::date is null or line_date <= $3::date)`,
		a.ID, nilStr(v.From), nilStr(v.To)).Scan(&s.Lines, &s.Matched, &s.Unmatched, &s.Credits, &s.Debits, &s.MatchedCredits, &s.MatchedDebits, &s.UnmatchedCredits, &s.UnmatchedDebits, &s.Ignored, &s.IgnoredCredits, &s.IgnoredDebits); err != nil {
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
		"ignored": s.Ignored, "ignored_credits_minor": s.IgnoredCredits, "ignored_debits_minor": s.IgnoredDebits, "unmatched_credits_minor": s.UnmatchedCredits, "unmatched_debits_minor": s.UnmatchedDebits,
		"closing_balance_minor": closing, "closing_balance_on": closingOn, "reconciled": s.Lines > 0 && s.Unmatched == 0}, nil
}

// ---------- mapping editor ----------

// csvPreview shows what an uploaded CSV looks like to the importer without writing anything: the first raw rows, the header row
// found, the columns auto-detect picks, the columns that would be used (a given mapping, else the account's saved one, else
// detected), the first parsed lines, and why detection failed when it did. It is the "map columns" screen's one read.
func csvPreview(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	if v.CSV == nil || strings.TrimSpace(*v.CSV) == "" {
		return nil, command.ErrInvalid("csv is required")
	}
	given := v.Mapping
	if given == nil && v.AccountID != "" {
		a, err := loadAccount(c, v.AccountID)
		if err != nil {
			return nil, err
		}
		var saved mapping
		if len(a.Mapping) > 0 && json.Unmarshal(a.Mapping, &saved) == nil {
			given = &saved
		}
	}
	recs, err := readRecords(*v.CSV)
	if err != nil {
		return nil, command.ErrInvalid(err.Error())
	}
	out := map[string]any{"rows": recs[:min(len(recs), 12)], "total_rows": len(recs)}
	var detected mapping
	for i := 0; i < len(recs) && i < 40; i++ {
		if d := detect(recs[i]); d.Date != "" && (d.Debit != "" || d.Credit != "" || d.Amount != "") {
			detected = d
			break
		}
	}
	out["detected"] = detected
	res, perr := parseStatement(*v.CSV, given)
	out["ok"] = perr == nil
	if perr != nil {
		out["reason"] = perr.Error()
		// offer the first row with several filled cells as the header to pick columns from
		for i := 0; i < len(recs) && i < 40; i++ {
			n := 0
			for _, x := range recs[i] {
				if strings.TrimSpace(x) != "" {
					n++
				}
			}
			if n >= 2 {
				out["headers"], out["header_row"] = recs[i], i+1
				break
			}
		}
		return out, nil
	}
	sample := []map[string]any{}
	for _, l := range res.Lines[:min(len(res.Lines), 5)] {
		sample = append(sample, map[string]any{"row": l.Row, "date": l.Date.Format("2006-01-02"), "amount_minor": l.Amount, "narration": l.Narration})
	}
	errs := res.Errors
	if errs == nil {
		errs = []string{}
	}
	out["headers"], out["header_row"], out["used"], out["source"] = recs[res.Header-1], res.Header, res.Used, res.Source
	out["lines"], out["skipped"], out["errors"], out["sample"] = len(res.Lines), res.Skipped, errs, sample
	return out, nil
}
