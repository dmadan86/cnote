package server_test

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// WP25 payroll. Statutory arithmetic has its own unit tests in modules/payroll; these cover the workflow, the
// permission matrix, the journal and workspace isolation.

func rup(r int64) int64 { return r * 100 }

func prevMonth() (string, time.Time) {
	t := todayIST()
	m := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	return m.Format("2006-01"), m
}

func (b *bt) structure(emp string, extra map[string]any) {
	b.t.Helper()
	in := map[string]any{"employee_id": emp, "basic_minor": rup(25000), "hra_minor": rup(10000), "special_minor": rup(15000), "pt_state": "29",
		"pan": "ABCDE1234F", "bank_account": "123456789012", "ifsc": "HDFC0001234", "pf_applicable": true}
	for k, v := range extra {
		in[k] = v
	}
	b.ok("payroll.structure_set", in)
}

func (b *bt) payLines(run map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, l := range run["lines"].([]any) {
		m := l.(map[string]any)
		out[m["name"].(string)] = m
	}
	return out
}

func (b *bt) sum(sql string) int64 {
	b.t.Helper()
	v, err := strconv.ParseInt(b.dbStrings(b.owner.ws, sql)[0], 10, 64)
	if err != nil {
		b.t.Fatal(err)
	}
	return v
}

func (b *bt) pacct(code string) int64 {
	return b.sum("select coalesce(sum(debit_minor - credit_minor),0)::text from billing.journal_lines where account_code = '" + code + "'")
}

