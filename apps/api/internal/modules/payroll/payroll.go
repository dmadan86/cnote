// Package payroll is monthly payroll for Indian employers (WP25): salary structures, a monthly run (draft, adjust,
// approve, finalise, pay, reverse) with statutory deductions (EPF/EPS, ESI, professional tax, TDS on salary under the
// new regime), payslips, a bank payout CSV and PF/ESI/PT/TDS summaries. Go-only. Money is integer paise.
//
// Statutory rates, ceilings, slabs and PT tables are rows in payroll.params, tax_slabs and pt_slabs (dated), not code.
// Salary data is personal and sensitive: every command is for a person (agents and API keys get 403), the payroll.*
// commands need an admin with finance access, and a member only ever reads their own payslips.
//
// Postings (accounts are created on first use):
//
//	finalise   Dr 5300 salaries, Dr 5310 employer contributions / Cr 2510 PF, 2520 ESI, 2530 PT, 2540 TDS, 2560 other deductions, 2550 net salary payable
//	mark paid  Dr 2550 / Cr bank
//	reverse    the same legs swapped, for each posting made (owner only)
//
// Not built (deferred): old-regime declarations, gratuity, bonus, LWF, arrears, full and final, Form 16, filing
// integrations, EDLI/admin charges, women's PT exemptions, mid-year joiner tax carry-over.
package payroll

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
)

func Commands() []command.Command {
	adm := func(name string, h command.Handler) command.Command {
		c := command.NewCommand(name, command.GroupAdmin, h)
		c.MinRole = command.Admin
		return c
	}
	reverse := adm("payroll.reverse", reverse)
	reverse.MinRole = command.Owner
	rd := func(name string, h command.Handler) command.Command {
		c := command.NewCommand(name, command.GroupRead, h)
		c.MinRole = command.Member
		return c
	}
	return []command.Command{
		adm("payroll.structure_list", structureList),
		adm("payroll.structure_get", structureGet),
		adm("payroll.structure_set", structureSet),
		adm("payroll.config", config),
		adm("payroll.run_create", runCreate),
		adm("payroll.run_recompute", runRecompute),
		adm("payroll.run_list", runList),
		adm("payroll.run_get", runGet),
		adm("payroll.run_delete", runDelete),
		adm("payroll.adjust", adjust),
		adm("payroll.adjust_delete", adjustDelete),
		adm("payroll.approve", approve),
		adm("payroll.finalise", finalise),
		adm("payroll.mark_paid", markPaid),
		reverse,
		adm("payroll.payout_csv", payoutCSV),
		adm("payroll.statutory", statutory),
		rd("payslip.list", payslipList),
		rd("payslip.get", payslipGet),
	}
}

// ---------- input helpers ----------

var (
	uuidRe  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	monthRe = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)
)

func bind(raw json.RawMessage, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return command.ErrInvalid("invalid input: " + err.Error())
	}
	return nil
}

func checkID(name, v string, required bool) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" && !required {
		return "", nil
	}
	if !uuidRe.MatchString(v) {
		return "", command.ErrInvalid(name + " must be a UUID")
	}
	return v, nil
}

func parseMonth(v string) (time.Time, error) {
	if !monthRe.MatchString(v) {
		return time.Time{}, command.ErrInvalid("month must be like 2026-04")
	}
	t, _ := time.Parse("2006-01", v)
	return t, nil
}

// human: salary data belongs to people; agents and API keys never touch it.
func human(c *command.Ctx) (string, error) {
	if c.Principal.Kind != command.KindUser || c.Principal.UserID == nil {
		return "", command.ErrForbidden("payroll is for people: API keys and agents cannot use it")
	}
	return *c.Principal.UserID, nil
}

func ensureAccount(c *command.Ctx, code, name, kind string) error {
	_, err := c.Tx.Exec(c.Context, "insert into billing.accounts (workspace_id, code, name, kind) values ($1::uuid,$2,$3,$4) on conflict do nothing", c.WorkspaceID(), code, name, kind)
	return command.DB(err)
}

