package server_test

import (
	"strings"
	"testing"
)

// Report builder: report.* and dashboard.* commands (Go only). Seeds a workspace with invoices, deals and tasks.

type rb struct {
	*bt
	member, noFin, guest who
	c1, c2               string
	crm, eng             string
}

func newRB(t *testing.T) *rb {
	b := newBT(t, false)
	r := &rb{bt: b}
	r.member = b.memberAs("Mia", "member")
	r.noFin = b.memberAs("Nolan", "member")
	r.guest = b.memberAs("Gus", "guest")
	b.ok("member.update", map[string]any{"user_id": r.noFin.userID, "finance_access": false})
	r.c1, r.c2 = b.contact, b.newContact("27")
	r.crm = b.must(b.owner, "space.create", map[string]any{"key": "SAL", "name": "Sales"})["id"].(string)
	r.eng = b.must(b.owner, "space.create", map[string]any{"key": "ENG", "name": "Eng", "template": "software"})["id"].(string)
	return r
}

func (r *rb) invoice(contact string, offset int) map[string]any {
	return r.issue(r.draft(contact, offset))
}

func (r *rb) run(w who, in map[string]any) map[string]any { return r.okAs(w, "report.run", in) }

// notFoundIn calls cmd in w's own workspace with an id that belongs to another one.
func (r *rb) notFoundIn(w who, cmd string, id any) {
	r.t.Helper()
	if res := r.cmd(r.gosv.URL, w, w.ws, cmd, map[string]any{"id": id}, nil); res.status != 404 {
		r.t.Fatalf("%s: want 404, got %d %s", cmd, res.status, res.body)
	}
}

func invSpec(extra map[string]any) map[string]any {
	s := map[string]any{"dataset": "invoices"}
	for k, v := range extra {
		s[k] = v
	}
	return s
}

func TestReportGuestIsForbidden(t *testing.T) {
	r := newRB(t)
	for _, cmd := range []string{"report.run", "report.datasets", "report.list", "report.get", "dashboard.list", "dashboard.get"} {
		r.errAs(r.guest, cmd, map[string]any{"spec": map[string]any{"dataset": "deals"}}, 403, "forbidden")
	}
}

func TestReportFinanceGating(t *testing.T) {
	r := newRB(t)
	r.invoice(r.c1, 0)
	r.ok("deal.create", map[string]any{"space_id": r.crm, "title": "D1", "amount_minor": 10_000, "stage": "proposal"})
	r.ok("deal.create", map[string]any{"space_id": r.crm, "title": "D2", "amount_minor": 5_000})
	r.ok("item.create", map[string]any{"space_id": r.eng, "title": "T1"})

	has := func(w who, key string) bool {
		for _, d := range r.okAs(w, "report.datasets", map[string]any{})["datasets"].([]any) {
			if d.(map[string]any)["key"] == key {
				return true
			}
		}
		return false
	}
	eq(t, has(r.member, "invoices"), true)
	eq(t, has(r.noFin, "invoices"), false)
	eq(t, has(r.noFin, "payments"), false)
	eq(t, has(r.noFin, "deals"), true)

	// every position of an invoice spec is refused without finance access
	for name, s := range map[string]map[string]any{
		"columns": invSpec(map[string]any{"columns": []string{"number", "total"}}),
		"filter":  invSpec(map[string]any{"filters": []any{map[string]any{"field": "total", "op": "gt", "value": 1}}}),
		"group":   invSpec(map[string]any{"group_by": []any{map[string]any{"field": "status"}}}),
		"measure": invSpec(map[string]any{"measures": []any{map[string]any{"field": "total", "agg": "sum"}}}),
		"sort":    invSpec(map[string]any{"columns": []string{"number"}, "sort": []any{map[string]any{"key": "total", "dir": "desc"}}}),
		"period":  invSpec(map[string]any{"period": map[string]any{"field": "invoice_date", "preset": "this_fy"}}),
	} {
		if r.call(r.noFin, "report.run", map[string]any{"spec": s}).status != 403 {
			t.Errorf("%s: want 403", name)
		}
		if r.call(r.noFin, "report.save", map[string]any{"name": "x", "spec": s}).status != 403 {
			t.Errorf("%s save: want 403", name)
		}
		r.run(r.member, map[string]any{"spec": s}) // finance members run the same specs
	}
	// deals (money field) and tasks stay available
	d := r.run(r.noFin, map[string]any{"spec": map[string]any{"dataset": "deals", "group_by": []any{map[string]any{"field": "stage"}},
		"measures": []any{map[string]any{"field": "amount", "agg": "sum"}}}})
	eq(t, d["count"], float64(2))
	eq(t, r.run(r.noFin, map[string]any{"spec": map[string]any{"dataset": "tasks"}})["count"], float64(1))
}