func TestPayrollRunFullLifecycle(t *testing.T) {
	b := newBT(t, false)
	mina := b.memberAs("Mina", "member")
	b.okAs(mina, "attendance.check_in", map[string]any{}) // makes the employee record
	emp := b.emp(mina.userID)
	month, mstart := prevMonth()
	days := int64(mstart.AddDate(0, 1, -1).Day())
	b.structure(emp, nil)

	// two absent days: loss of pay is pro-rata on the calendar days of the month
	for _, d := range []int{1, 2} {
		b.ok("attendance.mark", map[string]any{"employee_id": emp, "day": mstart.AddDate(0, 0, d-1).Format("2006-01-02"), "status": "absent"})
	}
	run := b.ok("payroll.run_create", map[string]any{"month": month})
	id := run["id"].(string)
	eq(t, run["status"], "draft")
	l := b.payLines(run)["Mina"]
	pay := func(x int64) int64 { return (x*(2*days-4) + days) / (2 * days) }
	eq(t, n(l["basic_minor"]), pay(rup(25000)))
	eq(t, n(l["gross_minor"]), pay(rup(25000))+pay(rup(10000))+pay(rup(15000)))
	eq(t, l["lop_days"], float64(2))
	eq(t, n(l["epf_ee_minor"]), rup(1800)) // wages above the ceiling
	wantPT := rup(200)
	if mstart.Month() == time.February {
		wantPT = rup(300)
	}
	eq(t, n(l["pt_minor"]), wantPT)
	eq(t, n(l["net_minor"]), n(l["gross_minor"])-n(l["deductions_minor"]))
	if len(run["warnings"].([]any)) == 0 { // the owner has no structure
		t.Errorf("warnings: %v", run["warnings"])
	}

	// the month is run once
	b.errIs("payroll.run_create", map[string]any{"month": month}, 409, "conflict")
	b.errIs("payroll.run_create", map[string]any{"month": time.Now().AddDate(1, 0, 0).Format("2006-01")}, 400, "invalid")
	b.errIs("payroll.run_create", map[string]any{"month": "April"}, 400, "invalid")

	// review: one-off earning and deduction with a reason
	before := n(l["net_minor"])
	b.ok("payroll.adjust", map[string]any{"id": id, "employee_id": emp, "kind": "earning", "amount_minor": rup(1000), "reason": "Diwali gift"})
	run = b.ok("payroll.adjust", map[string]any{"id": id, "employee_id": emp, "kind": "deduction", "amount_minor": rup(400), "reason": "Advance recovery"})
	l = b.payLines(run)["Mina"]
	eq(t, n(l["net_minor"]), before+rup(1000)-rup(400)) // PF, ESI and PT unchanged; no tax at this pay
	eq(t, len(run["adjustments"].([]any)), 2)
	b.errIs("payroll.adjust", map[string]any{"id": id, "employee_id": emp, "kind": "earning", "amount_minor": 5, "reason": ""}, 400, "invalid")
	b.errIs("payroll.adjust", map[string]any{"id": id, "employee_id": emp, "kind": "bonus", "amount_minor": 5, "reason": "x"}, 400, "invalid")
	b.errIs("payroll.adjust", map[string]any{"id": id, "employee_id": b.emp(b.owner.userID), "kind": "earning", "amount_minor": 5, "reason": "x"}, 404, "not_found")
	net := n(l["net_minor"])

	// finalising needs an approval first
	b.errIs("payroll.finalise", map[string]any{"id": id}, 409, "conflict")
	b.errIs("payroll.payout_csv", map[string]any{"id": id}, 409, "conflict")
	eq(t, b.ok("payroll.approve", map[string]any{"id": id})["status"], "approved")
	fin := b.ok("payroll.finalise", map[string]any{"id": id})
	eq(t, fin["status"], "finalised")
	eq(t, n(fin["net_minor"]), net)
	again := b.ok("payroll.finalise", map[string]any{"id": id}) // replay posts nothing twice
	eq(t, again["status"], "finalised")
	eq(t, b.count("select count(*)::text from billing.journal_entries where source_type = 'payroll'"), "1")

	// the books: expense = gross, employer contribution, payables, net salary payable
	gross := n(l["gross_minor"])
	eq(t, b.pacct("5300"), gross)
	eq(t, b.pacct("5310"), n(l["epf_er_minor"])+n(l["eps_er_minor"])+n(l["esi_er_minor"]))
	eq(t, b.pacct("2510"), -(n(l["epf_ee_minor"]) + n(l["epf_er_minor"]) + n(l["eps_er_minor"])))
	eq(t, b.pacct("2530"), -n(l["pt_minor"]))
	eq(t, b.pacct("2560"), -rup(400))
	eq(t, b.pacct("2550"), -net)

	// a finalised month is locked
	b.errIs("payroll.run_create", map[string]any{"month": month}, 409, "conflict")
	b.errIs("payroll.run_recompute", map[string]any{"id": id}, 409, "conflict")
	b.errIs("payroll.adjust", map[string]any{"id": id, "employee_id": emp, "kind": "earning", "amount_minor": 5, "reason": "x"}, 409, "conflict")
	b.errIs("payroll.run_delete", map[string]any{"id": id}, 409, "conflict")

	// the bank file
	csv := b.ok("payroll.payout_csv", map[string]any{"id": id})
	if s := csv["csv"].(string); !strings.Contains(s, "123456789012,HDFC0001234,") || !strings.HasPrefix(s, "Employee code,Name,Account number,IFSC,Amount (INR),Reference") {
		t.Errorf("payout csv:\n%s", s)
	}
	eq(t, n(csv["total_minor"]), net)

	// payment
	paid := b.ok("payroll.mark_paid", map[string]any{"id": id, "reference": "NEFT-1"})
	eq(t, paid["status"], "paid")
	b.ok("payroll.mark_paid", map[string]any{"id": id})
	eq(t, b.pacct("2550"), int64(0))
	eq(t, b.pacct("1200"), -net)
	eq(t, b.count("select count(*)::text from billing.journal_entries where source_type = 'payroll_payment'"), "1")

	// statutory exports
	pf := b.ok("payroll.statutory", map[string]any{"month": month, "kind": "pf"})
	if !strings.Contains(pf["csv"].(string), "Mina") || !strings.Contains(pf["csv"].(string), "UAN,Member name") {
		t.Errorf("pf: %v", pf["csv"])
	}
	eq(t, n(pf["totals"].(map[string]any)["epf_ee_minor"]), rup(1800))
	pt := b.ok("payroll.statutory", map[string]any{"month": month, "kind": "pt"})
	eq(t, n(pt["totals"].(map[string]any)["pt_minor"]), wantPT)
	eq(t, len(b.ok("payroll.statutory", map[string]any{"month": month, "kind": "esi"})["rows"].([]any)), 0) // not covered above the ceiling
	tds := b.ok("payroll.statutory", map[string]any{"month": month, "kind": "tds"})
	if !strings.Contains(tds["csv"].(string), "ABCDE1234F") {
		t.Errorf("tds: %v", tds["csv"])
	}
	b.errIs("payroll.statutory", map[string]any{"month": month, "kind": "gst"}, 400, "invalid")

	// payslips: the member sees only their own, with PAN and bank masked
	list := b.okAsList(mina, "payslip.list", map[string]any{})
	eq(t, len(list), 1)
	slip := b.okAs(mina, "payslip.get", map[string]any{"id": list[0]["id"]})
	eq(t, slip["pan"], "******234F")
	eq(t, slip["bank_account"], "********9012")
	eq(t, n(slip["net_minor"]), net)
	eq(t, len(slip["adjustments"].([]any)), 2)
	other := b.memberAs("Omar", "member")
	b.errAs(other, "payslip.get", map[string]any{"id": list[0]["id"]}, 404, "not_found")
	eq(t, len(b.okAsList(other, "payslip.list", map[string]any{})), 0)
	b.errAs(other, "payslip.list", map[string]any{"employee_id": emp}, 403, "forbidden")
	eq(t, len(b.okAsList(b.owner, "payslip.list", map[string]any{"employee_id": emp})), 1)

	// reversal: only an owner, with a reason, with mirror entries; the month can be run again
	admin := b.memberAs("Ada", "admin")
	b.errAs(admin, "payroll.reverse", map[string]any{"id": id, "reason": "mistake"}, 403, "forbidden")
	b.errIs("payroll.reverse", map[string]any{"id": id}, 400, "invalid")
	rev := b.ok("payroll.reverse", map[string]any{"id": id, "reason": "wrong attendance"})
	eq(t, rev["status"], "reversed")
	b.ok("payroll.reverse", map[string]any{"id": id, "reason": "again"}) // replay
	for _, c := range []string{"5300", "5310", "2510", "2520", "2530", "2540", "2550", "2560", "1200"} {
		eq(t, b.pacct(c), int64(0))
	}
	eq(t, b.count("select count(*)::text from billing.journal_entries where source_type like 'payroll%reversal'"), "2")
	eq(t, len(b.okAsList(mina, "payslip.list", map[string]any{})), 0) // a reversed payslip is gone
	again2 := b.ok("payroll.run_create", map[string]any{"month": month})
	eq(t, again2["status"], "draft")
	b.ok("payroll.run_delete", map[string]any{"id": again2["id"]})
}