func mask(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	v := *s
	if len(v) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(v)-4) + v[len(v)-4:]
}

// ---------- salary structures ----------

type structIn struct {
	EmployeeID   string  `json:"employee_id"`
	BasicMinor   *int64  `json:"basic_minor"`
	HRAMinor     *int64  `json:"hra_minor"`
	SpecialMinor *int64  `json:"special_minor"`
	OtherMinor   *int64  `json:"other_minor"`
	PTState      *string `json:"pt_state"`
	PF           *bool   `json:"pf_applicable"`
	ESI          *bool   `json:"esi_applicable"`
	Regime       *string `json:"regime"`
	PAN          *string `json:"pan"`
	BankAccount  *string `json:"bank_account"`
	IFSC         *string `json:"ifsc"`
	UAN          *string `json:"uan"`
	ESICNo       *string `json:"esic_no"`
	BranchID     *string `json:"branch_id"`
}

type structure struct {
	EmployeeID                                    string
	Basic, HRA, Special, Other                    int64
	PTState, PAN, Bank, IFSC, UAN, ESICNo, Branch *string
	PF, ESI                                       bool
	Regime                                        string
}

const structCols = `s.employee_id::text, s.basic_minor, s.hra_minor, s.special_minor, s.other_minor, s.pt_state, s.pan, s.bank_account, s.ifsc, s.uan, s.esic_no, s.branch_id::text, s.pf_applicable, s.esi_applicable, s.regime`

func (s *structure) dests() []any {
	return []any{&s.EmployeeID, &s.Basic, &s.HRA, &s.Special, &s.Other, &s.PTState, &s.PAN, &s.Bank, &s.IFSC, &s.UAN, &s.ESICNo, &s.Branch, &s.PF, &s.ESI, &s.Regime}
}

func (s *structure) json(full bool) map[string]any {
	m := map[string]any{"employee_id": s.EmployeeID, "basic_minor": s.Basic, "hra_minor": s.HRA, "special_minor": s.Special, "other_minor": s.Other,
		"gross_minor": s.Basic + s.HRA + s.Special + s.Other, "pt_state": s.PTState, "pf_applicable": s.PF, "esi_applicable": s.ESI,
		"regime": s.Regime, "branch_id": s.Branch, "has_pan": s.PAN != nil, "has_bank": s.Bank != nil && s.IFSC != nil}
	if full { // the edit form of a finance admin
		m["pan"], m["bank_account"], m["ifsc"], m["uan"], m["esic_no"] = s.PAN, s.Bank, s.IFSC, s.UAN, s.ESICNo
	} else {
		m["pan"], m["bank_account"], m["ifsc"], m["uan"], m["esic_no"] = mask(s.PAN), mask(s.Bank), s.IFSC, mask(s.UAN), mask(s.ESICNo)
	}
	return m
}

func structureList(c *command.Ctx, in json.RawMessage) (any, error) {
	if _, err := human(c); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `select e.id::text, coalesce(u.name, e.name), e.code, e.active, `+structCols+`
		from hr.employees e left join core.users u on u.id = e.user_id left join payroll.salary_structures s on s.employee_id = e.id
		order by (not e.active), coalesce(u.name, e.name)`)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var code *string
		var active bool
		var eid, pt, pan, bank, ifsc, uan, esic, br, reg *string
		var b, h, sp, o *int64
		var pf, esi *bool
		if err := rows.Scan(&id, &name, &code, &active, &eid, &b, &h, &sp, &o, &pt, &pan, &bank, &ifsc, &uan, &esic, &br, &pf, &esi, &reg); err != nil {
			return nil, command.DB(err)
		}
		row := map[string]any{"employee_id": id, "name": name, "code": code, "active": active, "structure": nil}
		if eid != nil {
			s := structure{EmployeeID: id, Basic: *b, HRA: *h, Special: *sp, Other: *o, PTState: pt, PAN: pan, Bank: bank, IFSC: ifsc, UAN: uan, ESICNo: esic, Branch: br, PF: *pf, ESI: *esi, Regime: *reg}
			row["structure"] = s.json(false)
		}
		out = append(out, row)
	}
	return out, command.DB(rows.Err())
}

func structureGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	if _, err := human(c); err != nil {
		return nil, err
	}
	var in structIn
	if err := bind(raw, &in); err != nil {
		return nil, err
	}
	id, err := checkID("employee_id", in.EmployeeID, true)
	if err != nil {
		return nil, err
	}
	var s structure
	err = c.Tx.QueryRow(c.Context, "select "+structCols+" from payroll.salary_structures s where s.employee_id = $1::uuid", id).Scan(s.dests()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("salary structure")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	return s.json(true), nil // payroll commands already need finance access, so the full form is allowed
}

var (
	panRe  = regexp.MustCompile(`^[A-Z]{5}[0-9]{4}[A-Z]$`)
	ifscRe = regexp.MustCompile(`^[A-Z]{4}0[A-Z0-9]{6}$`)
	acctRe = regexp.MustCompile(`^[0-9]{6,20}$`)
	uanRe  = regexp.MustCompile(`^[0-9]{12}$`)
	esicRe = regexp.MustCompile(`^[0-9]{10,17}$`)
	stRe   = regexp.MustCompile(`^[0-9]{2}$`)
)

// clean trims and upper-cases an optional identifier; empty clears it.
func clean(p *string, re *regexp.Regexp, name string, upper bool) (*string, error) {
	if p == nil {
		return nil, nil
	}
	v := strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(*p))
	if upper {
		v = strings.ToUpper(v)
	}
	if v == "" {
		return nil, nil
	}
	if !re.MatchString(v) {
		return nil, command.ErrInvalid(name + " is not valid")
	}
	return &v, nil
}

