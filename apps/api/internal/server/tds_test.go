package server_test

import (
	"strings"
	"testing"
)

// billExcl is a recorded bill whose value without GST is `price` paise (18% GST on top, same state).
func (b *bt) billExcl(vendor, number string, price int64) string {
	id := b.ok("bill.create_draft", map[string]any{"space_id": b.space, "vendor_id": vendor, "vendor_bill_number": number, "bill_date": day(-5),
		"lines": []any{map[string]any{"description": "Work", "unit_price_minor": price}}})["id"].(string)
	b.bq("bill.record", id)
	return id
}

func (b *bt) setTax(vendor string, in map[string]any) map[string]any {
	in["id"] = vendor
	return b.ok("vendor.set_tax", in)
}

func (b *bt) preview(id string, amount int64, code string) map[string]any {
	in := map[string]any{"id": id, "amount_minor": amount}
	if code != "" {
		in["tds_code"] = code
	}
	return b.ok("bill.tds_preview", in)
}

const rs = 100

func TestTDSRatesAreSeededDataAndEditable(t *testing.T) {
	b := newBT(t, false)
	eq(t, len(b.tb()["accounts"].([]any)), 7)
	rates := b.list(b.owner, "tds.rate_list", map[string]any{})
	got := map[string]map[string]any{}
	for _, r := range rates {
		x := r.(map[string]any)
		got[x["code"].(string)] = x
	}
	eq(t, n(got["contractor_individual"]["rate_bp"]), int64(100))
	eq(t, n(got["contractor_other"]["rate_bp"]), int64(200))
	eq(t, n(got["professional"]["rate_bp"]), int64(1000))
	eq(t, n(got["technical"]["rate_bp"]), int64(200))
	eq(t, n(got["contractor_other"]["single_limit_minor"]), int64(30_000*rs))
	eq(t, n(got["contractor_other"]["annual_limit_minor"]), int64(100_000*rs))
	eq(t, got["professional"]["single_limit_minor"], nil)
	eq(t, strings.Contains(got["contractor_other"]["label"].(string), "194C"), true) // old and new section names
	eq(t, len(b.tb()["accounts"].([]any)), 7)                                        // reading rates posts nothing

	m := b.addMember(b.owner, b.signup("Mia", false), "member")
	b.errAs(m, "tds.rate_set", map[string]any{"code": "professional", "rate_bp": 500}, 403, "forbidden")
	eq(t, len(b.list(m, "tds.rate_list", map[string]any{})), 4)
	x := b.ok("tds.rate_set", map[string]any{"code": "professional", "rate_bp": 500, "annual_limit_minor": nil})
	eq(t, n(x["rate_bp"]), int64(500))
	eq(t, x["annual_limit_minor"], nil)
	eq(t, n(x["no_pan_rate_bp"]), int64(2000)) // untouched fields keep their value
	b.ok("tds.rate_set", map[string]any{"code": "rent", "label": "Rent of land or building", "rate_bp": 1000, "annual_limit_minor": 240_000 * rs})
	eq(t, len(b.list(b.owner, "tds.rate_list", map[string]any{})), 5)
	for _, bad := range []map[string]any{{"code": "Bad Code"}, {"code": "x", "label": ""}, {"code": "professional", "rate_bp": 10_001},
		{"code": "professional", "rate_bp": -1}, {"code": "professional", "single_limit_minor": -1}, {"code": "professional", "label": 5}} {
		b.errIs("tds.rate_set", bad, 400, "invalid")
	}
}

func TestVendorPANAndDefaultCode(t *testing.T) {
	b := newBT(t, false)
	v := b.vendor("29", "Builder")
	eq(t, b.setTax(v, map[string]any{"pan": "abcde1234f", "default_tds_code": "contractor_other"})["pan"], "ABCDE1234F")
	vl := b.list(b.owner, "vendor.list", map[string]any{})
	eq(t, vl[0].(map[string]any)["pan"], "ABCDE1234F")
	eq(t, vl[0].(map[string]any)["default_tds_code"], "contractor_other")
	for _, bad := range []map[string]any{{"pan": "123"}, {"pan": "ABCDE12345"}, {"default_tds_code": "nope"}, {"pan": 5}} {
		bad["id"] = v
		b.errIs("vendor.set_tax", bad, 400, "invalid")
	}
	eq(t, b.setTax(v, map[string]any{"default_tds_code": ""})["default_tds_code"], nil) // an empty string clears
	eq(t, b.setTax(v, map[string]any{"pan": nil})["pan"], nil)
	b.errIs("vendor.set_tax", map[string]any{"id": b.contact, "pan": "ABCDE1234F"}, 400, "invalid") // not a vendor
	g := b.addMember(b.owner, b.signup("Gus", false), "guest")
	b.errAs(g, "vendor.set_tax", map[string]any{"id": v, "pan": "ABCDE1234F"}, 403, "forbidden")
}