func TestPayrollRoleMatrixAndIsolation(t *testing.T) {
	b := newBT(t, false)
	mina := b.memberAs("Mina", "member")
	b.okAs(mina, "attendance.check_in", map[string]any{})
	emp := b.emp(mina.userID)
	b.structure(emp, nil)
	month, _ := prevMonth()
	run := b.ok("payroll.run_create", map[string]any{"month": month})
	id := run["id"].(string)

	admin := b.memberAs("Ada", "admin")
	finMember := b.memberAs("Fin", "member")
	b.ok("member.update", map[string]any{"user_id": finMember.userID, "finance_access": true})
	guest := b.memberAs("Gus", "guest")
	b.enableGroups("read", "write_notes", "admin", "destructive", "manage_invoices")
	agent := b.agentKey("payroll-bot")
	key := b.apiKey(b.owner)

	cmds := map[string]map[string]any{
		"payroll.structure_list": {}, "payroll.structure_get": {"employee_id": emp}, "payroll.structure_set": {"employee_id": emp, "basic_minor": 100},
		"payroll.config": {}, "payroll.run_create": {"month": month}, "payroll.run_recompute": {"id": id}, "payroll.run_list": {}, "payroll.run_get": {"id": id},
		"payroll.adjust": {"id": id, "employee_id": emp, "kind": "earning", "amount_minor": 1, "reason": "x"}, "payroll.approve": {"id": id}, "payroll.finalise": {"id": id},
		"payroll.mark_paid": {"id": id}, "payroll.reverse": {"id": id, "reason": "x"}, "payroll.payout_csv": {"id": id}, "payroll.statutory": {"month": month, "kind": "pf"},
		"payroll.run_delete": {"id": id}, "payroll.adjust_delete": {"id": id},
	}
	for name, in := range cmds {
		for who, w := range map[string]who{"member": mina, "member with finance access": finMember, "guest": guest, "agent": agent, "api key": key} {
			if r := b.call(w, name, in); r.status != 403 {
				t.Errorf("%s as %s: want 403, got %d %s", name, who, r.status, r.body)
			}
		}
	}
	for _, w := range []who{agent, key, guest} { // payslips are people's too
		b.errAs(w, "payslip.list", map[string]any{}, 403, "forbidden")
	}
	// an admin may read and prepare, but only the owner reverses
	b.okAs(admin, "payroll.run_get", map[string]any{"id": id})
	eq(t, len(b.okAsList(admin, "payroll.structure_list", map[string]any{})) >= 2, true)
	full := b.okAs(admin, "payroll.structure_get", map[string]any{"employee_id": emp})
	eq(t, full["pan"], "ABCDE1234F")
	masked := b.okAsList(admin, "payroll.structure_list", map[string]any{})
	for _, r := range masked {
		if s, _ := r["structure"].(map[string]any); s != nil {
			eq(t, s["pan"], "******234F")
			eq(t, s["bank_account"], "********9012")
		}
	}
	b.errAs(admin, "payroll.reverse", map[string]any{"id": id, "reason": "x"}, 403, "forbidden")

	// validation
	for name, in := range map[string]map[string]any{
		"bad pan": {"pan": "abc"}, "bad ifsc": {"ifsc": "HDFC123"}, "bad account": {"bank_account": "12"}, "ifsc without account": {"bank_account": "", "ifsc": "HDFC0001234"},
		"bad regime": {"regime": "x"}, "negative": {"hra_minor": -1}, "bad state": {"pt_state": "KA"},
	} {
		in["employee_id"] = emp
		if r := b.call(b.owner, "payroll.structure_set", in); r.status != 400 {
			t.Errorf("%s: want 400, got %d %s", name, r.status, r.body)
		}
	}
	b.errIs("payroll.structure_set", map[string]any{"employee_id": b.emp(b.owner.userID), "hra_minor": 5}, 400, "invalid") // basic is required

	// another workspace sees none of it
	o := newBT(t, false)
	o.errIs("payroll.run_get", map[string]any{"id": id}, 404, "not_found")
	o.errIs("payroll.structure_get", map[string]any{"employee_id": emp}, 404, "not_found")
	o.errIs("payroll.adjust", map[string]any{"id": id, "employee_id": emp, "kind": "earning", "amount_minor": 1, "reason": "x"}, 404, "not_found")
	o.errIs("payroll.structure_set", map[string]any{"employee_id": emp, "basic_minor": 100}, 404, "not_found")
	eq(t, len(o.okList("payroll.run_list", map[string]any{})), 0)
	o.errIs("payslip.get", map[string]any{"id": id}, 404, "not_found")
}