func structureSet(c *command.Ctx, raw json.RawMessage) (any, error) {
	uid, err := human(c)
	if err != nil {
		return nil, err
	}
	var in structIn
	if err := bind(raw, &in); err != nil {
		return nil, err
	}
	emp, err := checkID("employee_id", in.EmployeeID, true)
	if err != nil {
		return nil, err
	}
	for n, v := range map[string]*int64{"basic_minor": in.BasicMinor, "hra_minor": in.HRAMinor, "special_minor": in.SpecialMinor, "other_minor": in.OtherMinor} {
		if v != nil && (*v < 0 || *v > 100_000_000_00) {
			return nil, command.ErrInvalid(n + " must be paise, zero or more")
		}
	}
	var cur structure
	err = c.Tx.QueryRow(c.Context, "select "+structCols+" from payroll.salary_structures s where s.employee_id = $1::uuid", emp).Scan(cur.dests()...)
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, command.DB(err)
	}
	if !exists {
		cur = structure{EmployeeID: emp, PF: true, Regime: "new"}
	}
	set := func(dst *int64, v *int64) {
		if v != nil {
			*dst = *v
		}
	}
	set(&cur.Basic, in.BasicMinor)
	set(&cur.HRA, in.HRAMinor)
	set(&cur.Special, in.SpecialMinor)
	set(&cur.Other, in.OtherMinor)
	if cur.Basic <= 0 {
		return nil, command.ErrInvalid("basic_minor is required (paise)")
	}
	if in.PF != nil {
		cur.PF = *in.PF
	}
	if in.ESI != nil {
		cur.ESI = *in.ESI
	}
	if in.Regime != nil {
		if *in.Regime != "new" && *in.Regime != "old" {
			return nil, command.ErrInvalid("regime must be new or old")
		}
		cur.Regime = *in.Regime
	}
	for _, f := range []struct {
		src  *string
		dst  **string
		re   *regexp.Regexp
		name string
		up   bool
	}{{in.PTState, &cur.PTState, stRe, "pt_state (GST state code like 29)", false}, {in.PAN, &cur.PAN, panRe, "pan", true}, {in.BankAccount, &cur.Bank, acctRe, "bank_account", false},
		{in.IFSC, &cur.IFSC, ifscRe, "ifsc", true}, {in.UAN, &cur.UAN, uanRe, "uan", false}, {in.ESICNo, &cur.ESICNo, esicRe, "esic_no", false}} {
		if f.src == nil {
			continue
		}
		v, err := clean(f.src, f.re, f.name, f.up)
		if err != nil {
			return nil, err
		}
		*f.dst = v
	}
	if (cur.Bank == nil) != (cur.IFSC == nil) {
		return nil, command.ErrInvalid("give both bank_account and ifsc, or neither")
	}
	if in.BranchID != nil {
		b, err := checkID("branch_id", *in.BranchID, false)
		if err != nil {
			return nil, err
		}
		cur.Branch = nil
		if b != "" {
			cur.Branch = &b
		}
	}
	_, err = c.Tx.Exec(c.Context, `insert into payroll.salary_structures (workspace_id, employee_id, basic_minor, hra_minor, special_minor, other_minor, pt_state, pf_applicable,
		esi_applicable, regime, pan, bank_account, ifsc, uan, esic_no, branch_id, updated_by)
		values ($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16::uuid,$17::uuid)
		on conflict (workspace_id, employee_id) do update set basic_minor=excluded.basic_minor, hra_minor=excluded.hra_minor, special_minor=excluded.special_minor,
		other_minor=excluded.other_minor, pt_state=excluded.pt_state, pf_applicable=excluded.pf_applicable, esi_applicable=excluded.esi_applicable, regime=excluded.regime,
		pan=excluded.pan, bank_account=excluded.bank_account, ifsc=excluded.ifsc, uan=excluded.uan, esic_no=excluded.esic_no, branch_id=excluded.branch_id,
		updated_by=excluded.updated_by, updated_at=now()`,
		c.WorkspaceID(), emp, cur.Basic, cur.HRA, cur.Special, cur.Other, cur.PTState, cur.PF, cur.ESI, cur.Regime, cur.PAN, cur.Bank, cur.IFSC, cur.UAN, cur.ESICNo, cur.Branch, uid)
	if err != nil {
		if strings.Contains(err.Error(), "23503") {
			return nil, command.ErrNotFound("employee or branch")
		}
		return nil, command.DB(err)
	}
	if _, err := c.Emit("payroll.structure_set", nil, map[string]any{"employee_id": emp}); err != nil { // ids only: the log is append-only
		return nil, err
	}
	return cur.json(true), nil
}

// ---------- statutory configuration ----------

func loadParams(c *command.Ctx, month time.Time) (Params, error) {
	var p Params
	rows, err := c.Tx.Query(c.Context, `select distinct on (key) key, value from payroll.params where effective_from <= $1::date order by key, effective_from desc`, month)
	if err != nil {
		return p, command.DB(err)
	}
	vals := map[string]int64{}
	for rows.Next() {
		var k string
		var v int64
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return p, command.DB(err)
		}
		vals[k] = v
	}
	rows.Close()
	for _, k := range []string{"pf_wage_ceiling_minor", "pf_ee_bp", "pf_er_total_bp", "eps_bp", "eps_max_minor", "wage_rule_exclusion_bp", "esi_ceiling_minor", "esi_ee_bp", "esi_er_bp",
		"tds_std_deduction_minor", "tds_rebate_limit_minor", "tds_rebate_max_minor", "tds_cess_bp", "tds_surcharge_from_minor"} {
		if _, ok := vals[k]; !ok {
			return p, command.ErrConflict("statutory setting " + k + " has no value for this month; add it to payroll.params")
		}
	}
	p = Params{PFCeiling: vals["pf_wage_ceiling_minor"], PFEeBP: vals["pf_ee_bp"], PFErBP: vals["pf_er_total_bp"], EPSBP: vals["eps_bp"], EPSMax: vals["eps_max_minor"],
		ExclBP: vals["wage_rule_exclusion_bp"], ESICeiling: vals["esi_ceiling_minor"], ESIEeBP: vals["esi_ee_bp"], ESIErBP: vals["esi_er_bp"], StdDed: vals["tds_std_deduction_minor"],
		RebateLimit: vals["tds_rebate_limit_minor"], RebateMax: vals["tds_rebate_max_minor"], CessBP: vals["tds_cess_bp"], SurchFrom: vals["tds_surcharge_from_minor"]}
	sr, err := c.Tx.Query(c.Context, `select from_minor, rate_bp from payroll.tax_slabs where regime = 'new'
		and effective_from = (select max(effective_from) from payroll.tax_slabs where regime = 'new' and effective_from <= $1::date) order by from_minor`, month)
	if err != nil {
		return p, command.DB(err)
	}
	defer sr.Close()
	for sr.Next() {
		var s Slab
		if err := sr.Scan(&s.From, &s.RateBP); err != nil {
			return p, command.DB(err)
		}
		p.Slabs = append(p.Slabs, s)
	}
	if len(p.Slabs) == 0 {
		return p, command.ErrConflict("no income tax slabs for this month; add them to payroll.tax_slabs")
	}
	return p, command.DB(sr.Err())
}

