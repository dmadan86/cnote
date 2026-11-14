package purchases

// TDS on vendor payments: the server proposes the deduction from the vendor's default code, the rate table and the
// thresholds; the payer may override the amount. Deduction happens at payment time (the bill still posts gross).
//
// CA review: every number here is data in billing.tds_rates (editable by admins) and the defaults below must be checked
// each April. Sources: the Income-tax Act 2025 section 393(1) table in force from 1 Apr 2026 (old 194C contractors,
// 194J professional and technical fees keep their rates); no-PAN rate 20% (old 206AA). Not handled: 194Q purchase of
// goods (needs the buyer's turnover test), rent, lower-deduction certificates, surcharge and cess.

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
)

const acctTDSPayable = "2400"

var panRe = regexp.MustCompile(`^[A-Z]{5}[0-9]{4}[A-Z]$`)

type tdsRate struct {
	Code        string `json:"code"`
	Label       string `json:"label"`
	RateBP      int32  `json:"rate_bp"`
	NoPANRateBP int32  `json:"no_pan_rate_bp"`
	SingleLimit *int64 `json:"single_limit_minor"`
	AnnualLimit *int64 `json:"annual_limit_minor"`
	Active      bool   `json:"active"`
}

func lim(rupees int64) *int64 { v := rupees * 100; return &v }

// defaultRates: seeded once per workspace, then owned by the workspace.
var defaultRates = []tdsRate{
	{"contractor_individual", "Sec 393(1) contractor, individual/HUF payee (old 194C)", 100, 2000, lim(30_000), lim(100_000), true},
	{"contractor_other", "Sec 393(1) contractor, other payee (old 194C)", 200, 2000, lim(30_000), lim(100_000), true},
	{"professional", "Sec 393(1) professional fees (old 194J)", 1000, 2000, nil, lim(50_000), true},
	{"technical", "Sec 393(1) technical services (old 194J)", 200, 2000, nil, lim(50_000), true},
}

func ensureRates(c *command.Ctx) error {
	for _, r := range defaultRates {
		if _, err := c.Tx.Exec(c.Context,
			`insert into billing.tds_rates (workspace_id, code, label, rate_bp, no_pan_rate_bp, single_limit_minor, annual_limit_minor)
			 values ($1::uuid,$2,$3,$4,$5,$6,$7) on conflict do nothing`,
			c.WorkspaceID(), r.Code, r.Label, r.RateBP, r.NoPANRateBP, r.SingleLimit, r.AnnualLimit); err != nil {
			return command.DB(err)
		}
	}
	return nil
}

const rateCols = "code, label, rate_bp, no_pan_rate_bp, single_limit_minor, annual_limit_minor, active"

func scanRate(r pgx.Row) (tdsRate, error) {
	var x tdsRate
	err := r.Scan(&x.Code, &x.Label, &x.RateBP, &x.NoPANRateBP, &x.SingleLimit, &x.AnnualLimit, &x.Active)
	return x, err
}

func rateList(c *command.Ctx, input json.RawMessage) (any, error) {
	if err := ensureRates(c); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, "select "+rateCols+" from billing.tds_rates order by code")
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []tdsRate{}
	for rows.Next() {
		x, err := scanRate(rows)
		if err != nil {
			return nil, command.DB(err)
		}
		out = append(out, x)
	}
	return out, command.DB(rows.Err())
}

