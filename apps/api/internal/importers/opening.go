package importers

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
)

// The opening-balances page: a draft (go-live date and an account grid) in core.opening_balances, and one posting.
// Posting writes ONE journal entry through billing's ledger.opening_post: the grid, Dr 1100 for opening invoices,
// Cr 2100 for opening bills and the balance to 3900. Reversing posts its exact negation once.

type OpenLine struct {
	AccountCode string `json:"account_code"`
	DrMinor     int64  `json:"dr_minor"`
	CrMinor     int64  `json:"cr_minor"`
}

type openingRow struct {
	GoLive     string
	Status     string
	Lines      []OpenLine
	EntryID    *string
	DiffMinor  *int64
	PostedAt   *time.Time
	ReversalID *string
	hasRow     bool
}

func defaultGoLive(c *command.Ctx) (string, error) {
	var m int
	if err := c.Tx.QueryRow(c.Context, "select coalesce(nullif(settings->>'fy_start_month', '')::int, 4) from core.workspaces where id = $1::uuid", c.WorkspaceID()).Scan(&m); err != nil || m < 1 || m > 12 {
		m = 4
	}
	now := time.Now().UTC().Add(330 * time.Minute)
	y := now.Year()
	if int(now.Month()) < m {
		y--
	}
	return fmt.Sprintf("%04d-%02d-01", y, m), nil
}

func loadOpening(c *command.Ctx, lock bool) (openingRow, error) {
	q := "select go_live::text, status, lines, entry_id::text, difference_minor, posted_at, reversal_entry_id::text from core.opening_balances"
	if lock {
		q += " for update"
	}
	var o openingRow
	var lines []byte
	err := c.Tx.QueryRow(c.Context, q).Scan(&o.GoLive, &o.Status, &lines, &o.EntryID, &o.DiffMinor, &o.PostedAt, &o.ReversalID)
	if errors.Is(err, pgx.ErrNoRows) {
		g, err := defaultGoLive(c)
		return openingRow{GoLive: g, Status: "draft", Lines: []OpenLine{}}, err
	}
	if err != nil {
		return o, command.DB(err)
	}
	o.hasRow = true
	_ = json.Unmarshal(lines, &o.Lines)
	if o.Lines == nil {
		o.Lines = []OpenLine{}
	}
	return o, nil
}

func openingStatus(c *command.Ctx) (string, error) {
	o, err := loadOpening(c, false)
	return o.Status, err
}

func saveDraft(c *command.Ctx, goLive string, lines []OpenLine) error {
	b, _ := json.Marshal(lines)
	_, err := c.Tx.Exec(c.Context,
		`insert into core.opening_balances (workspace_id, go_live, lines, status, updated_by) values ($1::uuid,$2::date,$3,'draft',$4::uuid)
		 on conflict (workspace_id) do update set go_live = $2::date, lines = $3, status = 'draft', entry_id = null, reversal_entry_id = null,
		   difference_minor = null, posted_at = null, posted_by = null, updated_by = $4::uuid, updated_at = now()`,
		c.WorkspaceID(), goLive, b, c.Principal.UserID)
	return command.DB(err)
}

// editable loads the draft for a change: a posted page must be reversed first.
func editable(c *command.Ctx) (openingRow, error) {
	o, err := loadOpening(c, true)
	if err != nil {
		return o, err
	}
	if o.Status == "posted" {
		return o, command.ErrConflict("opening balances are posted: reverse them to change them")
	}
	return o, nil
}

// draftAddLine adds a balance from an imported ledger to the draft (the same account twice nets out).
func draftAddLine(c *command.Ctx, code string, dr, cr int64) error {
	o, err := editable(c)
	if err != nil {
		return err
	}
	for i, l := range o.Lines {
		if l.AccountCode == code {
			net := l.DrMinor - l.CrMinor + dr - cr
			o.Lines[i] = OpenLine{code, max(net, 0), max(-net, 0)}
			return saveDraft(c, o.GoLive, o.Lines)
		}
	}
	return saveDraft(c, o.GoLive, append(o.Lines, OpenLine{code, dr, cr}))
}

