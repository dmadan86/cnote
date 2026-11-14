package server_test

import (
	"sync"
	"testing"
)

// WP9: time tracking and milestone billing. Go-only commands; fixtures (spaces, items, contacts) come from Rust.

func (b *bt) item(title string) string {
	b.t.Helper()
	return b.ok("item.create", map[string]any{"space_id": b.space, "title": title})["id"].(string)
}

func (b *bt) logTime(w who, item string, minutes int, over map[string]any) map[string]any {
	b.t.Helper()
	in := map[string]any{"item_id": item, "minutes": minutes, "worked_on": day(0)}
	for k, v := range over {
		in[k] = v
	}
	return b.okAs(w, "time.log", in)
}

func (b *bt) profile() {
	b.ok("project.billing_set", map[string]any{"space_id": b.space, "contact_id": b.contact, "hourly_rate_minor": 150_000, "hsn_sac": "998314"})
}

func TestTimeLogRulesCapAndOwnership(t *testing.T) {
	b := newBT(t, false)
	it := b.item("Design review")
	mia := b.memberAs("Mia", "member")
	zed := b.memberAs("Zed", "member")
	adm := b.memberAs("Ada", "admin")

	e := b.logTime(mia, it, 120, map[string]any{"note": "wireframes"})
	eq(t, e["minutes"], float64(120))
	eq(t, e["billable"], true)
	eq(t, e["user_name"], "Mia")
	eq(t, count(b.events(it), "time.logged"), 1)

	// 24 h a day per person, across entries; another person is unaffected
	b.errAs(mia, "time.log", map[string]any{"item_id": it, "minutes": 1321, "worked_on": day(0)}, 400, "invalid")
	b.logTime(mia, it, 1320, nil)
	b.errAs(mia, "time.log", map[string]any{"item_id": it, "minutes": 1, "worked_on": day(0)}, 400, "invalid")
	b.logTime(zed, it, 60, nil)
	b.logTime(mia, it, 30, map[string]any{"worked_on": day(-1)})
	for _, bad := range []map[string]any{{"minutes": 0}, {"minutes": 1441}, {"minutes": 5, "worked_on": "2026-13-45"}, {"minutes": 5, "item_id": "zzz"}, {"minutes": 5, "item_id": nil}} {
		in := map[string]any{"item_id": it, "worked_on": day(0)}
		for k, v := range bad {
			in[k] = v
		}
		b.errAs(mia, "time.log", in, 400, "invalid")
	}
	b.errAs(mia, "time.log", map[string]any{"item_id": "00000000-0000-4000-8000-000000000000", "minutes": 5}, 404, "not_found")

	// update: the cap counts the other entries of the day, the author or an admin may change, others may not
	b.errAs(mia, "time.update", map[string]any{"id": e["id"], "minutes": 200}, 400, "invalid")
	eq(t, b.okAs(mia, "time.update", map[string]any{"id": e["id"], "minutes": 90, "note": "", "billable": false})["billable"], false)
	b.errAs(zed, "time.update", map[string]any{"id": e["id"], "minutes": 10}, 403, "forbidden")
	b.errAs(zed, "time.delete", map[string]any{"id": e["id"]}, 403, "forbidden")
	eq(t, b.okAs(adm, "time.update", map[string]any{"id": e["id"], "minutes": 15})["minutes"], float64(15))
	b.okAs(mia, "time.delete", map[string]any{"id": e["id"]})
	b.errAs(mia, "time.delete", map[string]any{"id": e["id"]}, 404, "not_found")

	// list: totals, filters
	r := b.okAs(zed, "time.list", map[string]any{"space_id": b.space})
	tot := r["totals"].(map[string]any)
	eq(t, tot["minutes"], float64(1320+60+30))
	eq(t, len(r["entries"].([]any)), 3)
	eq(t, len(b.okAs(zed, "time.list", map[string]any{"user_id": zed.userID})["entries"].([]any)), 1)
	eq(t, len(b.okAs(zed, "time.list", map[string]any{"from": day(0), "to": day(0)})["entries"].([]any)), 2)

	// agents do not log time; an API key of a person does
	b.errAs(b.agentKey("bot"), "time.log", map[string]any{"item_id": it, "minutes": 5}, 403, "forbidden")
	b.okAs(b.apiKey(mia), "time.log", map[string]any{"item_id": it, "minutes": 5, "worked_on": day(-2)})
	// another workspace sees nothing and cannot log on this item
	other := newBT(t, false)
	other.errIs("time.log", map[string]any{"item_id": it, "minutes": 5}, 404, "not_found")
	eq(t, len(other.ok("time.list", map[string]any{})["entries"].([]any)), 0)
}