// rateSet creates or updates a rate row; only the fields sent change (null clears a limit).
func rateSet(c *command.Ctx, input json.RawMessage) (any, error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(input, &m) != nil || m == nil {
		return nil, command.ErrInvalid("invalid input")
	}
	var code string
	if json.Unmarshal(m["code"], &code) != nil || !regexp.MustCompile(`^[a-z0-9_]{1,40}$`).MatchString(code) {
		return nil, command.ErrInvalid("code must be lowercase letters, digits and underscores")
	}
	if err := ensureRates(c); err != nil {
		return nil, err
	}
	cur, err := scanRate(c.Tx.QueryRow(c.Context, "select "+rateCols+" from billing.tds_rates where code = $1", code))
	if errors.Is(err, pgx.ErrNoRows) {
		cur = tdsRate{Code: code, NoPANRateBP: 2000, Active: true}
	} else if err != nil {
		return nil, command.DB(err)
	}
	set := func(k string, dst any) error {
		raw, ok := m[k]
		if !ok {
			return nil
		}
		if json.Unmarshal(raw, dst) != nil {
			return command.ErrInvalid(k + " has the wrong type")
		}
		return nil
	}
	for k, dst := range map[string]any{"label": &cur.Label, "rate_bp": &cur.RateBP, "no_pan_rate_bp": &cur.NoPANRateBP,
		"single_limit_minor": &cur.SingleLimit, "annual_limit_minor": &cur.AnnualLimit, "active": &cur.Active} {
		if err := set(k, dst); err != nil {
			return nil, err
		}
	}
	cur.Label = strings.TrimSpace(cur.Label)
	if cur.Label == "" || len(cur.Label) > 200 || cur.RateBP < 0 || cur.RateBP > 10000 || cur.NoPANRateBP < 0 || cur.NoPANRateBP > 10000 ||
		(cur.SingleLimit != nil && *cur.SingleLimit < 0) || (cur.AnnualLimit != nil && *cur.AnnualLimit < 0) {
		return nil, command.ErrInvalid("label is required and rates must be 0 to 10000 basis points; limits cannot be negative")
	}
	out, err := scanRate(c.Tx.QueryRow(c.Context,
		`insert into billing.tds_rates (workspace_id, code, label, rate_bp, no_pan_rate_bp, single_limit_minor, annual_limit_minor, active)
		 values ($1::uuid,$2,$3,$4,$5,$6,$7,$8)
		 on conflict (workspace_id, code) do update set label = excluded.label, rate_bp = excluded.rate_bp, no_pan_rate_bp = excluded.no_pan_rate_bp,
		   single_limit_minor = excluded.single_limit_minor, annual_limit_minor = excluded.annual_limit_minor, active = excluded.active
		 returning `+rateCols, c.WorkspaceID(), cur.Code, cur.Label, cur.RateBP, cur.NoPANRateBP, cur.SingleLimit, cur.AnnualLimit, cur.Active))
	if err != nil {
		return nil, command.DB(err)
	}
	_, err = c.Emit("tds.rate_set", nil, map[string]any{"code": code, "rate_bp": out.RateBP})
	return out, err
}