// draftSubLine takes back exactly what draftAddLine put in, so a manual part of the same account stays.
func draftSubLine(c *command.Ctx, code string, dr, cr int64) error {
	o, err := editable(c)
	if err != nil {
		return err
	}
	kept := []OpenLine{}
	for _, l := range o.Lines {
		if l.AccountCode == code {
			net := l.DrMinor - l.CrMinor - dr + cr
			if net == 0 {
				continue
			}
			l = OpenLine{code, max(net, 0), max(-net, 0)}
		}
		kept = append(kept, l)
	}
	return saveDraft(c, o.GoLive, kept)
}

type summary struct {
	Count      int64 `json:"count"`
	TotalMinor int64 `json:"total_minor"`
}

func OpeningGet(c *command.Ctx) (any, error) {
	o, err := loadOpening(c, false)
	if err != nil {
		return nil, err
	}
	var accts []struct{ Code, Name, Kind string }
	if err := callJSON(c, "ledger.account_list", map[string]any{}, &accts); err != nil {
		return nil, err
	}
	names := map[string]string{}
	pick := []map[string]string{}
	for _, a := range accts {
		names[a.Code] = a.Name
		if a.Code != "1100" && a.Code != "2100" && a.Code != "3900" {
			pick = append(pick, map[string]string{"code": a.Code, "name": a.Name, "kind": a.Kind})
		}
	}
	var inv, bill summary
	if err := callJSON(c, "invoice.import_opening_summary", map[string]any{}, &inv); err != nil {
		return nil, err
	}
	if err := callJSON(c, "bill.import_opening_summary", map[string]any{}, &bill); err != nil {
		return nil, err
	}
	lines := []map[string]any{}
	dr, cr := inv.TotalMinor, bill.TotalMinor
	for _, l := range o.Lines {
		lines = append(lines, map[string]any{"account_code": l.AccountCode, "name": names[l.AccountCode], "dr_minor": l.DrMinor, "cr_minor": l.CrMinor})
		dr, cr = dr+l.DrMinor, cr+l.CrMinor
	}
	diff := dr - cr // debits over credits: 3900 takes the credit
	if o.DiffMinor != nil && o.Status != "draft" {
		diff = *o.DiffMinor
	}
	var posted any
	if o.PostedAt != nil {
		posted = o.PostedAt.UTC().Format(time.RFC3339)
	}
	return map[string]any{"go_live": o.GoLive, "status": o.Status, "lines": lines, "invoices": inv, "bills": bill,
		"difference_minor": diff, "total_dr_minor": dr, "total_cr_minor": cr, "posted_at": posted, "accounts": pick}, nil
}

var (
	dayRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	codeRe = regexp.MustCompile(`^[0-9]{4}$`)
)

func parseDay(s string) (string, error) {
	if !dayRe.MatchString(s) {
		return "", command.ErrInvalid("go_live must be a date, YYYY-MM-DD")
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return "", command.ErrInvalid("go_live is not a real date")
	}
	return s, nil
}