func TestTimeInvoiceBillsOnceAndLocksEntries(t *testing.T) {
	b := newBT(t, false)
	a, c := b.item("Build"), b.item("Test")
	mia := b.memberAs("Mia", "member")
	b.logTime(mia, a, 90, map[string]any{"worked_on": day(-3)})
	b.logTime(mia, a, 45, map[string]any{"worked_on": day(-2)})
	b.logTime(b.owner, c, 20, map[string]any{"worked_on": day(-2)})
	b.logTime(b.owner, c, 600, map[string]any{"billable": false})
	b.logTime(b.owner, c, 60, map[string]any{"worked_on": day(5)}) // after to_date

	// the project needs a customer, rate and SAC first
	b.errIs("time.invoice", map[string]any{"space_id": b.space, "to_date": day(0)}, 400, "invalid")
	b.errIs("project.billing_set", map[string]any{"space_id": b.space, "hsn_sac": "12"}, 400, "invalid")
	b.errIs("project.billing_set", map[string]any{"space_id": b.space, "gst_rate_bp": 5000}, 400, "invalid")
	b.profile()
	eq(t, n(b.ok("project.billing_get", map[string]any{"space_id": b.space})["hourly_rate_minor"]), int64(150_000))
	b.errIs("time.invoice", map[string]any{"space_id": b.space, "to_date": day(0), "group_by": "week"}, 400, "invalid")
	b.errIs("time.invoice", map[string]any{"space_id": b.space}, 400, "invalid")

	// 155 min at 1500.00/h = 3875.00, one line; grouped by item: 135 min -> 3375.00 and 20 min -> 500.00
	h := map[string]string{"Idempotency-Key": "ti-" + rnd()}
	in := map[string]any{"space_id": b.space, "to_date": day(0), "group_by": "item"}
	r1 := b.cmd(b.gosv.URL, b.owner, b.owner.ws, "time.invoice", in, h)
	r2 := b.cmd(b.gosv.URL, b.owner, b.owner.ws, "time.invoice", in, h)
	if r1.status != 200 || string(r1.body) != string(r2.body) {
		t.Fatalf("replay: %d %s / %d %s", r1.status, r1.body, r2.status, r2.body)
	}
	got := b.ok("time.list", map[string]any{"unbilled": true})["totals"].(map[string]any)
	eq(t, got["unbilled_minutes"], float64(60)) // the entry after to_date; the non-billable one never counts
	pv := b.ok("invoice.get", map[string]any{"id": b.firstInvoice()})
	eq(t, n(pv["subtotal_minor"]), int64(387_500))
	eq(t, len(pv["lines"].([]any)), 2)
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from projects.time_entries where invoice_id is not null"), []string{"3"})
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from billing.invoices"), []string{"1"}) // replay did not bill twice

	// billed entries are frozen; a second run finds nothing
	id := b.dbStrings(b.owner.ws, "select id::text from projects.time_entries where invoice_id is not null limit 1")[0]
	b.errIs("time.update", map[string]any{"id": id, "minutes": 5}, 409, "conflict")
	b.errIs("time.delete", map[string]any{"id": id}, 409, "conflict")
	b.errIs("time.invoice", map[string]any{"space_id": b.space, "to_date": day(0)}, 409, "conflict")
	eq(t, count(b.events(b.firstInvoice()), "time.invoiced"), 1)

	// concurrent runs bill each entry exactly once
	b.logTime(b.owner, a, 30, map[string]any{"worked_on": day(-1)})
	var wg sync.WaitGroup
	codes := make([]int, 4)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = b.call(b.owner, "time.invoice", map[string]any{"space_id": b.space, "to_date": day(0)}).status
		}()
	}
	wg.Wait()
	wins := 0
	for _, s := range codes {
		if s == 200 {
			wins++
		} else if s != 409 {
			t.Fatalf("status %d", s)
		}
	}
	eq(t, wins, 1)
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from billing.invoices"), []string{"2"})
}

func (b *bt) firstInvoice() string {
	return b.dbStrings(b.owner.ws, "select note_id::text from billing.invoices order by (select created_at from core.notes where id = note_id) limit 1")[0]
}