func loadPT(c *command.Ctx, state string, month time.Time) ([]PTSlab, error) {
	rows, err := c.Tx.Query(c.Context, `select upto_minor, amount_minor, feb_amount_minor, basis_months, months, state_name, state_code from payroll.pt_slabs where state_code = $1
		and effective_from = (select max(effective_from) from payroll.pt_slabs where state_code = $1 and effective_from <= $2::date)`, state, month)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	var out []PTSlab
	for rows.Next() {
		var s PTSlab
		var bm int32
		if err := rows.Scan(&s.Upto, &s.Amount, &s.Feb, &bm, &s.Months, &s.StateName, &s.Code); err != nil {
			return nil, command.DB(err)
		}
		s.BasisMonths = int64(bm)
		out = append(out, s)
	}
	return out, command.DB(rows.Err())
}

// config shows what the engine uses for a month and where each value came from, so a CA can review it.
func config(c *command.Ctx, raw json.RawMessage) (any, error) {
	if _, err := human(c); err != nil {
		return nil, err
	}
	var in struct {
		Month string `json:"month"`
	}
	if err := bind(raw, &in); err != nil {
		return nil, err
	}
	m := time.Now().UTC()
	if in.Month != "" {
		var err error
		if m, err = parseMonth(in.Month); err != nil {
			return nil, err
		}
	}
	m = time.Date(m.Year(), m.Month(), 1, 0, 0, 0, 0, time.UTC)
	agg := func(sql string) (json.RawMessage, error) {
		var out json.RawMessage
		err := c.Tx.QueryRow(c.Context, `select coalesce(jsonb_agg(to_jsonb(t)), '[]'::jsonb) from (`+sql+`) t`, m).Scan(&out)
		return out, command.DB(err)
	}
	params, err := agg(`select distinct on (key) key, value, effective_from, source, note from payroll.params where effective_from <= $1::date order by key, effective_from desc`)
	if err != nil {
		return nil, err
	}
	slabs, err := agg(`select from_minor, rate_bp, effective_from, source from payroll.tax_slabs where regime = 'new' and effective_from = (select max(effective_from) from payroll.tax_slabs where effective_from <= $1::date) order by from_minor`)
	if err != nil {
		return nil, err
	}
	pt, err := agg(`select state_code, state_name, effective_from, upto_minor, amount_minor, feb_amount_minor, basis_months, months, source from payroll.pt_slabs p
		where effective_from = (select max(effective_from) from payroll.pt_slabs q where q.state_code = p.state_code and q.effective_from <= $1::date) order by state_code, upto_minor nulls last`)
	if err != nil {
		return nil, err
	}
	return map[string]any{"month": m.Format("2006-01"), "params": params, "tax_slabs": slabs, "pt_slabs": pt,
		"note": "Every value is marked CA review. Change a rate by inserting a row with a newer effective_from; states without a PT table pay no PT and show a warning."}, nil
}