// vendorSetTax sets the PAN and default TDS code of a vendor.
func vendorSetTax(c *command.Ctx, input json.RawMessage) (any, error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(input, &m) != nil || m == nil {
		return nil, command.ErrInvalid("invalid input")
	}
	var id string
	if json.Unmarshal(m["id"], &id) != nil || !uuidRe.MatchString(id) {
		return nil, command.ErrInvalid("id must be a uuid")
	}
	id = strings.ToLower(id)
	if _, err := loadVendor(c, id); err != nil { // reads the contact page access and is_vendor
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	// a key that is absent keeps the stored value; null or "" clears it
	field := func(k string) (present bool, val string, err error) {
		raw, ok := m[k]
		if !ok {
			return false, "", nil
		}
		var s *string
		if json.Unmarshal(raw, &s) != nil {
			return false, "", command.ErrInvalid(k + " must be a string or null")
		}
		return true, strings.TrimSpace(deref(s)), nil
	}
	hasPAN, panV, err := field("pan")
	if err != nil {
		return nil, err
	}
	hasCode, codeV, err := field("default_tds_code")
	if err != nil {
		return nil, err
	}
	panV = strings.ToUpper(panV)
	if panV != "" && !panRe.MatchString(panV) {
		return nil, command.ErrInvalid("PAN must look like AAAAA9999A")
	}
	if codeV != "" {
		if err := ensureRates(c); err != nil {
			return nil, err
		}
		var ok bool
		if err := c.Tx.QueryRow(c.Context, "select exists(select 1 from billing.tds_rates where code = $1)", codeV).Scan(&ok); err != nil {
			return nil, command.DB(err)
		}
		if !ok {
			return nil, command.ErrInvalid("unknown TDS code " + codeV)
		}
	}
	// an empty string clears; an absent key keeps
	if _, err := c.Tx.Exec(c.Context,
		`update crm.contacts set pan = case when $2::boolean then nullif($3,'') else pan end,
		   default_tds_code = case when $4::boolean then nullif($5,'') else default_tds_code end where note_id = $1::uuid`,
		id, hasPAN, panV, hasCode, codeV); err != nil {
		return nil, command.DB(err)
	}
	var outPAN, outCode *string
	if err := c.Tx.QueryRow(c.Context, "select pan, default_tds_code from crm.contacts where note_id = $1::uuid", id).Scan(&outPAN, &outCode); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("vendor.tax_set", &id, map[string]any{"default_tds_code": outCode, "has_pan": outPAN != nil}); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "pan": outPAN, "default_tds_code": outCode}, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ---------- the deduction ----------

type tds struct {
	Code       string   `json:"tds_code"`
	Label      string   `json:"label,omitempty"`
	RateBP     int32    `json:"rate_bp"`
	BaseMinor  int64    `json:"base_minor"` // this payment's value without GST
	OnMinor    int64    `json:"on_minor"`   // value the deduction is computed on (catch-up included)
	TDSMinor   int64    `json:"tds_minor"`
	NetMinor   int64    `json:"net_minor"` // cash to pay the vendor
	Reason     string   `json:"reason"`
	Warnings   []string `json:"warnings"`
	PriorBase  int64    `json:"prior_base_minor"`
	NoPAN      bool     `json:"no_pan"`
	Applicable bool     `json:"applicable"`
}

// fyStart is the 1 April on or before d (the TDS financial year is fixed by statute, not by the company's FY setting).
func fyStart(d time.Time) time.Time {
	y := d.Year()
	if d.Month() < time.April {
		y--
	}
	return time.Date(y, time.April, 1, 0, 0, 0, 0, d.Location())
}

func mulDiv(a, b, c int64) int64 {
	n := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	return n.Div(n, big.NewInt(c)).Int64()
}

// computeTDS proposes the deduction for paying `amount` (gross, GST included) of bill b. code "" = the vendor's default.
// Rules: base = payment x subtotal / total (GST shown separately is excluded); the deduction applies when one payment
// exceeds the single limit or the financial year's total to the vendor under that code exceeds the annual limit; on the
// payment that crosses the annual limit it is taken on everything not yet deducted on; no limits = every payment.
// The amount is rounded to the nearest rupee.
func computeTDS(c *command.Ctx, b row, amount int64, code string, at time.Time) (tds, error) {
	t := tds{Warnings: []string{}, NetMinor: amount}
	var pan, def *string
	if err := c.Tx.QueryRow(c.Context, "select pan, default_tds_code from crm.contacts where note_id = $1::uuid", b.VendorID).Scan(&pan, &def); err != nil {
		return t, command.DB(err)
	}
	if code == "" && def != nil {
		code = *def
	}
	if code == "" {
		t.Reason = "no TDS code on the vendor or the request: nothing is deducted"
		return t, nil
	}
	if err := ensureRates(c); err != nil {
		return t, err
	}
	r, err := scanRate(c.Tx.QueryRow(c.Context, "select "+rateCols+" from billing.tds_rates where code = $1", code))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, command.ErrInvalid("unknown TDS code " + code)
	}
	if err != nil {
		return t, command.DB(err)
	}
	if !r.Active {
		return t, command.ErrInvalid("TDS code " + code + " is switched off")
	}
	t.Code, t.Label, t.RateBP, t.Applicable = r.Code, r.Label, r.RateBP, true
	if pan == nil {
		t.NoPAN = true
		t.RateBP = max(r.RateBP, r.NoPANRateBP)
		t.Warnings = append(t.Warnings, fmt.Sprintf("The vendor has no PAN: the higher rate %.2f%% applies. Add the PAN to the vendor to use %.2f%%.",
			float64(t.RateBP)/100, float64(r.RateBP)/100))
	}
	t.BaseMinor = amount
	if b.TotalMinor > 0 {
		t.BaseMinor = mulDiv(amount, b.SubtotalMinor, b.TotalMinor)
	}
	start := fyStart(at)
	var prior, priorOn int64
	if err := c.Tx.QueryRow(c.Context,
		`select coalesce(sum(p.tds_base_minor),0)::bigint, coalesce(sum(p.tds_on_minor),0)::bigint
		   from billing.purchase_payments p join billing.purchase_bills pb on pb.note_id = p.bill_id
		  where pb.vendor_id = $1::uuid and p.tds_code = $2 and p.paid_at >= $3 and p.paid_at < $4`,
		b.VendorID, code, start, start.AddDate(1, 0, 0)).Scan(&prior, &priorOn); err != nil {
		return t, command.DB(err)
	}
	t.PriorBase = prior
	cum := prior + t.BaseMinor
	switch {
	case r.SingleLimit == nil && r.AnnualLimit == nil:
		t.OnMinor, t.Reason = t.BaseMinor, "no threshold on this code: every payment is deducted"
	case r.AnnualLimit != nil && cum > *r.AnnualLimit:
		t.OnMinor = cum - priorOn
		t.Reason = fmt.Sprintf("this financial year's payments to the vendor under %s (%s) exceed the annual limit %s", code, rupees(cum), rupees(*r.AnnualLimit))
		if priorOn < prior {
			t.Warnings = append(t.Warnings, "This payment crosses the annual limit, so TDS is also deducted on earlier payments this year that were under the limit. CA review.")
		}
	case r.SingleLimit != nil && t.BaseMinor > *r.SingleLimit:
		t.OnMinor, t.Reason = t.BaseMinor, fmt.Sprintf("this payment (%s without GST) exceeds the single-payment limit %s", rupees(t.BaseMinor), rupees(*r.SingleLimit))
	default:
		t.Reason = fmt.Sprintf("under the limits: %s this year so far, limit %s", rupees(cum), limitText(r))
	}
	t.TDSMinor = min(amount, roundRupee(mulDiv(t.OnMinor, int64(t.RateBP), 10000)))
	t.NetMinor = amount - t.TDSMinor
	return t, nil
}