func TestTDSThresholdsCrossingAndLedger(t *testing.T) {
	b := newBT(t, false)
	v := b.vendor("29", "Builder")
	b.setTax(v, map[string]any{"pan": "ABCDE1234F", "default_tds_code": "contractor_other"})
	b1, b2, b3 := b.billExcl(v, "B1", 25_000*rs), b.billExcl(v, "B2", 25_000*rs), b.billExcl(v, "B3", 60_000*rs)
	gross := func(price int64) int64 { return price * 118 / 100 }

	// 25,000 without GST: under the single limit and the annual limit
	p := b.preview(b1, gross(25_000*rs), "")
	eq(t, p["tds_code"], "contractor_other")
	eq(t, n(p["base_minor"]), int64(25_000*rs))
	eq(t, n(p["tds_minor"]), int64(0))
	eq(t, strings.Contains(p["reason"].(string), "under the limits"), true)
	r := b.payBill(b1, gross(25_000*rs))
	pay := r["payments"].([]any)[0].(map[string]any)
	eq(t, n(pay["tds_minor"]), int64(0))
	eq(t, pay["tds_code"], "contractor_other")
	eq(t, b.acct("1200"), -gross(25_000*rs))
	accounts := len(b.tb()["accounts"].([]any))
	b.payBill(b2, gross(25_000*rs))                  // 50,000 so far: still under 1,00,000
	eq(t, len(b.tb()["accounts"].([]any)), accounts) // no deduction yet: no TDS account

	// the third payment takes the year past 1,00,000: TDS on everything not yet deducted (1,10,000 x 2%)
	p = b.preview(b3, gross(60_000*rs), "")
	eq(t, n(p["prior_base_minor"]), int64(50_000*rs))
	eq(t, n(p["on_minor"]), int64(110_000*rs))
	eq(t, n(p["tds_minor"]), int64(2_200*rs))
	eq(t, n(p["net_minor"]), gross(60_000*rs)-2_200*rs)
	eq(t, strings.Contains(strings.Join(strToList(p["warnings"]), " "), "CA review"), true)
	bank0, payable0 := b.acct("1200"), b.acct("2100")
	r = b.payBill(b3, gross(60_000*rs))
	eq(t, r["status"], "paid")
	eq(t, n(r["payments"].([]any)[0].(map[string]any)["tds_minor"]), int64(2_200*rs))
	eq(t, b.acct("2400"), int64(-2_200*rs))
	eq(t, b.acct("1200"), bank0-(gross(60_000*rs)-2_200*rs))
	eq(t, b.acct("2100"), payable0+gross(60_000*rs))
	b.assertTBZero()
	k := b.events(b3)
	eq(t, count(k, "bill.paid"), 1)
	eq(t, n(b.eventPayload(b3, "bill.paid")["tds_minor"]), int64(2_200*rs))

	// a further payment this year deducts on its own value only
	b4 := b.billExcl(v, "B4", 10_000*rs)
	p = b.preview(b4, gross(10_000*rs), "")
	eq(t, n(p["on_minor"]), int64(10_000*rs))
	eq(t, n(p["tds_minor"]), int64(200*rs))

	// the report: by code and vendor with PAN, and what is owed to the government
	rep := b.ok("report.tds_payable", map[string]any{"from": day(-1), "to": day(0)})
	eq(t, n(rep["total_tds_minor"]), int64(2_200*rs))
	eq(t, n(rep["owed_to_government_minor"]), int64(2_200*rs))
	g := rep["groups"].([]any)[0].(map[string]any)
	eq(t, g["tds_code"], "contractor_other")
	vr := g["vendors"].([]any)[0].(map[string]any)
	eq(t, vr["pan"], "ABCDE1234F")
	eq(t, vr["vendor"], "Builder")
	eq(t, n(vr["tds_minor"]), int64(2_200*rs))
	empty := b.ok("report.tds_payable", map[string]any{"from": day(-60), "to": day(-30)})
	eq(t, len(empty["groups"].([]any)), 0)
	b.errIs("report.tds_payable", map[string]any{"from": day(0)}, 400, "invalid")
}

