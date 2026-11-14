package payroll

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
)

func rupees(minor int64) string { return fmt.Sprintf("%d.%02d", minor/100, minor%100) }

// csvSafe stops a name from turning into a spreadsheet formula.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func toCSV(rows [][]string) string {
	var sb strings.Builder
	w := csv.NewWriter(&sb)
	_ = w.WriteAll(rows)
	return sb.String()
}

func s(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// payoutCSV is the bank upload file of a finalised run: name, account, IFSC, amount, reference.
func payoutCSV(c *command.Ctx, raw json.RawMessage) (any, error) {
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
	var status, month string
	if err := c.Tx.QueryRow(c.Context, "select status, to_char(month, 'YYYY-MM') from payroll.runs where id = $1::uuid", v.ID).Scan(&status, &month); errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("payroll run")
	} else if err != nil {
		return nil, command.DB(err)
	}
	if status != "finalised" && status != "paid" {
		return nil, command.ErrConflict("the payout file is for a finalised run; this one is " + status)
	}
	rows, err := c.Tx.Query(c.Context, "select name, code, bank_account, ifsc, net_minor from payroll.run_lines where run_id = $1::uuid and net_minor > 0 order by name", v.ID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := [][]string{{"Employee code", "Name", "Account number", "IFSC", "Amount (INR)", "Reference"}}
	var missing []string
	var total int64
	for rows.Next() {
		var name string
		var code, acct, ifsc *string
		var net int64
		if err := rows.Scan(&name, &code, &acct, &ifsc, &net); err != nil {
			return nil, command.DB(err)
		}
		if acct == nil || ifsc == nil {
			missing = append(missing, name)
			continue
		}
		total += net
		out = append(out, []string{csvSafe(s(code)), csvSafe(name), *acct, *ifsc, rupees(net), "SAL-" + month + "-" + csvSafe(s(code))})
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("payroll.payout_exported", nil, map[string]any{"run_id": v.ID, "rows": len(out) - 1}); err != nil {
		return nil, err
	}
	return map[string]any{"filename": "salary-payout-" + month + ".csv", "csv": toCSV(out), "rows": len(out) - 1, "total_minor": total, "missing_bank_details": missing}, nil
}

// statutory: month figures of finalised runs, for the CA to file. Exports only; nothing is filed from here.
func statutory(c *command.Ctx, raw json.RawMessage) (any, error) {
	if _, err := human(c); err != nil {
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
	ms := month.Format("2006-01")
	fy := month.Year()
	if month.Month() < 4 {
		fy--
	}
	rows, err := c.Tx.Query(c.Context, `select l.name, l.pan, l.uan, l.esic_no, l.pt_state, l.gross_minor, l.pf_wages_minor, l.epf_ee_minor, l.epf_er_minor, l.eps_er_minor,
		l.esi_wages_minor, l.esi_ee_minor, l.esi_er_minor, l.pt_minor, l.tds_minor, l.lop_halves, l.days_in_month,
		coalesce((select sum(y.gross_minor) from payroll.run_lines y join payroll.runs q on q.id = y.run_id where y.employee_id = l.employee_id and q.status in ('finalised','paid')
		  and q.month >= make_date($2, 4, 1) and q.month <= r.month), 0),
		coalesce((select sum(y.tds_minor) from payroll.run_lines y join payroll.runs q on q.id = y.run_id where y.employee_id = l.employee_id and q.status in ('finalised','paid')
		  and q.month >= make_date($2, 4, 1) and q.month <= r.month), 0)
		from payroll.run_lines l join payroll.runs r on r.id = l.run_id where r.month = $1::date and r.status in ('finalised','paid') order by l.name`, month, fy)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	type pt struct {
		code  string
		n     int
		total int64
	}
	ptBy := map[string]*pt{}
	var table [][]string
	var data []map[string]any
	totals := map[string]int64{}
	for rows.Next() {
		var name string
		var pan, uan, esic, state *string
		var gross, pfw, epfEe, epfEr, eps, esiW, esiEe, esiEr, ptx, tds, ytdGross, ytdTDS int64
		var halves, days int64
		if err := rows.Scan(&name, &pan, &uan, &esic, &state, &gross, &pfw, &epfEe, &epfEr, &eps, &esiW, &esiEe, &esiEr, &ptx, &tds, &halves, &days, &ytdGross, &ytdTDS); err != nil {
			return nil, command.DB(err)
		}
		ncp := (halves + 1) / 2
		switch v.Kind {
		case "pf":
			if pfw == 0 {
				continue
			}
			totals["epf_ee_minor"] += epfEe
			totals["eps_minor"] += eps
			totals["epf_er_minor"] += epfEr
			table = append(table, []string{s(uan), csvSafe(name), rupees(gross), rupees(pfw), rupees(pfw), rupees(pfw), rupees(epfEe), rupees(eps), rupees(epfEr), fmt.Sprint(ncp), "0.00"})
			data = append(data, map[string]any{"uan": uan, "name": name, "gross_minor": gross, "epf_wages_minor": pfw, "epf_ee_minor": epfEe, "eps_minor": eps, "epf_er_minor": epfEr, "ncp_days": ncp})
		case "esi":
			if esiW == 0 {
				continue
			}
			totals["esi_ee_minor"] += esiEe
			totals["esi_er_minor"] += esiEr
			table = append(table, []string{s(esic), csvSafe(name), fmt.Sprint(days - ncp), rupees(esiW), rupees(esiEe), rupees(esiEr)})
			data = append(data, map[string]any{"esic_no": esic, "name": name, "days": days - ncp, "wages_minor": esiW, "esi_ee_minor": esiEe, "esi_er_minor": esiEr})
		case "pt":
			if ptx == 0 {
				continue
			}
			k := s(state)
			if ptBy[k] == nil {
				ptBy[k] = &pt{code: k}
			}
			ptBy[k].n++
			ptBy[k].total += ptx
		case "tds":
			totals["tds_minor"] += tds
			table = append(table, []string{s(pan), csvSafe(name), rupees(gross), rupees(tds), rupees(ytdGross), rupees(ytdTDS)})
			data = append(data, map[string]any{"pan": pan, "name": name, "gross_minor": gross, "tds_minor": tds, "ytd_gross_minor": ytdGross, "ytd_tds_minor": ytdTDS})
		default:
			return nil, command.ErrInvalid("kind must be pf, esi, pt or tds")
		}
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	var head []string
	switch v.Kind {
	case "pf":
		head = []string{"UAN", "Member name", "Gross wages", "EPF wages", "EPS wages", "EDLI wages", "EPF contribution (employee)", "EPS contribution (employer)", "EPF-EPS difference (employer)", "NCP days", "Refund of advances"}
	case "esi":
		head = []string{"IP number", "IP name", "Days worked", "Total wages", "Employee contribution", "Employer contribution"}
	case "tds":
		head = []string{"PAN", "Employee name", "Gross salary this month", "TDS this month", "Gross salary FY to date", "TDS FY to date"}
	case "pt":
		head = []string{"State code", "Employees", "Professional tax"}
		for _, p := range ptBy {
			table = append(table, []string{p.code, fmt.Sprint(p.n), rupees(p.total)})
			data = append(data, map[string]any{"state_code": p.code, "employees": p.n, "pt_minor": p.total})
			totals["pt_minor"] += p.total
		}
	}
	if data == nil {
		data = []map[string]any{}
	}
	if _, err := c.Emit("payroll.statutory_exported", nil, map[string]any{"month": ms, "kind": v.Kind, "rows": len(table)}); err != nil {
		return nil, err
	}
	return map[string]any{"month": ms, "kind": v.Kind, "rows": data, "totals": totals, "csv": toCSV(append([][]string{head}, table...)), "filename": v.Kind + "-" + ms + ".csv",
		"note": "Export for your CA. Figures come from finalised runs only; nothing is filed from here."}, nil
}

// ---------- payslips ----------

// canReadAll: an admin with finance access may open anyone's payslip; everyone else only their own.
func canReadAll(c *command.Ctx) (bool, error) {
	if c.Principal.Role < command.Admin {
		return false, nil
	}
	return c.FinanceAllowed()
}

func payslipList(c *command.Ctx, raw json.RawMessage) (any, error) {
	uid, err := human(c)
	if err != nil {
		return nil, err
	}
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	all, err := canReadAll(c)
	if err != nil {
		return nil, err
	}
	if v.EmployeeID != "" && !all {
		return nil, command.ErrForbidden("you can only see your own payslips")
	}
	rows, err := c.Tx.Query(c.Context, `select l.id::text, l.employee_id::text, l.name, to_char(r.month, 'YYYY-MM'), r.status, l.gross_minor, l.deductions_minor, l.net_minor
		from payroll.run_lines l join payroll.runs r on r.id = l.run_id join hr.employees e on e.id = l.employee_id
		where r.status in ('finalised','paid') and (case when $2 <> '' then l.employee_id = $2::uuid else e.user_id = $1::uuid end) order by r.month desc limit 120`, uid, v.EmployeeID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, emp, name, month, status string
		var g, d, n int64
		if err := rows.Scan(&id, &emp, &name, &month, &status, &g, &d, &n); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, map[string]any{"id": id, "employee_id": emp, "name": name, "month": month, "run_status": status, "gross_minor": g, "deductions_minor": d, "net_minor": n})
	}
	return out, command.DB(rows.Err())
}

func payslipGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	uid, err := human(c)
	if err != nil {
		return nil, err
	}
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if v.ID == "" {
		return nil, command.ErrInvalid("id must be a UUID")
	}
	all, err := canReadAll(c)
	if err != nil {
		return nil, err
	}
	var m = map[string]any{}
	var name, month, status string
	var code, pan, bank, ifsc, uan, esic, ptState *string
	var days, halves, unmarked int64
	var n [20]int64
	err = c.Tx.QueryRow(c.Context, `select l.id::text, l.name, l.code, l.pan, l.bank_account, l.ifsc, l.uan, l.esic_no, l.pt_state, to_char(r.month, 'YYYY-MM'), r.status, l.days_in_month, l.lop_halves, l.unmarked_days,
		l.basic_minor, l.hra_minor, l.special_minor, l.other_minor, l.adj_earnings_minor, l.gross_minor, l.pf_wages_minor, l.epf_ee_minor, l.esi_ee_minor, l.pt_minor, l.tds_minor,
		l.adj_deductions_minor, l.deductions_minor, l.net_minor, l.epf_er_minor, l.eps_er_minor, l.esi_er_minor
		from payroll.run_lines l join payroll.runs r on r.id = l.run_id join hr.employees e on e.id = l.employee_id
		where l.id = $1::uuid and r.status in ('finalised','paid') and ($3 or e.user_id = $2::uuid)`, v.ID, uid, all).Scan(&v.ID, &name, &code, &pan, &bank, &ifsc, &uan, &esic, &ptState, &month, &status, &days, &halves, &unmarked,
		&n[0], &n[1], &n[2], &n[3], &n[4], &n[5], &n[6], &n[7], &n[8], &n[9], &n[10], &n[11], &n[12], &n[13], &n[14], &n[15], &n[16])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("payslip")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	var adjustments json.RawMessage
	if err := c.Tx.QueryRow(c.Context, `select coalesce(jsonb_agg(jsonb_build_object('kind', a.kind, 'amount_minor', a.amount_minor, 'reason', a.reason) order by a.created_at), '[]'::jsonb)
		from payroll.adjustments a join payroll.run_lines l on l.run_id = a.run_id and l.employee_id = a.employee_id where l.id = $1::uuid`, v.ID).Scan(&adjustments); err != nil {
		return nil, command.DB(err)
	}
	m["adjustments"] = adjustments
	var company string
	var gstin *string
	if err := c.Tx.QueryRow(c.Context, "select name, settings->>'gstin' from core.workspaces where id = $1::uuid", c.WorkspaceID()).Scan(&company, &gstin); err != nil {
		return nil, command.DB(err)
	}
	for k, x := range map[string]any{"id": v.ID, "month": month, "run_status": status, "employee_name": name, "employee_code": code, "pan": mask(pan), "uan": mask(uan), "esic_no": mask(esic),
		"bank_account": mask(bank), "ifsc": ifsc, "pt_state": ptState, "days_in_month": days, "lop_days": float64(halves) / 2, "company": company, "company_gstin": gstin,
		"basic_minor": n[0], "hra_minor": n[1], "special_minor": n[2], "other_minor": n[3], "adj_earnings_minor": n[4], "gross_minor": n[5], "pf_wages_minor": n[6],
		"epf_ee_minor": n[7], "esi_ee_minor": n[8], "pt_minor": n[9], "tds_minor": n[10], "adj_deductions_minor": n[11], "deductions_minor": n[12], "net_minor": n[13],
		"epf_er_minor": n[14], "eps_er_minor": n[15], "esi_er_minor": n[16]} {
		m[k] = x
	}
	return m, nil
}