func TestReportSavedVisibilityAndEditRights(t *testing.T) {
	r := newRB(t)
	spec := map[string]any{"dataset": "deals"}
	priv := r.ok("report.save", map[string]any{"name": "Mine", "spec": spec, "visibility": "private"})["report"].(map[string]any)
	pub := r.ok("report.save", map[string]any{"name": "Shared", "spec": spec, "visibility": "workspace"})["report"].(map[string]any)
	eq(t, priv["can_edit"], true)
	eq(t, priv["owner_name"], "Tess")

	r.errAs(r.member, "report.get", map[string]any{"id": priv["id"]}, 404, "not_found")
	r.errAs(r.member, "report.run", map[string]any{"id": priv["id"]}, 404, "not_found")
	var names []string
	for _, x := range r.okAs(r.member, "report.list", map[string]any{})["reports"].([]any) {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	eq(t, names, []string{"Shared"})
	eq(t, len(r.ok("report.list", map[string]any{})["reports"].([]any)), 2)

	eq(t, r.okAs(r.member, "report.run", map[string]any{"id": pub["id"]})["count"], float64(0))
	g := r.okAs(r.member, "report.get", map[string]any{"id": pub["id"]})["report"].(map[string]any)
	eq(t, g["can_edit"], false)
	r.errAs(r.member, "report.save", map[string]any{"id": pub["id"], "name": "Hijack", "spec": spec}, 403, "forbidden")
	r.errAs(r.member, "report.delete", map[string]any{"id": pub["id"]}, 403, "forbidden")

	// an admin may edit a workspace report but still cannot see someone's private one
	adm := r.memberAs("Ada", "admin")
	eq(t, r.okAs(adm, "report.get", map[string]any{"id": pub["id"]})["report"].(map[string]any)["can_edit"], true)
	r.errAs(adm, "report.get", map[string]any{"id": priv["id"]}, 404, "not_found")
	r.okAs(adm, "report.save", map[string]any{"id": pub["id"], "name": "Renamed", "spec": spec, "visibility": "workspace"})

	// validation
	r.errIs("report.save", map[string]any{"name": "", "spec": spec}, 400, "invalid")
	r.errIs("report.save", map[string]any{"name": strings.Repeat("x", 121), "spec": spec}, 400, "invalid")
	r.errIs("report.save", map[string]any{"name": "x", "spec": spec, "visibility": "public"}, 400, "invalid")
	r.errIs("report.save", map[string]any{"name": "x", "spec": map[string]any{"dataset": "nope"}}, 400, "invalid")

	// another workspace cannot reach it
	other := r.env.signup("Otto", true)
	for _, cmd := range []string{"report.get", "report.run", "report.delete"} {
		r.notFoundIn(other, cmd, pub["id"])
	}

	r.ok("report.delete", map[string]any{"id": priv["id"]})
	r.errIs("report.get", map[string]any{"id": priv["id"]}, 404, "not_found")
}

func TestReportTruncationTotalsAndCSV(t *testing.T) {
	r := newRB(t)
	var total float64
	for i := 0; i < 3; i++ {
		total += r.invoice(r.c1, -i)["total_minor"].(float64)
	}
	res := r.run(r.owner, map[string]any{"spec": invSpec(map[string]any{"columns": []string{"number", "total"}, "limit": 2})})
	eq(t, len(res["rows"].([]any)), 2)
	eq(t, res["truncated"], true)
	eq(t, res["count"], float64(3))
	tot := res["totals"].([]any)
	eq(t, tot[1], total) // over all three rows, not the two shown

	full := r.run(r.owner, map[string]any{"spec": invSpec(map[string]any{"columns": []string{"number", "total"}})})
	eq(t, full["truncated"], false)

	// CSV: header labels, rupees with two decimals, formula guard on text
	evil := r.must(r.owner, "contact.create", map[string]any{"space_id": r.space, "name": "=HYPERLINK(\"http://x\")", "kind": "company", "state_code": "29"})["id"].(string)
	r.invoice(evil, 0)
	out := r.run(r.owner, map[string]any{"format": "csv", "spec": invSpec(map[string]any{"columns": []string{"contact", "total"}})})
	csv := out["csv"].(string)
	if !strings.HasPrefix(csv, "Customer,Total amount\n") {
		t.Fatalf("header: %q", csv)
	}
	if !strings.Contains(csv, "\"'=HYPERLINK") {
		t.Fatalf("formula not neutralised: %q", csv)
	}
	if !strings.Contains(csv, ",") || strings.Contains(csv, "# truncated") || !strings.HasSuffix(out["filename"].(string), ".csv") {
		t.Fatalf("csv: %v", out)
	}
	if strings.Contains(csv, ".5\n") { // 2 decimals always
		t.Fatalf("money not in rupees with 2 decimals: %q", csv)
	}
}

func TestReportPeriodThisFY(t *testing.T) {
	r := newRB(t)
	r.invoice(r.c1, 0)
	r.invoice(r.c1, -800)
	spec := invSpec(map[string]any{"columns": []string{"number"}})
	eq(t, r.run(r.owner, map[string]any{"spec": spec})["count"], float64(2))
	spec["period"] = map[string]any{"field": "invoice_date", "preset": "this_fy"}
	res := r.run(r.owner, map[string]any{"spec": spec})
	eq(t, res["count"], float64(1))
	p := res["period"].(map[string]any)
	if p["from"] == "" || p["to"] == "" || p["label"] == "" {
		t.Fatalf("period: %v", p)
	}
	// a saved report can be run with a different period
	id := r.ok("report.save", map[string]any{"name": "Inv", "spec": spec})["report"].(map[string]any)["id"]
	last := r.run(r.owner, map[string]any{"id": id, "period": map[string]any{"preset": "last_fy"}})
	eq(t, last["count"], float64(0))
}

func TestReportWhoOwesMeMatchesReceivablesAgeing(t *testing.T) {
	r := newRB(t)
	a := r.invoice(r.c1, -5)
	r.invoice(r.c2, -50)
	r.invoice(r.c2, -100)
	r.draft(r.c1, 0) // a draft is not owed
	r.ok("payment.record", map[string]any{"invoice_id": a["id"], "amount_minor": 20_000, "method": "upi"})
	ageing := r.ok("report.receivables_ageing", map[string]any{})
	want := ageing["total_minor"].(float64)
	if want <= 0 {
		t.Fatal("seed has no receivables")
	}
	spec := invSpec(map[string]any{
		"filters":  []any{map[string]any{"field": "status", "op": "in", "value": []string{"issued", "irn_generated", "sent", "partly_paid"}}},
		"group_by": []any{map[string]any{"field": "contact"}},
		"measures": []any{map[string]any{"field": "balance", "agg": "sum"}},
		"sort":     []any{map[string]any{"key": "m0", "dir": "desc"}},
		"viz":      "bar",
	})
	res := r.run(r.owner, map[string]any{"spec": spec})
	eq(t, res["totals"].([]any)[1], want)
	var sum float64
	for _, row := range res["rows"].([]any) {
		sum += row.([]any)[1].(float64)
	}
	eq(t, sum, want)
	eq(t, len(res["rows"].([]any)), 2)
	if c := res["rows"].([]any)[0].([]any)[0].(map[string]any); c["label"] == nil || c["id"] == nil {
		t.Fatalf("contact cell: %v", c)
	}
}

func TestReportDashboards(t *testing.T) {
	r := newRB(t)
	spec := map[string]any{"dataset": "deals"}
	priv := r.ok("report.save", map[string]any{"name": "Mine", "spec": spec})["report"].(map[string]any)["id"].(string)
	pub := r.ok("report.save", map[string]any{"name": "Shared", "spec": spec, "visibility": "workspace"})["report"].(map[string]any)["id"].(string)
	d := r.ok("dashboard.save", map[string]any{"name": "Board", "visibility": "workspace", "period": map[string]any{"preset": "this_month"},
		"tiles": []any{map[string]any{"report_id": priv, "size": "half"}, map[string]any{"report_id": pub, "size": "number"}}})["dashboard"].(map[string]any)
	eq(t, d["can_edit"], true)

	got := r.okAs(r.member, "dashboard.get", map[string]any{"id": d["id"]})["dashboard"].(map[string]any)
	tiles := got["tiles"].([]any)
	eq(t, tiles[0].(map[string]any)["available"], false)
	eq(t, tiles[0].(map[string]any)["name"], nil)
	eq(t, tiles[1].(map[string]any)["available"], true)
	eq(t, tiles[1].(map[string]any)["name"], "Shared")
	eq(t, got["can_edit"], false)
	own := r.ok("dashboard.get", map[string]any{"id": d["id"]})["dashboard"].(map[string]any)
	eq(t, own["tiles"].([]any)[0].(map[string]any)["available"], true)
	eq(t, len(r.okAs(r.member, "dashboard.list", map[string]any{})["dashboards"].([]any)), 1)

	r.errAs(r.member, "dashboard.save", map[string]any{"id": d["id"], "name": "x", "tiles": []any{}}, 403, "forbidden")
	r.errAs(r.member, "dashboard.delete", map[string]any{"id": d["id"]}, 403, "forbidden")
	// someone else's private report id must look exactly like a missing one
	r.errAs(r.member, "dashboard.save", map[string]any{"name": "x", "tiles": []any{map[string]any{"report_id": priv, "size": "half"}}}, 400, "invalid")
	r.errIs("dashboard.save", map[string]any{"name": "x", "tiles": []any{map[string]any{"report_id": pub, "size": "huge"}}}, 400, "invalid")
	r.errIs("dashboard.save", map[string]any{"name": "x", "tiles": []any{map[string]any{"report_id": "3f1c1f0e-1b1b-4b1b-8b1b-1b1b1b1b1b1b", "size": "half"}}}, 400, "invalid")
	r.errIs("dashboard.save", map[string]any{"name": "x", "tiles": []any{}, "period": map[string]any{"preset": "someday"}}, 400, "invalid")
	many := make([]any, 25)
	for i := range many {
		many[i] = map[string]any{"report_id": pub, "size": "half"}
	}
	r.errIs("dashboard.save", map[string]any{"name": "x", "tiles": many}, 400, "invalid")

	// deleting a report leaves the tile as "not available"; the dashboard still loads
	r.ok("report.delete", map[string]any{"id": pub})
	after := r.okAs(r.member, "dashboard.get", map[string]any{"id": d["id"]})["dashboard"].(map[string]any)
	eq(t, after["tiles"].([]any)[1].(map[string]any)["available"], false)
	other := r.env.signup("Otto", true)
	r.notFoundIn(other, "dashboard.get", d["id"])
	r.ok("dashboard.delete", map[string]any{"id": d["id"]})
}