func strToList(v any) []string {
	out := []string{}
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func (b *bt) eventPayload(note, kind string) map[string]any {
	b.t.Helper()
	for _, e := range b.okList("note.history", map[string]any{"id": note}) {
		if m := e.(map[string]any); m["kind"] == kind {
			if p, ok := m["payload"].(map[string]any); ok {
				return p
			}
		}
	}
	b.t.Fatalf("no %s event with a payload", kind)
	return nil
}

func TestTDSSingleLimitProfessionalAndNoPAN(t *testing.T) {
	b := newBT(t, false)
	// professional: no single limit, annual 50,000; 20% without a PAN
	v := b.vendor("29", "Adviser")
	big := b.billExcl(v, "P1", 60_000*rs)
	p := b.preview(big, 60_000*rs*118/100, "professional")
	eq(t, p["no_pan"], true)
	eq(t, n(p["rate_bp"]), int64(2000))
	eq(t, n(p["tds_minor"]), int64(12_000*rs))
	eq(t, len(strToList(p["warnings"])) >= 1, true)
	eq(t, strings.Contains(strToList(p["warnings"])[0], "no PAN"), true)
	b.setTax(v, map[string]any{"pan": "ABCDE1234F"})
	p = b.preview(big, 60_000*rs*118/100, "professional")
	eq(t, p["no_pan"], false)
	eq(t, n(p["rate_bp"]), int64(1000))
	eq(t, n(p["tds_minor"]), int64(6_000*rs))
	eq(t, len(strToList(p["warnings"])), 0)
	// 40,000 now, then 20,000: the second crosses the annual limit and catches up on both
	small := b.billExcl(v, "P2", 40_000*rs)
	b.payBillTDS(small, 40_000*rs*118/100, map[string]any{"tds_code": "professional"})
	eq(t, b.acctOr0("2400"), int64(0)) // under the annual limit: nothing withheld
	more := b.billExcl(v, "P3", 20_000*rs)
	p = b.preview(more, 20_000*rs*118/100, "professional")
	eq(t, n(p["on_minor"]), int64(60_000*rs))
	eq(t, n(p["tds_minor"]), int64(6_000*rs))

	// contractor single limit: one payment over 30,000 deducts on that payment alone
	c := b.vendor("29", "Contractor")
	b.setTax(c, map[string]any{"pan": "ABCDE1234F", "default_tds_code": "contractor_individual"})
	cb := b.billExcl(c, "C1", 35_000*rs)
	p = b.preview(cb, 35_000*rs*118/100, "")
	eq(t, n(p["tds_minor"]), int64(350*rs)) // 1%
	eq(t, strings.Contains(p["reason"].(string), "single-payment limit"), true)
	// GST is not part of the base: paying half of the bill takes half of the base
	half := int64(35_000 * rs * 118 / 100 / 2)
	p = b.preview(cb, half, "")
	eq(t, n(p["base_minor"]), int64(17_500*rs))
	eq(t, n(p["tds_minor"]), int64(0))
	// no code anywhere: nothing is deducted
	plain := b.vendor("29", "Plain")
	pb := b.billExcl(plain, "N1", 90_000*rs)
	p = b.preview(pb, 90_000*rs*118/100, "")
	eq(t, n(p["tds_minor"]), int64(0))
	eq(t, p["applicable"], false)
	// turning a code off refuses it
	b.ok("tds.rate_set", map[string]any{"code": "technical", "active": false})
	b.errIs("bill.tds_preview", map[string]any{"id": pb, "amount_minor": 100, "tds_code": "technical"}, 400, "invalid")
	b.errIs("bill.tds_preview", map[string]any{"id": pb, "amount_minor": 100, "tds_code": "nope"}, 400, "invalid")
	b.errIs("bill.tds_preview", map[string]any{"id": pb, "amount_minor": 0}, 400, "invalid")
}

func (b *bt) payBillTDS(id string, amount int64, extra map[string]any) map[string]any {
	in := map[string]any{"id": id, "amount_minor": amount}
	for k, v := range extra {
		in[k] = v
	}
	return b.ok("bill.pay", in)
}

func TestTDSOverrideAndOldInputsUnchanged(t *testing.T) {
	b := newBT(t, false)
	v := b.vendor("29", "Builder")
	b.setTax(v, map[string]any{"pan": "ABCDE1234F", "default_tds_code": "contractor_other"})
	id := b.billExcl(v, "O1", 40_000*rs)
	total := int64(40_000 * rs * 118 / 100)
	b.errIs("bill.pay", map[string]any{"id": id, "amount_minor": 1_000, "tds_minor": -1}, 400, "invalid")
	b.errIs("bill.pay", map[string]any{"id": id, "amount_minor": 1_000, "tds_minor": 1_001}, 400, "invalid")
	plainV := b.vendor("29", "NoCode")
	pid := b.billExcl(plainV, "O2", 10_000*rs)
	b.errIs("bill.pay", map[string]any{"id": pid, "amount_minor": 1_000, "tds_minor": 10}, 400, "invalid") // no code to file it under
	eq(t, b.acct("1200"), int64(0))
	// the payer overrides the proposal (800 proposed, 500 chosen)
	r := b.payBillTDS(id, total/2, map[string]any{"tds_minor": 500 * rs})
	eq(t, n(r["payments"].([]any)[0].(map[string]any)["tds_minor"]), int64(500*rs))
	eq(t, b.acct("2400"), int64(-500*rs))
	// zero opts out although the code applies
	b.payBillTDS(id, total-total/2, map[string]any{"tds_minor": 0})
	eq(t, b.acct("2400"), int64(-500*rs))
	b.assertTBZero()
	// a bill without any TDS input pays exactly as before
	r = b.payBill(pid, 5_000)
	eq(t, n(r["payments"].([]any)[0].(map[string]any)["tds_minor"]), int64(0))
	eq(t, r["payments"].([]any)[0].(map[string]any)["tds_code"], nil)
	b.assertTBZero()
	// an agent still needs a person to confirm a payment, TDS or not
	b.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "manage_invoices"}}})
	k := b.apiKey(b.owner)
	x := b.okAs(k, "bill.pay", map[string]any{"id": pid, "amount_minor": 100, "tds_minor": 0})
	eq(t, x["confirmation_required"], true)
}

