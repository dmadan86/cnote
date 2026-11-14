package payroll

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/billing"
)

const (
	acctSalaries = "5300"
	acctEmployer = "5310"
	acctPF       = "2510"
	acctESI      = "2520"
	acctPT       = "2530"
	acctTDS      = "2540"
	acctNet      = "2550"
	acctOther    = "2560"
)

type in struct {
	ID          string `json:"id"`
	Month       string `json:"month"`
	BranchID    string `json:"branch_id"`
	EmployeeID  string `json:"employee_id"`
	Kind        string `json:"kind"`
	Reason      string `json:"reason"`
	Reference   string `json:"reference"`
	AmountMinor *int64 `json:"amount_minor"`
	Status      string `json:"status"`
}

func decode(raw json.RawMessage) (in, error) {
	var v in
	if err := bind(raw, &v); err != nil {
		return v, err
	}
	var err error
	if v.ID, err = checkID("id", v.ID, false); err != nil {
		return v, err
	}
	if v.EmployeeID, err = checkID("employee_id", v.EmployeeID, false); err != nil {
		return v, err
	}
	if v.BranchID, err = checkID("branch_id", v.BranchID, false); err != nil {
		return v, err
	}
	for n, s := range map[string]*string{"reason": &v.Reason, "reference": &v.Reference} {
		*s = strings.TrimSpace(*s)
		if len([]rune(*s)) > 200 {
			return v, command.ErrInvalid(n + " is too long")
		}
	}
	return v, nil
}

type runInfo struct {
	ID, Status string
	Month      time.Time
	Branch     *string
}

// lockRun reads a run for update.
func lockRun(c *command.Ctx, id string) (runInfo, error) {
	if id == "" {
		return runInfo{}, command.ErrInvalid("id must be a UUID")
	}
	var r runInfo
	err := c.Tx.QueryRow(c.Context, "select id::text, status, month, branch_id::text from payroll.runs where id = $1::uuid for update", id).Scan(&r.ID, &r.Status, &r.Month, &r.Branch)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, command.ErrNotFound("payroll run")
	}
	return r, command.DB(err)
}

const runCols = `id::text, month::text, branch_id::text, status, employees, gross_minor, deductions_minor, net_minor, employer_minor, warnings, created_at, approved_at, finalised_at, paid_at, reversed_at, reverse_reason, paid_reference`

func scanRun(row pgx.Row) (map[string]any, error) {
	var id, month, status string
	var branch, reason, ref *string
	var emp int
	var gross, ded, net, er int64
	var warn json.RawMessage
	var created time.Time
	var appr, fin, paid, rev *time.Time
	if err := row.Scan(&id, &month, &branch, &status, &emp, &gross, &ded, &net, &er, &warn, &created, &appr, &fin, &paid, &rev, &reason, &ref); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "month": month[:7], "branch_id": branch, "status": status, "employees": emp, "gross_minor": gross, "deductions_minor": ded,
		"net_minor": net, "employer_minor": er, "warnings": warn, "created_at": created, "approved_at": command.TsPtr(appr), "finalised_at": command.TsPtr(fin),
		"paid_at": command.TsPtr(paid), "reversed_at": command.TsPtr(rev), "reverse_reason": reason, "paid_reference": ref}, nil
}

func runRow(c *command.Ctx, id string) (map[string]any, error) {
	m, err := scanRun(c.Tx.QueryRow(c.Context, "select "+runCols+" from payroll.runs where id = $1::uuid", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("payroll run")
	}
	return m, command.DB(err)
}

func runList(c *command.Ctx, raw json.RawMessage) (any, error) {
	if _, err := human(c); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, "select "+runCols+" from payroll.runs order by month desc, created_at desc limit 200")
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		m, err := scanRun(rows)
		if err != nil {
			return nil, command.DB(err)
		}
		out = append(out, m)
	}
	return out, command.DB(rows.Err())
}

func runGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	if _, err := human(c); err != nil {
		return nil, err
	}
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if v.ID == "" {
		return nil, command.ErrInvalid("id must be a UUID")
	}
	run, err := runRow(c, v.ID)
	if err != nil {
		return nil, err
	}
	var lines, adj json.RawMessage
	err = c.Tx.QueryRow(c.Context, `select coalesce(jsonb_agg(jsonb_build_object('id', id, 'employee_id', employee_id, 'name', name, 'code', code, 'days_in_month', days_in_month,
		'lop_days', lop_halves / 2.0, 'unmarked_days', unmarked_days, 'basic_minor', basic_minor, 'hra_minor', hra_minor, 'special_minor', special_minor, 'other_minor', other_minor,
		'adj_earnings_minor', adj_earnings_minor, 'gross_minor', gross_minor, 'epf_ee_minor', epf_ee_minor, 'esi_ee_minor', esi_ee_minor, 'pt_minor', pt_minor, 'tds_minor', tds_minor,
		'adj_deductions_minor', adj_deductions_minor, 'deductions_minor', deductions_minor, 'net_minor', net_minor, 'epf_er_minor', epf_er_minor, 'eps_er_minor', eps_er_minor,
		'esi_er_minor', esi_er_minor, 'pf_wages_minor', pf_wages_minor, 'has_bank', bank_account is not null, 'warnings', warnings) order by name), '[]'::jsonb) from payroll.run_lines where run_id = $1::uuid`, v.ID).Scan(&lines)
	if err != nil {
		return nil, command.DB(err)
	}
	if err := c.Tx.QueryRow(c.Context, `select coalesce(jsonb_agg(jsonb_build_object('id', id, 'employee_id', employee_id, 'kind', kind, 'amount_minor', amount_minor, 'reason', reason) order by created_at), '[]'::jsonb)
		from payroll.adjustments where run_id = $1::uuid`, v.ID).Scan(&adj); err != nil {
		return nil, command.DB(err)
	}
	run["lines"], run["adjustments"] = lines, adj
	return run, nil
}

func nowIST(c *command.Ctx) (time.Time, error) {
	var d time.Time
	err := c.Tx.QueryRow(c.Context, "select (now() at time zone 'Asia/Kolkata')::date").Scan(&d)
	return d, command.DB(err)
}

func runCreate(c *command.Ctx, raw json.RawMessage) (any, error) {
	uid, err := human(c)
	if err != nil {
		return nil, err
	}
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	month, err := parseMonth(v.Month)
	if err != nil {
		return nil, err
	}
	today, err := nowIST(c)
	if err != nil {
		return nil, err
	}
	if month.After(time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)) {
		return nil, command.ErrInvalid("payroll cannot be run for a future month")
	}
	// A month is run once per scope: one run for everyone, or one per branch. Reversed runs free the month.
	var other, status string
	err = c.Tx.QueryRow(c.Context, `select id::text, status from payroll.runs where month = $1::date and status <> 'reversed'
		and (branch_id is null or $2::uuid is null or branch_id = $2::uuid) limit 1`, month, nilIfEmpty(v.BranchID)).Scan(&other, &status)
	if err == nil {
		if status == "draft" || status == "approved" {
			return nil, command.ErrConflict("a " + status + " run for " + month.Format("January 2006") + " already exists; open it (run " + other + ")")
		}
		return nil, command.ErrConflict("payroll for " + month.Format("January 2006") + " is already " + status + " (run " + other + "); only an owner can reverse it")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, command.DB(err)
	}
	var id string
	if err := c.Tx.QueryRow(c.Context, "insert into payroll.runs (workspace_id, month, branch_id, created_by) values ($1::uuid,$2::date,$3::uuid,$4::uuid) returning id::text",
		c.WorkspaceID(), month, nilIfEmpty(v.BranchID), uid).Scan(&id); err != nil {
		if strings.Contains(err.Error(), "23503") {
			return nil, command.ErrInvalid("branch not found")
		}
		return nil, command.DB(err)
	}
	if err := computeRun(c, runInfo{ID: id, Month: month, Status: "draft", Branch: nilIfEmptyP(v.BranchID)}); err != nil {
		return nil, err
	}
	if _, err := c.Emit("payroll.run_created", nil, map[string]any{"run_id": id, "month": month.Format("2006-01")}); err != nil {
		return nil, err
	}
	return runGet(c, mustJSON(id))
}