func limitText(r tdsRate) string {
	var p []string
	if r.SingleLimit != nil {
		p = append(p, rupees(*r.SingleLimit)+" per payment")
	}
	if r.AnnualLimit != nil {
		p = append(p, rupees(*r.AnnualLimit)+" a year")
	}
	return strings.Join(p, " or ")
}

func rupees(minor int64) string { return fmt.Sprintf("Rs %d", minor/100) }

func roundRupee(minor int64) int64 { return (minor + 50) / 100 * 100 }

// tdsPreview is what the pay-bill dialog shows before the payment.
func tdsPreview(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	if err := requireID(v); err != nil {
		return nil, err
	}
	if v.AmountMinor == nil || *v.AmountMinor <= 0 {
		return nil, command.ErrInvalid("amount_minor must be positive")
	}
	if _, err := access.AssertNoteAccess(c, v.ID, false); err != nil {
		return nil, err
	}
	b, err := load(c, v.ID, false)
	if err != nil {
		return nil, err
	}
	code := ""
	if v.TDSCode != nil {
		code = *v.TDSCode
	}
	return computeTDS(c, b, *v.AmountMinor, code, billing.TodayIST())
}

// tdsPayable is the tax withheld from vendor payments between two dates (by payment date, India time), by code and
// vendor with the vendor's PAN, plus what the books still owe the government (account 2400; challan deposits are not
// tracked yet, so that balance only falls when someone posts against it).
func tdsPayable(c *command.Ctx, input json.RawMessage) (any, error) {
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
		`select p.tds_code, coalesce(r.label, p.tds_code), b.vendor_id::text, vn.title, ct.pan, count(*)::bigint,
		   sum(p.tds_base_minor)::bigint, sum(p.tds_minor)::bigint, max(p.tds_rate_bp)
		   from billing.purchase_payments p
		   join billing.purchase_bills b on b.note_id = p.bill_id join core.notes n on n.id = b.note_id
		   join crm.contacts ct on ct.note_id = b.vendor_id join core.notes vn on vn.id = b.vendor_id
		   left join billing.tds_rates r on r.code = p.tds_code
		  where p.tds_minor > 0 and (p.paid_at at time zone 'Asia/Kolkata')::date between $1::date and $2::date
		    and n.space_id = any($3::text[]::uuid[]) and core.note_access(n.id, $4::uuid, $5, $3::text[]::uuid[]) <> 'none'
		  group by p.tds_code, r.label, b.vendor_id, vn.title, ct.pan order by p.tds_code, vn.title`, *v.From, *v.To, spaces, uid, admin)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	type vrow struct {
		VendorID  string  `json:"vendor_id"`
		Vendor    string  `json:"vendor"`
		PAN       *string `json:"pan"`
		Payments  int64   `json:"payments"`
		BaseMinor int64   `json:"base_minor"`
		TDSMinor  int64   `json:"tds_minor"`
		RateBP    int32   `json:"rate_bp"`
	}
	type group struct {
		Code     string `json:"tds_code"`
		Label    string `json:"label"`
		TDSMinor int64  `json:"tds_minor"`
		Vendors  []vrow `json:"vendors"`
	}
	groups := []*group{}
	var total int64
	for rows.Next() {
		var code, label string
		var x vrow
		if err := rows.Scan(&code, &label, &x.VendorID, &x.Vendor, &x.PAN, &x.Payments, &x.BaseMinor, &x.TDSMinor, &x.RateBP); err != nil {
			return nil, command.DB(err)
		}
		if len(groups) == 0 || groups[len(groups)-1].Code != code {
			groups = append(groups, &group{Code: code, Label: label, Vendors: []vrow{}})
		}
		g := groups[len(groups)-1]
		g.Vendors, g.TDSMinor, total = append(g.Vendors, x), g.TDSMinor+x.TDSMinor, total+x.TDSMinor
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	var owed int64
	if err := c.Tx.QueryRow(c.Context, "select coalesce(sum(credit_minor - debit_minor), 0)::bigint from billing.journal_lines where account_code = $1", acctTDSPayable).Scan(&owed); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"from": *v.From, "to": *v.To, "groups": groups, "total_tds_minor": total, "owed_to_government_minor": owed}, nil
}