func TestPayrollApprovalRuleHoldsApproveForAnOwner(t *testing.T) {
	b := newBT(t, false)
	mina := b.memberAs("Mina", "member")
	b.okAs(mina, "attendance.check_in", map[string]any{})
	b.structure(b.emp(mina.userID), nil)
	month, _ := prevMonth()
	id := b.ok("payroll.run_create", map[string]any{"month": month})["id"].(string)
	admin := b.memberAs("Ada", "admin")
	b.rule(map[string]any{"command": "payroll.approve", "min_amount_minor": 1})

	r := b.okAs(admin, "payroll.approve", map[string]any{"id": id})
	eq(t, r["approval_required"], true)
	aid := r["confirmation_id"].(string)
	eq(t, b.okAs(admin, "payroll.approve", map[string]any{"id": id})["confirmation_id"], aid) // no second request
	eq(t, b.ok("payroll.run_get", map[string]any{"id": id})["status"], "draft")
	b.errAs(admin, "payroll.finalise", map[string]any{"id": id}, 409, "conflict")
	b.errAs(admin, "approval.decide", map[string]any{"id": aid, "decision": "approve"}, 403, "forbidden") // not your own request

	// a recompute after the request changes the amount: the old request expires instead of approving something else
	b.ok("payroll.adjust", map[string]any{"id": id, "employee_id": b.emp(mina.userID), "kind": "earning", "amount_minor": 100, "reason": "x"})
	d := b.ok("approval.decide", map[string]any{"id": aid, "decision": "approve"})
	eq(t, d["status"], "expired")
	eq(t, b.ok("payroll.run_get", map[string]any{"id": id})["status"], "draft")

	r = b.okAs(admin, "payroll.approve", map[string]any{"id": id})
	eq(t, r["approval_required"], true)
	d = b.ok("approval.decide", map[string]any{"id": r["confirmation_id"], "decision": "approve"})
	eq(t, d["status"], "approved")
	eq(t, b.ok("payroll.run_get", map[string]any{"id": id})["status"], "approved")
	// agents are refused outright above the threshold, and never reach payroll anyway
	eq(t, b.ok("payroll.finalise", map[string]any{"id": id})["status"], "finalised")
}