func OpeningSave(c *command.Ctx, goLive string, lines []OpenLine) (any, error) {
	cur, err := editable(c)
	if err != nil {
		return nil, err
	}
	if goLive == "" {
		goLive = cur.GoLive
	}
	if _, err := parseDay(goLive); err != nil {
		return nil, err
	}
	var accts []struct{ Code string }
	if err := callJSON(c, "ledger.account_list", map[string]any{}, &accts); err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, a := range accts {
		known[a.Code] = true
	}
	seen := map[string]bool{}
	clean := []OpenLine{}
	for _, l := range lines {
		switch {
		case !codeRe.MatchString(l.AccountCode):
			return nil, command.ErrInvalid("account_code must be a 4 digit account code")
		case l.AccountCode == "1100" || l.AccountCode == "2100":
			return nil, command.ErrInvalid("customer and vendor balances come from the opening invoices and bills: leave 1100 and 2100 out")
		case l.AccountCode == "3900":
			return nil, command.ErrInvalid("3900 balances the entry and is filled in for you")
		case !known[l.AccountCode]:
			return nil, command.ErrInvalid("unknown account " + l.AccountCode)
		case seen[l.AccountCode]:
			return nil, command.ErrInvalid("account " + l.AccountCode + " appears twice")
		case l.DrMinor < 0 || l.CrMinor < 0 || (l.DrMinor > 0 && l.CrMinor > 0) || l.DrMinor > 1_000_000_000_000 || l.CrMinor > 1_000_000_000_000:
			return nil, command.ErrInvalid("account " + l.AccountCode + ": give either a debit or a credit amount")
		}
		seen[l.AccountCode] = true
		if l.DrMinor > 0 || l.CrMinor > 0 {
			clean = append(clean, l)
		}
	}
	if err := saveDraft(c, goLive, clean); err != nil {
		return nil, err
	}
	return OpeningGet(c)
}

func OpeningPost(c *command.Ctx) (any, error) {
	// make sure the row exists, then lock it: two first posts queue here and the second sees 'posted'
	if _, err := c.Tx.Exec(c.Context, "insert into core.opening_balances (workspace_id, go_live, updated_by) select $1::uuid, $2::date, $3::uuid on conflict do nothing",
		c.WorkspaceID(), mustDefault(c), c.Principal.UserID); err != nil {
		return nil, command.DB(err)
	}
	o, err := loadOpening(c, true)
	if err != nil {
		return nil, err
	}
	if o.Status == "posted" {
		return nil, command.ErrConflict("opening balances are already posted")
	}
	var res struct {
		EntryID string `json:"entry_id"`
		Diff    int64  `json:"difference_minor"`
	}
	if err := callJSON(c, "ledger.opening_post", map[string]any{"go_live": o.GoLive, "lines": o.Lines}, &res); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context,
		`insert into core.opening_balances (workspace_id, go_live, lines, status, entry_id, difference_minor, posted_at, posted_by, updated_by)
		 values ($1::uuid,$2::date,$3,'posted',$4::uuid,$5,now(),$6::uuid,$6::uuid)
		 on conflict (workspace_id) do update set status = 'posted', entry_id = $4::uuid, reversal_entry_id = null, difference_minor = $5,
		   posted_at = now(), posted_by = $6::uuid, updated_by = $6::uuid, updated_at = now()`,
		c.WorkspaceID(), o.GoLive, mustJSON(o.Lines), res.EntryID, res.Diff, c.Principal.UserID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("opening.posted", nil, map[string]any{"go_live": o.GoLive, "entry_id": res.EntryID, "difference_minor": res.Diff}); err != nil {
		return nil, err
	}
	return OpeningGet(c)
}

func OpeningReverse(c *command.Ctx) (any, error) {
	o, err := loadOpening(c, true)
	if err != nil {
		return nil, err
	}
	if o.Status != "posted" || o.EntryID == nil {
		return nil, command.ErrConflict("opening balances are not posted")
	}
	var res struct {
		EntryID string `json:"entry_id"`
	}
	if err := callJSON(c, "ledger.opening_reverse", map[string]any{"entry_id": *o.EntryID}, &res); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "update core.opening_balances set status = 'reversed', reversal_entry_id = $1::uuid, updated_at = now() where workspace_id = $2::uuid", res.EntryID, c.WorkspaceID()); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("opening.reversed", nil, map[string]any{"entry_id": *o.EntryID, "reversal_entry_id": res.EntryID}); err != nil {
		return nil, err
	}
	return OpeningGet(c)
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func mustDefault(c *command.Ctx) string { g, _ := defaultGoLive(c); return g }