func TestMilestonesFromQuoteWithAdvance(t *testing.T) {
	b := newBT(t, false)
	q := b.quote(b.contact, "")
	b.q("quote.send", q)
	b.q("quote.accept", q)
	sub := n(b.ok("quote.get", map[string]any{"id": q})["subtotal_minor"])
	b.errIs("quote.convert_to_project", map[string]any{"id": q, "milestones": []any{map[string]any{"name": "", "percent_bp": 3000}}}, 400, "invalid")
	b.errIs("quote.convert_to_project", map[string]any{"id": q, "milestones": []any{map[string]any{"name": "A", "percent_bp": 6000}, map[string]any{"name": "B", "percent_bp": 6000}}}, 400, "invalid")
	b.q("quote.convert_to_project", q) // nothing was left half-done by the refusals: the quote was still convertible
	// a second quote is converted with a payment schedule: 30 / 40 / 30
	q2 := b.quote(b.contact, "")
	b.q("quote.send", q2)
	b.q("quote.accept", q2)
	p := b.ok("quote.convert_to_project", map[string]any{"id": q2, "milestones": []any{
		map[string]any{"name": "Slab", "percent_bp": 4000}, map[string]any{"name": "Advance", "percent_bp": 3000}, map[string]any{"name": "Handover", "percent_bp": 3000}}})
	_ = p
	ms := b.okList("milestone.list", map[string]any{"space_id": b.space})
	eq(t, len(ms), 3)
	var sum int64
	byName := map[string]map[string]any{}
	for _, x := range ms {
		m := x.(map[string]any)
		byName[m["name"].(string)] = m
		sum += n(m["bill_amount_minor"])
		eq(t, m["status"], "planned")
		eq(t, m["quotation_id"], q2)
	}
	eq(t, sum, sub) // the last milestone took the remainder: no paisa unbilled
	// more than the quotation is refused, also by amount; a percent without a quotation is refused
	extra := b.ok("version.create", map[string]any{"space_id": b.space, "name": "Extra"})["id"]
	b.errIs("milestone.set_billing", map[string]any{"version_id": extra, "quotation_id": q2, "bill_percent_bp": 100}, 400, "invalid")
	b.errIs("milestone.set_billing", map[string]any{"version_id": extra, "quotation_id": q2, "bill_amount_minor": 1}, 400, "invalid")
	b.errIs("milestone.set_billing", map[string]any{"version_id": extra, "bill_percent_bp": 100}, 400, "invalid")
	b.errIs("milestone.set_billing", map[string]any{"version_id": extra, "bill_amount_minor": 5, "bill_percent_bp": 5}, 400, "invalid")

	// the 30% advance milestone: invoice dated by the user, GST per quotation rate, the sum of the lines is the milestone
	adv := byName["Advance"]["id"].(string)
	inv := b.ok("milestone.invoice", map[string]any{"version_id": adv, "invoice_date": day(-3)})["invoice"].(map[string]any)
	eq(t, n(inv["subtotal_minor"]), n(byName["Advance"]["bill_amount_minor"]))
	eq(t, inv["invoice_date"], day(-3))
	eq(t, len(inv["lines"].([]any)), 2) // the quotation has two GST rates
	b.errIs("milestone.invoice", map[string]any{"version_id": adv}, 409, "conflict")
	b.errIs("milestone.set_billing", map[string]any{"version_id": adv, "bill_amount_minor": 10}, 409, "conflict")
	b.errIs("milestone.invoice", map[string]any{"version_id": extra}, 400, "invalid") // not a billing milestone
	eq(t, count(b.events(inv["id"].(string)), "milestone.invoiced"), 1)
	m := b.milestone("Advance")
	eq(t, m["status"], "invoiced")
	eq(t, m["invoice_id"], inv["id"])

	// an advance received for the quotation is applied to the milestone invoice (WP4)
	issued := b.issue(inv["id"].(string))
	total := n(issued["total_minor"])
	r := b.receipt(map[string]any{"quotation_id": q2, "amount_minor": total})
	b.apply(b.owner, r["id"].(string), issued["id"].(string), total)
	eq(t, b.milestone("Advance")["status"], "paid")
	b.releaseVersion(adv)
	eq(t, b.milestone("Slab")["status"], "planned")
	b.releaseVersion(byName["Slab"]["id"].(string))
	eq(t, b.milestone("Slab")["status"], "due")

	// project money: quoted, invoiced, received (advance applied is not new money), the advance ledger
	rep := b.ok("report.project_money", map[string]any{"space_id": b.space})
	eq(t, n(rep["quoted_minor"]), sub*2)
	eq(t, n(rep["invoiced_minor"]), n(inv["subtotal_minor"]))
	eq(t, n(rep["advance_applied_minor"]), total)
	eq(t, n(rep["advance_received_minor"]), total)
	eq(t, n(rep["advance_unapplied_minor"]), int64(0))
	eq(t, n(rep["received_minor"]), int64(0))
	eq(t, n(rep["milestones_planned_minor"]), sub)
	eq(t, n(rep["milestones_invoiced_minor"]), n(inv["subtotal_minor"]))
	b.assertTBZero()

	// a cancelled milestone invoice can be raised again
	m2 := byName["Handover"]["id"].(string)
	d := b.ok("milestone.invoice", map[string]any{"version_id": m2})["invoice"].(map[string]any)
	b.ok("invoice.cancel", map[string]any{"id": b.issue(d["id"].(string))["id"], "reason": "wrong date"})
	eq(t, b.milestone("Handover")["status"], "planned")
	b.ok("milestone.invoice", map[string]any{"version_id": m2})
}