func TestPayrollDPDPExportAndErase(t *testing.T) {
	b := newBT(t, false)
	mina := b.memberAs("Mina", "member")
	b.okAs(mina, "attendance.check_in", map[string]any{})
	emp := b.emp(mina.userID)
	b.structure(emp, nil)
	month, _ := prevMonth()
	id := b.ok("payroll.run_create", map[string]any{"month": month})["id"].(string)
	b.ok("payroll.approve", map[string]any{"id": id})
	b.ok("payroll.finalise", map[string]any{"id": id})

	exp := b.ok("data.export_subject", map[string]any{"user_id": mina.userID})["data"].(map[string]any)
	eq(t, len(exp["salary_structure"].([]any)), 1)
	eq(t, len(exp["payslips"].([]any)), 1)
	eq(t, exp["salary_structure"].([]any)[0].(map[string]any)["bank_account"], "123456789012")

	b.errIs("data.erase_subject", map[string]any{"user_id": mina.userID}, 400, "invalid") // reason first
	er := b.ok("data.erase_subject", map[string]any{"user_id": mina.userID, "reason": "DPDP request"})
	eq(t, er["status"], "done")
	if !strings.Contains(er["message"].(string), "statutory") {
		t.Errorf("response must say what was kept: %v", er)
	}
	row := b.dbStrings(b.owner.ws, "select concat_ws('|', pan, bank_account, ifsc, uan, esic_no, basic_minor::text) from payroll.salary_structures")[0]
	eq(t, row, "2500000")
	line := b.dbStrings(b.owner.ws, "select concat_ws('|', pan, bank_account, ifsc, net_minor > 0) from payroll.run_lines")[0]
	eq(t, line, "t")
	// the payslip still opens (figures kept), the identifiers are gone
	list := b.okAsList(mina, "payslip.list", map[string]any{})
	slip := b.okAs(mina, "payslip.get", map[string]any{"id": list[0]["id"]})
	eq(t, slip["pan"], nil)
	eq(t, slip["bank_account"], nil)
	if n(slip["gross_minor"]) == 0 {
		t.Error("figures must be kept")
	}
	// a member with no payroll data still cannot be erased here
	other := b.memberAs("Omar", "member")
	b.errIs("data.erase_subject", map[string]any{"user_id": other.userID, "reason": "x"}, 400, "invalid")
}

func TestPayrollConfigAndMissingPT(t *testing.T) {
	b := newBT(t, false)
	cfg := b.ok("payroll.config", map[string]any{"month": "2026-06"})
	eq(t, len(cfg["pt_slabs"].([]any)) >= 19, true)
	eq(t, len(cfg["tax_slabs"].([]any)), 7)
	for _, p := range cfg["params"].([]any) {
		if strings.TrimSpace(p.(map[string]any)["note"].(string)) == "" || p.(map[string]any)["source"].(string) == "" {
			t.Errorf("every statutory value carries a source and a note: %v", p)
		}
	}
	// a state without a table: no PT and a visible warning
	mina := b.memberAs("Mina", "member")
	b.okAs(mina, "attendance.check_in", map[string]any{})
	b.structure(b.emp(mina.userID), map[string]any{"pt_state": "07"})
	month, _ := prevMonth()
	run := b.ok("payroll.run_create", map[string]any{"month": month})
	l := b.payLines(run)["Mina"]
	eq(t, n(l["pt_minor"]), int64(0))
	if !strings.Contains(mustJSON(l["warnings"]), "no_pt_state") {
		t.Errorf("want a configure-PT warning: %v", l["warnings"])
	}
}