func TestITCAtRiskFlag(t *testing.T) {
	b := newBT(t, false)
	v := b.vendor("29", "Old Supplier")
	mk := func(num string, age int) string {
		id := b.ok("bill.create_draft", map[string]any{"space_id": b.space, "vendor_id": v, "vendor_bill_number": num, "bill_date": day(-age),
			"lines": []any{map[string]any{"description": "Goods", "unit_price_minor": 10_000}}})["id"].(string)
		b.bq("bill.record", id)
		return id
	}
	old, fresh := mk("OLD", 160), mk("NEW", 100)
	edge := mk("EDGE", 150)
	eq(t, b.bq("bill.get", old)["itc_at_risk"], true)
	eq(t, b.bq("bill.get", fresh)["itc_at_risk"], false)
	eq(t, b.bq("bill.get", edge)["itc_at_risk"], false) // past 150 days, not 150
	flagged := 0
	for _, x := range b.list(b.owner, "bill.list", map[string]any{}) {
		if x.(map[string]any)["itc_at_risk"] == true {
			flagged++
		}
	}
	eq(t, flagged, 1)
	b.payBill(old, n(b.bq("bill.get", old)["total_minor"]))
	eq(t, b.bq("bill.get", old)["itc_at_risk"], false) // paid: no longer at risk
	// a draft carries no credit yet
	d := b.ok("bill.create_draft", map[string]any{"space_id": b.space, "vendor_id": v, "vendor_bill_number": "DRAFT", "bill_date": day(-200),
		"lines": []any{map[string]any{"description": "x", "unit_price_minor": 100}}})
	eq(t, d["itc_at_risk"], false)
}

func (b *bt) acctOr0(code string) int64 {
	for _, a := range b.tb()["accounts"].([]any) {
		if m := a.(map[string]any); m["code"] == code {
			return n(m["balance_minor"])
		}
	}
	return 0
}