func mustJSON(id string) json.RawMessage { b, _ := json.Marshal(map[string]string{"id": id}); return b }

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nilIfEmptyP(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func runRecompute(c *command.Ctx, raw json.RawMessage) (any, error) {
	if _, err := human(c); err != nil {
		return nil, err
	}
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	r, err := lockRun(c, v.ID)
	if err != nil {
		return nil, err
	}
	if err := computeRun(c, r); err != nil {
		return nil, err
	}
	return runGet(c, mustJSON(r.ID))
}

func runDelete(c *command.Ctx, raw json.RawMessage) (any, error) {
	if _, err := human(c); err != nil {
		return nil, err
	}
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	r, err := lockRun(c, v.ID)
	if err != nil {
		return nil, err
	}
	if r.Status != "draft" && r.Status != "approved" {
		return nil, command.ErrConflict("only a draft or approved run can be deleted; a finalised run is reversed by an owner")
	}
	if _, err := c.Tx.Exec(c.Context, "delete from payroll.runs where id = $1::uuid", r.ID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("payroll.run_deleted", nil, map[string]any{"run_id": r.ID}); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

// computeRun (re)builds every line of a draft or approved run from structures, attendance, adjustments and the
// statutory tables. An approved run goes back to draft: what was approved no longer exists.
func computeRun(c *command.Ctx, r runInfo) error {
	if r.Status != "draft" && r.Status != "approved" {
		return command.ErrConflict("payroll for this month is already " + r.Status + "; it cannot be recomputed")
	}
	p, err := loadParams(c, r.Month)
	if err != nil {
		return err
	}
	first, last := r.Month, r.Month.AddDate(0, 1, -1)
	fy := time.Date(r.Month.Year(), 4, 1, 0, 0, 0, 0, time.UTC)
	if r.Month.Month() < 4 {
		fy = fy.AddDate(-1, 0, 0)
	}
	today, err := nowIST(c)
	if err != nil {
		return err
	}

	// Loss of pay in half days: absent days, half days, and approved unpaid leave. Paid leave and not-marked days cost nothing.
	lop := map[string]int64{}
	if err := mapInts(c, lop, `select a.employee_id::text, sum(case a.status
		when 'absent' then 2
		when 'half_day' then case when a.note = 'Half-day leave' then (case when `+unpaid+` then 1 else 0 end) else 1 end
		when 'leave' then case when `+unpaid+` then 2 else 0 end else 0 end)::bigint
		from hr.attendance a where a.day between $1::date and $2::date group by a.employee_id`, first, last); err != nil {
		return err
	}
	unmarked := map[string]int64{}
	if err := mapInts(c, unmarked, `select e.id::text, count(*)::bigint from hr.employees e cross join generate_series($1::date, $2::date, interval '1 day') g(d)
		where e.active and g.d::date <= $3::date
		  and not exists (select 1 from hr.attendance a where a.employee_id = e.id and a.day = g.d::date)
		  and not (e.weekly_off @> array[extract(dow from g.d)::int])
		  and not exists (select 1 from hr.holidays h where h.day = g.d::date) group by e.id`, first, last, today); err != nil {
		return err
	}
	adjEarn, adjDed := map[string]int64{}, map[string]int64{}
	if err := mapInts(c, adjEarn, "select employee_id::text, sum(amount_minor)::bigint from payroll.adjustments where run_id = $1::uuid and kind = 'earning' group by employee_id", r.ID); err != nil {
		return err
	}
	if err := mapInts(c, adjDed, "select employee_id::text, sum(amount_minor)::bigint from payroll.adjustments where run_id = $1::uuid and kind = 'deduction' group by employee_id", r.ID); err != nil {
		return err
	}
	ytdGross, ytdTDS := map[string]int64{}, map[string]int64{}
	if err := mapInts(c, ytdGross, `select l.employee_id::text, sum(l.gross_minor)::bigint from payroll.run_lines l join payroll.runs x on x.id = l.run_id
		where x.status in ('finalised','paid') and x.month >= $1::date and x.month < $2::date group by l.employee_id`, fy, first); err != nil {
		return err
	}
	if err := mapInts(c, ytdTDS, `select l.employee_id::text, sum(l.tds_minor)::bigint from payroll.run_lines l join payroll.runs x on x.id = l.run_id
		where x.status in ('finalised','paid') and x.month >= $1::date and x.month < $2::date group by l.employee_id`, fy, first); err != nil {
		return err
	}

	type emp struct {
		id, name     string
		code         *string
		s            structure
		joined, left *time.Time
	}
	rows, err := c.Tx.Query(c.Context, `select e.id::text, coalesce(u.name, e.name), e.code, `+structCols+`, m.joined_on, m.left_on
		from hr.employees e join payroll.salary_structures s on s.employee_id = e.id left join core.users u on u.id = e.user_id
		left join core.memberships m on m.workspace_id = e.workspace_id and m.user_id = e.user_id
		where e.active and ($1::uuid is null or s.branch_id = $1::uuid) order by coalesce(u.name, e.name)`, r.Branch)
	if err != nil {
		return command.DB(err)
	}
	var emps []emp
	for rows.Next() {
		var e emp
		dst := append([]any{&e.id, &e.name, &e.code}, e.s.dests()...)
		if err := rows.Scan(append(dst, &e.joined, &e.left)...); err != nil {
			rows.Close()
			return command.DB(err)
		}
		emps = append(emps, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return command.DB(err)
	}

	if _, err := c.Tx.Exec(c.Context, "delete from payroll.run_lines where run_id = $1::uuid", r.ID); err != nil {
		return command.DB(err)
	}
	pt := map[string][]PTSlab{}
	var gross, ded, net, employer int64
	warnUnmarked := 0
	for _, e := range emps {
		st := ""
		if e.s.PTState != nil {
			st = *e.s.PTState
			if _, ok := pt[st]; !ok {
				if pt[st], err = loadPT(c, st, r.Month); err != nil {
					return err
				}
			}
		}
		halves := lop[e.id]
		// days outside the employment window (joined late or left early) are not paid
		if e.joined != nil && e.joined.After(first) {
			halves += 2 * int64(e.joined.Sub(first).Hours()/24+0.5)
		}
		if e.left != nil && e.left.Before(last) {
			halves += 2 * int64(last.Sub(*e.left).Hours()/24+0.5)
		}
		l := Compute(p, Input{Month: r.Month, Basic: e.s.Basic, HRA: e.s.HRA, Special: e.s.Special, Other: e.s.Other, LOPHalves: halves,
			AdjEarn: adjEarn[e.id], AdjDed: adjDed[e.id], PF: e.s.PF, ESI: e.s.ESI, OldRegime: e.s.Regime == "old", HasPAN: e.s.PAN != nil,
			PT: pt[st], YTDGross: ytdGross[e.id], YTDTDS: ytdTDS[e.id]})
		warnings := l.Warnings
		if unmarked[e.id] > 0 {
			warnUnmarked++
			warnings = append(warnings, "unmarked_days")
		}
		if warnings == nil {
			warnings = []string{}
		}
		wj, _ := json.Marshal(warnings)
		if _, err := c.Tx.Exec(c.Context, `insert into payroll.run_lines (workspace_id, run_id, employee_id, name, code, pan, bank_account, ifsc, uan, esic_no, pt_state, days_in_month, lop_halves, unmarked_days,
			basic_minor, hra_minor, special_minor, other_minor, adj_earnings_minor, gross_minor, pf_wages_minor, epf_ee_minor, esi_wages_minor, esi_ee_minor, pt_minor, tds_minor,
			adj_deductions_minor, deductions_minor, net_minor, epf_er_minor, eps_er_minor, esi_er_minor, warnings)
			values ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33)`,
			c.WorkspaceID(), r.ID, e.id, e.name, e.code, e.s.PAN, e.s.Bank, e.s.IFSC, e.s.UAN, e.s.ESICNo, e.s.PTState, l.DaysInMonth, min(halves, 2*l.DaysInMonth), unmarked[e.id],
			l.Basic, l.HRA, l.Special, l.Other, adjEarn[e.id], l.Gross, l.PFWages, l.EPFEe, l.ESIWages, l.ESIEe, l.PT, l.TDS,
			adjDed[e.id], l.Deductions, l.Net, l.EPFEr, l.EPSEr, l.ESIEr, wj); err != nil {
			return command.DB(err)
		}
		gross += l.Gross
		ded += l.Deductions
		net += l.Net
		employer += l.EPFEr + l.EPSEr + l.ESIEr
	}

	var warn []string
	var missing int
	if err := c.Tx.QueryRow(c.Context, `select count(*) from hr.employees e where e.active and not exists (select 1 from payroll.salary_structures s where s.employee_id = e.id)`).Scan(&missing); err != nil {
		return command.DB(err)
	}
	if missing > 0 && r.Branch == nil {
		warn = append(warn, fmt.Sprintf("%d active employee(s) have no salary structure and are not in this run", missing))
	}
	if len(emps) == 0 {
		warn = append(warn, "no employee has a salary structure yet")
	}
	if warnUnmarked > 0 {
		warn = append(warn, fmt.Sprintf("%d employee(s) have working days with no attendance marked; those days are paid", warnUnmarked))
	}
	if warn == nil {
		warn = []string{}
	}
	wj, _ := json.Marshal(warn)
	_, err = c.Tx.Exec(c.Context, `update payroll.runs set status = 'draft', approved_by = null, approved_at = null, employees = $2, gross_minor = $3, deductions_minor = $4,
		net_minor = $5, employer_minor = $6, warnings = $7 where id = $1::uuid`, r.ID, len(emps), gross, ded, net, employer, wj)
	return command.DB(err)
}

const unpaid = `exists (select 1 from hr.leave_requests r join hr.leave_types t on t.id = r.leave_type_id where r.employee_id = a.employee_id and r.status = 'approved' and not t.paid and a.day between r.from_day and r.to_day)`

func mapInts(c *command.Ctx, into map[string]int64, sql string, args ...any) error {
	rows, err := c.Tx.Query(c.Context, sql, args...)
	if err != nil {
		return command.DB(err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var v int64
		if err := rows.Scan(&k, &v); err != nil {
			return command.DB(err)
		}
		into[k] = v
	}
	return command.DB(rows.Err())
}

// ---------- adjustments ----------

func adjust(c *command.Ctx, raw json.RawMessage) (any, error) {
	uid, err := human(c)
	if err != nil {
		return nil, err
	}
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if v.Kind != "earning" && v.Kind != "deduction" {
		return nil, command.ErrInvalid("kind must be earning or deduction")
	}
	if v.AmountMinor == nil || *v.AmountMinor <= 0 || *v.AmountMinor > 100_000_000_00 {
		return nil, command.ErrInvalid("amount_minor must be a positive number of paise")
	}
	if v.Reason == "" {
		return nil, command.ErrInvalid("a reason is required")
	}
	if v.EmployeeID == "" {
		return nil, command.ErrInvalid("employee_id must be a UUID")
	}
	r, err := lockRun(c, v.ID)
	if err != nil {
		return nil, err
	}
	if r.Status != "draft" && r.Status != "approved" {
		return nil, command.ErrConflict("a " + r.Status + " run cannot be adjusted")
	}
	var inRun bool
	if err := c.Tx.QueryRow(c.Context, "select exists (select 1 from payroll.run_lines where run_id = $1::uuid and employee_id = $2::uuid)", r.ID, v.EmployeeID).Scan(&inRun); err != nil {
		return nil, command.DB(err)
	}
	if !inRun {
		return nil, command.ErrNotFound("employee in this run")
	}
	if _, err := c.Tx.Exec(c.Context, "insert into payroll.adjustments (workspace_id, run_id, employee_id, kind, amount_minor, reason, created_by) values ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7::uuid)",
		c.WorkspaceID(), r.ID, v.EmployeeID, v.Kind, *v.AmountMinor, v.Reason, uid); err != nil {
		return nil, command.DB(err)
	}
	if err := computeRun(c, r); err != nil {
		return nil, err
	}
	if _, err := c.Emit("payroll.adjusted", nil, map[string]any{"run_id": r.ID, "employee_id": v.EmployeeID, "kind": v.Kind}); err != nil {
		return nil, err
	}
	return runGet(c, mustJSON(r.ID))
}

func adjustDelete(c *command.Ctx, raw json.RawMessage) (any, error) {
	if _, err := human(c); err != nil {
		return nil, err
	}
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if v.ID == "" {
		return nil, command.ErrInvalid("id must be a UUID")
	}
	var run string
	if err := c.Tx.QueryRow(c.Context, "select run_id::text from payroll.adjustments where id = $1::uuid", v.ID).Scan(&run); errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("adjustment")
	} else if err != nil {
		return nil, command.DB(err)
	}
	r, err := lockRun(c, run)
	if err != nil {
		return nil, err
	}
	if r.Status != "draft" && r.Status != "approved" {
		return nil, command.ErrConflict("a " + r.Status + " run cannot be adjusted")
	}
	if _, err := c.Tx.Exec(c.Context, "delete from payroll.adjustments where id = $1::uuid", v.ID); err != nil {
		return nil, command.DB(err)
	}
	if err := computeRun(c, r); err != nil {
		return nil, err
	}
	return runGet(c, mustJSON(r.ID))
}

// ---------- approve, finalise, pay, reverse ----------

func approve(c *command.Ctx, raw json.RawMessage) (any, error) {
	uid, err := human(c)
	if err != nil {
		return nil, err
	}
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	r, err := lockRun(c, v.ID)
	if err != nil {
		return nil, err
	}
	if r.Status == "approved" { // replay
		return runRow(c, r.ID)
	}
	if r.Status != "draft" {
		return nil, command.ErrConflict("only a draft run can be approved; this one is " + r.Status)
	}
	if err := checkLines(c, r.ID); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "update payroll.runs set status = 'approved', approved_by = $2::uuid, approved_at = now() where id = $1::uuid", r.ID, uid); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("payroll.approved", nil, map[string]any{"run_id": r.ID}); err != nil {
		return nil, err
	}
	return runRow(c, r.ID)
}

func checkLines(c *command.Ctx, run string) error {
	var n, neg int
	if err := c.Tx.QueryRow(c.Context, "select count(*), count(*) filter (where net_minor < 0) from payroll.run_lines where run_id = $1::uuid", run).Scan(&n, &neg); err != nil {
		return command.DB(err)
	}
	if n == 0 {
		return command.ErrConflict("the run has no employees")
	}
	if neg > 0 {
		return command.ErrConflict(fmt.Sprintf("%d employee(s) would get negative net pay; reduce their deductions first", neg))
	}
	return nil
}

func finalise(c *command.Ctx, raw json.RawMessage) (any, error) {
	uid, err := human(c)
	if err != nil {
		return nil, err
	}
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	r, err := lockRun(c, v.ID)
	if err != nil {
		return nil, err
	}
	if r.Status == "finalised" || r.Status == "paid" { // replay: posted once, never twice
		return runRow(c, r.ID)
	}
	if r.Status != "approved" {
		return nil, command.ErrConflict("approve the run before finalising it; this one is " + r.Status)
	}
	if err := checkLines(c, r.ID); err != nil {
		return nil, err
	}
	var gross, er, pf, esi, pt, tds, other, net int64
	if err := c.Tx.QueryRow(c.Context, `select coalesce(sum(gross_minor),0), coalesce(sum(epf_er_minor + eps_er_minor + esi_er_minor),0), coalesce(sum(epf_ee_minor + epf_er_minor + eps_er_minor),0),
		coalesce(sum(esi_ee_minor + esi_er_minor),0), coalesce(sum(pt_minor),0), coalesce(sum(tds_minor),0), coalesce(sum(adj_deductions_minor),0), coalesce(sum(net_minor),0)
		from payroll.run_lines where run_id = $1::uuid`, r.ID).Scan(&gross, &er, &pf, &esi, &pt, &tds, &other, &net); err != nil {
		return nil, command.DB(err)
	}
	for _, a := range []struct{ code, name, kind string }{{acctSalaries, "Salaries and wages", "expense"}, {acctEmployer, "Employer PF and ESI contributions", "expense"},
		{acctPF, "PF payable", "liability"}, {acctESI, "ESI payable", "liability"}, {acctPT, "Professional tax payable", "liability"}, {acctTDS, "TDS on salary payable", "liability"},
		{acctNet, "Net salary payable", "liability"}, {acctOther, "Other payroll deductions payable", "liability"}} {
		if err := ensureAccount(c, a.code, a.name, a.kind); err != nil {
			return nil, err
		}
	}
	memo := "Payroll " + r.Month.Format("Jan 2006")
	if err := billing.Post(c, "payroll", r.ID, memo, []billing.Leg{billing.L(acctSalaries, gross, 0), billing.L(acctEmployer, er, 0), billing.L(acctPF, 0, pf),
		billing.L(acctESI, 0, esi), billing.L(acctPT, 0, pt), billing.L(acctTDS, 0, tds), billing.L(acctOther, 0, other), billing.L(acctNet, 0, net)}); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "update payroll.runs set status = 'finalised', finalised_by = $2::uuid, finalised_at = now() where id = $1::uuid", r.ID, uid); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("payroll.finalised", nil, map[string]any{"run_id": r.ID, "month": r.Month.Format("2006-01")}); err != nil {
		return nil, err
	}
	return runRow(c, r.ID)
}

func markPaid(c *command.Ctx, raw json.RawMessage) (any, error) {
	uid, err := human(c)
	if err != nil {
		return nil, err
	}
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	r, err := lockRun(c, v.ID)
	if err != nil {
		return nil, err
	}
	if r.Status == "paid" {
		return runRow(c, r.ID)
	}
	if r.Status != "finalised" {
		return nil, command.ErrConflict("only a finalised run can be marked paid; this one is " + r.Status)
	}
	var net int64
	if err := c.Tx.QueryRow(c.Context, "select net_minor from payroll.runs where id = $1::uuid", r.ID).Scan(&net); err != nil {
		return nil, command.DB(err)
	}
	if err := billing.Post(c, "payroll_payment", r.ID, "Salary paid "+r.Month.Format("Jan 2006"), []billing.Leg{billing.L(acctNet, net, 0), billing.L(billing.AcctBank, 0, net)}); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "update payroll.runs set status = 'paid', paid_by = $2::uuid, paid_at = now(), paid_reference = nullif($3,'') where id = $1::uuid", r.ID, uid, v.Reference); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("payroll.paid", nil, map[string]any{"run_id": r.ID}); err != nil {
		return nil, err
	}
	return runRow(c, r.ID)
}

// reverse is the owner's undo of a finalised or paid run: the journal is append-only, so each posting gets a mirror entry.
func reverse(c *command.Ctx, raw json.RawMessage) (any, error) {
	uid, err := human(c)
	if err != nil {
		return nil, err
	}
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	r, err := lockRun(c, v.ID)
	if err != nil {
		return nil, err
	}
	if r.Status == "reversed" {
		return runRow(c, r.ID)
	}
	if r.Status != "finalised" && r.Status != "paid" {
		return nil, command.ErrConflict("only a finalised or paid run can be reversed; delete a draft instead")
	}
	if v.Reason == "" {
		return nil, command.ErrInvalid("a reason is required")
	}
	memo := "Reversal of payroll " + r.Month.Format("Jan 2006") + ": " + v.Reason
	if r.Status == "paid" {
		if err := mirror(c, "payroll_payment", r.ID, memo); err != nil {
			return nil, err
		}
	}
	if err := mirror(c, "payroll", r.ID, memo); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "update payroll.runs set status = 'reversed', reversed_by = $2::uuid, reversed_at = now(), reverse_reason = $3 where id = $1::uuid", r.ID, uid, v.Reason); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("payroll.reversed", nil, map[string]any{"run_id": r.ID, "month": r.Month.Format("2006-01")}); err != nil {
		return nil, err
	}
	return runRow(c, r.ID)
}

func mirror(c *command.Ctx, source, run, memo string) error {
	rows, err := c.Tx.Query(c.Context, `select l.account_code, l.debit_minor, l.credit_minor from billing.journal_lines l join billing.journal_entries e on e.id = l.entry_id
		where e.source_type = $1 and e.source_id = $2::uuid`, source, run)
	if err != nil {
		return command.DB(err)
	}
	var legs []billing.Leg
	for rows.Next() {
		var code string
		var d, cr int64
		if err := rows.Scan(&code, &d, &cr); err != nil {
			rows.Close()
			return command.DB(err)
		}
		legs = append(legs, billing.L(code, cr, d))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return command.DB(err)
	}
	return billing.Post(c, source+"_reversal", run, memo, legs)
}