func (b *bt) milestone(name string) map[string]any {
	for _, x := range b.okList("milestone.list", map[string]any{"space_id": b.space}) {
		if m := x.(map[string]any); m["name"] == name {
			return m
		}
	}
	b.t.Fatalf("no milestone %s", name)
	return nil
}

func (b *bt) releaseVersion(id string) { b.ok("version.release", map[string]any{"id": id}) }

func TestTimeAndMilestonePermissions(t *testing.T) {
	b := newBT(t, false)
	it := b.item("Task")
	b.profile()
	mia := b.memberAs("Mia", "member")
	guest := b.memberAs("Gus", "guest")
	b.logTime(mia, it, 60, nil)
	q := b.quote(b.contact, "")
	b.q("quote.send", q)
	b.q("quote.accept", q)
	b.ok("quote.convert_to_project", map[string]any{"id": q, "milestones": []any{map[string]any{"name": "All", "percent_bp": 10000}}})
	ms := b.milestone("All")
	eq(t, n(ms["bill_amount_minor"]), n(b.ok("quote.get", map[string]any{"id": q})["subtotal_minor"]))

	// guests: no space, no time
	for _, c := range []string{"time.list", "milestone.list", "report.project_money", "project.billing_get"} {
		b.errAs(guest, c, map[string]any{"space_id": b.space}, 403, "forbidden")
	}
	b.errAs(guest, "time.log", map[string]any{"item_id": it, "minutes": 5}, 403, "forbidden")

	// finance access off: invoicing and money reads are refused, logging time is not
	b.ok("member.update", map[string]any{"user_id": mia.userID, "finance_access": false})
	for _, c := range []string{"time.invoice", "milestone.invoice", "milestone.list", "report.project_money"} {
		r := b.call(mia, c, map[string]any{"space_id": b.space, "to_date": day(0), "version_id": ms["id"]})
		if r.status != 403 {
			t.Fatalf("%s: want 403, got %d %s", c, r.status, r.body)
		}
	}
	b.logTime(mia, it, 5, map[string]any{"worked_on": day(-1)})
	b.ok("member.update", map[string]any{"user_id": mia.userID, "finance_access": true})
	b.okAs(mia, "report.project_money", map[string]any{"space_id": b.space})

	// agents: no money tools unless the workspace enables manage_invoices; time.invoice stays a draft either way
	agent := b.agentKey("bot")
	b.errAs(agent, "time.invoice", map[string]any{"space_id": b.space, "to_date": day(0)}, 403, "forbidden")
	b.errAs(agent, "milestone.invoice", map[string]any{"version_id": ms["id"]}, 403, "forbidden")
	agent = who{ws: b.owner.ws, bearer: b.must(b.owner, "agent.create", map[string]any{"name": "biller", "scopes": []string{"read", "manage_invoices"}})["api_key"].(string)}
	b.errAs(agent, "milestone.invoice", map[string]any{"version_id": ms["id"]}, 403, "forbidden") // group still off for agents
	b.enableGroups("read", "write_notes", "write_items", "manage_invoices")
	b.okAs(agent, "milestone.invoice", map[string]any{"version_id": ms["id"]})
	b.okAs(agent, "report.project_money", map[string]any{"space_id": b.space})

	// another workspace cannot see or bill this project
	other := newBT(t, false)
	other.errIs("milestone.invoice", map[string]any{"version_id": ms["id"]}, 404, "not_found")
	other.errIs("milestone.set_billing", map[string]any{"version_id": ms["id"], "bill_amount_minor": 5}, 404, "not_found")
	other.errIs("time.invoice", map[string]any{"space_id": b.space, "to_date": day(0)}, 404, "not_found")
	other.errIs("report.project_money", map[string]any{"space_id": b.space}, 404, "not_found")
}
