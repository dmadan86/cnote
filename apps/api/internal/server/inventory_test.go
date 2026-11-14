package server_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Inventory (WP13): items, locations and the append-only stock ledger. Bills post receipts and invoices post issues in
// their own transaction; valuation is weighted average cost. Fixtures come from the Rust server.

func (b *bt) sitem(sku string, extra map[string]any) string {
	b.t.Helper()
	in := map[string]any{"sku": sku, "name": "Item " + sku, "hsn_sac": "8471", "unit": "pcs", "price_minor": 30_000}
	for k, v := range extra {
		in[k] = v
	}
	return b.ok("stock_item.create", in)["id"].(string)
}

func (b *bt) onHand(item string) string {
	b.t.Helper()
	return b.ok("stock_item.get", map[string]any{"id": item})["item"].(map[string]any)["on_hand"].(string)
}

// open adds opening stock at a stated cost (paise per unit).
func (b *bt) open13(item string, qty, cost int) {
	b.t.Helper()
	b.ok("stock.adjust", map[string]any{"item_id": item, "qty": qty, "reason": "opening stock", "unit_cost_minor": cost})
}

func (b *bt) stockBill(vendor, number, item string, qty, price int) string {
	b.t.Helper()
	id := b.ok("bill.create_draft", map[string]any{"space_id": b.space, "vendor_id": vendor, "vendor_bill_number": number, "bill_date": day(-3),
		"lines": []any{map[string]any{"description": "Goods", "quantity": qty, "unit_price_minor": price, "stock_item_id": item}}})["id"].(string)
	b.bq("bill.record", id)
	return id
}

func (b *bt) stockInvoice(item string, qty int) string {
	return b.ok("invoice.create_draft", map[string]any{"space_id": b.space, "contact_id": b.contact, "invoice_date": day(0),
		"lines": []any{map[string]any{"description": "Goods", "quantity": qty, "unit_price_minor": 30_000, "stock_item_id": item}}})["id"].(string)
}

func (b *bt) valuation() map[string]any { return b.ok("report.stock_valuation", map[string]any{}) }

func (b *bt) itemValue(item string) (qty string, value, avg int64) {
	b.t.Helper()
	for _, r := range b.valuation()["items"].([]any) {
		if m := r.(map[string]any); m["item_id"] == item {
			return m["on_hand"].(string), n(m["value_minor"]), n(m["avg_cost_minor"])
		}
	}
	b.t.Fatalf("item %s not in valuation", item)
	return
}

func TestStockItemValidationAndLedgerDerivedOnHand(t *testing.T) {
	b := newBT(t, false)
	a := b.sitem("WID-1", nil)
	b.errIs("stock_item.create", map[string]any{"sku": "wid-1", "name": "dup"}, 409, "conflict") // SKU unique, case-insensitive
	for name, in := range map[string]map[string]any{
		"no sku":      {"name": "x"},
		"no name":     {"sku": "A"},
		"bad hsn":     {"sku": "B", "name": "x", "hsn_sac": "12345"},
		"bad rate":    {"sku": "C", "name": "x", "gst_rate_bp": 3000},
		"neg price":   {"sku": "D", "name": "x", "price_minor": -1},
		"bad reorder": {"sku": "E", "name": "x", "reorder_level": "-3"},
		"not object":  nil,
	} {
		if r := b.call(b.owner, "stock_item.create", in); r.status != 400 {
			t.Errorf("%s: want 400, got %d %s", name, r.status, r.body)
		}
	}
	eq(t, b.onHand(a), "0")
	b.errIs("stock.adjust", map[string]any{"item_id": a, "qty": 5, "reason": "x"}, 400, "invalid") // no cost and no stock yet
	b.errIs("stock.adjust", map[string]any{"item_id": a, "qty": 5, "reason": " "}, 400, "invalid") // reason is mandatory
	b.errIs("stock.adjust", map[string]any{"item_id": a, "qty": 0, "reason": "x"}, 400, "invalid")
	b.errIs("stock.adjust", map[string]any{"item_id": a, "qty": 1.2345, "reason": "x"}, 400, "invalid")
	b.open13(a, 10, 10_000)
	eq(t, b.onHand(a), "10")
	b.ok("stock.adjust", map[string]any{"item_id": a, "qty": -2.5, "reason": "breakage"})
	eq(t, b.onHand(a), "7.5")
	// the ledger is the only truth: on hand equals the sum of movements, per item and per location
	eq(t, b.count("select sum(qty)::text from inventory.movements where item_id = '"+a+"'"), "7.5")
	eq(t, b.ok("stock_item.update", map[string]any{"id": a, "name": "Widget", "reorder_level": 3, "active": true})["on_hand"], "7.5")
	if _, bad := b.ok("stock_item.get", map[string]any{"id": a})["item"].(map[string]any)["on_hand_override"]; bad {
		t.Fatal("on hand is not editable")
	}
	b.ok("stock_item.update", map[string]any{"id": a, "sku": "wid-1 "}) // its own SKU is fine
}

func TestStockLedgerIsAppendOnly(t *testing.T) {
	b := newBT(t, false)
	a := b.sitem("AO-1", nil)
	b.open13(a, 4, 5_000)
	if err := b.raw("update inventory.movements set qty = 99"); err == nil {
		t.Fatal("movements must not be updatable by the app role")
	}
	if err := b.raw("delete from inventory.movements"); err == nil {
		t.Fatal("movements must not be deletable by the app role")
	}
	// the database also refuses an inconsistent row: a receipt cannot take stock out, an adjustment needs a reason
	loc := b.count("select id::text from inventory.locations limit 1")
	ins := func(kind, qty, reason string) error {
		return b.raw(fmt.Sprintf("insert into inventory.movements (workspace_id, item_id, location_id, kind, qty, value_minor, reason) values ('%s','%s','%s','%s',%s,0,%s)",
			b.owner.ws, a, loc, kind, qty, reason))
	}
	if ins("receipt", "-1", "null") == nil || ins("adjustment", "1", "null") == nil || ins("issue", "1", "null") == nil {
		t.Fatal("check constraints let a bad movement in")
	}
	eq(t, b.onHand(a), "4")
}

func TestWeightedAverageCostThroughBillsAndInvoices(t *testing.T) {
	b := newBT(t, false)
	a := b.sitem("WAC-1", nil)
	v := b.vendor("29", "Supplier")
	b.open13(a, 10, 10_000) // 10 @ Rs 100
	qty, val, avg := b.itemValue(a)
	eq(t, [3]any{qty, val, avg}, [3]any{"10", int64(100_000), int64(10_000)})

	bill := b.stockBill(v, "WAC-B1", a, 10, 20_000) // 10 @ Rs 200, GST excluded from cost
	qty, val, avg = b.itemValue(a)
	eq(t, [3]any{qty, val, avg}, [3]any{"20", int64(300_000), int64(15_000)})

	inv := b.stockInvoice(a, 5)
	b.issue(inv) // five units leave at the average of Rs 150
	qty, val, avg = b.itemValue(a)
	eq(t, [3]any{qty, val, avg}, [3]any{"15", int64(225_000), int64(15_000)})
	eq(t, b.valuation()["method"], "weighted_average")
	eq(t, n(b.valuation()["total_minor"]), int64(225_000))
	b.assertTBZero() // stock never touches the general ledger

	// movements reference their documents; the ledger shows quantities, never cost
	mv := b.okList("stock.movement_list", map[string]any{"item_id": a})
	eq(t, len(mv), 3)
	top := mv[0].(map[string]any)
	eq(t, [3]any{top["kind"], top["source_type"], top["source_id"]}, [3]any{"issue", "invoice", inv})
	if _, has := top["value_minor"]; has {
		t.Fatal("movement list must not expose cost")
	}
	_ = bill
}

func TestNoNegativeStockUnlessWorkspaceAllows(t *testing.T) {
	b := newBT(t, false)
	a := b.sitem("NEG-1", nil)
	b.open13(a, 3, 1_000)
	inv := b.stockInvoice(a, 5)
	b.errIs("invoice.issue", map[string]any{"id": inv}, 409, "conflict")
	// atomic: nothing was posted, the invoice is still a draft
	eq(t, b.ok("invoice.get", map[string]any{"id": inv})["status"], "draft")
	eq(t, b.onHand(a), "3")
	b.assertTBZero()
	b.errIs("stock.adjust", map[string]any{"item_id": a, "qty": -4, "reason": "lost"}, 409, "conflict")

	b.ok("workspace.update", map[string]any{"settings": map[string]any{"allow_negative_stock": true}})
	b.issue(inv)
	eq(t, b.onHand(a), "-2")
	// negative stock carries no cost until stock arrives
	_, val, _ := b.itemValue(a)
	eq(t, val, int64(0))
	b.ok("workspace.update", map[string]any{"settings": map[string]any{"allow_negative_stock": false}})
	b.ok("invoice.cancel", map[string]any{"id": inv, "reason": "x"}) // undoing an issue only adds stock
	eq(t, b.onHand(a), "3")
}

func TestReversalOnCancelRestoresQuantityAndValue(t *testing.T) {
	b := newBT(t, false)
	a := b.sitem("REV-1", nil)
	v := b.vendor("29", "Rev supplier")
	bill := b.stockBill(v, "REV-B1", a, 10, 20_000)
	eq(t, b.onHand(a), "10")

	// sold stock cannot be taken back out by cancelling the bill that brought it
	inv := b.stockInvoice(a, 6)
	b.issue(inv)
	eq(t, b.onHand(a), "4")
	b.errIs("bill.cancel", map[string]any{"id": bill}, 409, "conflict")
	eq(t, b.bq("bill.get", bill)["status"], "recorded")
	eq(t, b.onHand(a), "4")

	// cancelling the invoice returns the six units at the cost they left with
	b.ok("invoice.cancel", map[string]any{"id": inv, "reason": "customer returned"})
	qty, val, avg := b.itemValue(a)
	eq(t, [3]any{qty, val, avg}, [3]any{"10", int64(200_000), int64(20_000)})
	b.errIs("invoice.cancel", map[string]any{"id": inv, "reason": "again"}, 409, "conflict")
	eq(t, b.onHand(a), "10") // a second cancel cannot double-reverse

	b.bq("bill.cancel", bill)
	qty, val, _ = b.itemValue(a)
	eq(t, [2]any{qty, val}, [2]any{"0", int64(0)})
	eq(t, b.count("select count(*)::text from inventory.movements where kind = 'reversal'"), "2")
	// every reversal points at a different movement
	eq(t, b.count("select count(distinct reverses)::text from inventory.movements where kind = 'reversal'"), "2")
	b.assertTBZero()

	// a draft bill or invoice never posted stock, so cancelling it reverses nothing
	d := b.ok("bill.create_draft", map[string]any{"space_id": b.space, "vendor_id": v, "vendor_bill_number": "REV-B2", "bill_date": day(-1),
		"lines": []any{map[string]any{"description": "G", "quantity": 1, "unit_price_minor": 100, "stock_item_id": a}}})["id"].(string)
	b.bq("bill.cancel", d)
	eq(t, b.count("select count(*)::text from inventory.movements"), "4")
}

func TestTwoIssuesRacingForTheLastUnits(t *testing.T) {
	b := newBT(t, false)
	a := b.sitem("RACE-1", nil)
	b.open13(a, 5, 1_000)
	inv := []string{b.stockInvoice(a, 5), b.stockInvoice(a, 5), b.stockInvoice(a, 5)}
	var wg sync.WaitGroup
	status := make([]int, len(inv))
	for i, id := range inv {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status[i] = b.call(b.owner, "invoice.issue", map[string]any{"id": id}).status
		}()
	}
	wg.Wait()
	won, lost := 0, 0
	for _, s := range status {
		switch s {
		case 200:
			won++
		case 409:
			lost++
		default:
			t.Fatalf("unexpected status %v", status)
		}
	}
	if won != 1 || lost != 2 {
		t.Fatalf("exactly one issue may take the last 5 units: %v", status)
	}
	eq(t, b.onHand(a), "0")
	eq(t, b.count("select count(*)::text from inventory.movements where kind = 'issue'"), "1")
	b.assertTBZero()
}

func TestStockTransferAndLocations(t *testing.T) {
	b := newBT(t, false)
	a := b.sitem("TR-1", nil)
	b.open13(a, 10, 3_000)
	main := b.okList("stock_location.list", map[string]any{})
	eq(t, len(main), 1)
	eq(t, main[0].(map[string]any)["name"], "Main")
	eq(t, main[0].(map[string]any)["is_default"], true)
	shop := b.ok("stock_location.create", map[string]any{"name": "Shop"})["id"].(string)
	b.errIs("stock_location.create", map[string]any{"name": "Shop"}, 409, "conflict")
	mainID := main[0].(map[string]any)["id"].(string)

	b.ok("stock.transfer", map[string]any{"item_id": a, "from_location_id": mainID, "to_location_id": shop, "qty": 4})
	perLoc := map[string]string{}
	for _, l := range b.ok("stock_item.get", map[string]any{"id": a})["locations"].([]any) {
		m := l.(map[string]any)
		perLoc[m["name"].(string)] = m["on_hand"].(string)
	}
	eq(t, perLoc, map[string]string{"Main": "6", "Shop": "4"})
	_, val, _ := b.itemValue(a)
	eq(t, val, int64(30_000)) // a transfer moves cost, it creates none
	eq(t, b.onHand(a), "10")
	b.errIs("stock.transfer", map[string]any{"item_id": a, "from_location_id": shop, "to_location_id": mainID, "qty": 5}, 409, "conflict")
	b.errIs("stock.transfer", map[string]any{"item_id": a, "from_location_id": shop, "to_location_id": shop, "qty": 1}, 400, "invalid")
	b.errIs("stock.transfer", map[string]any{"item_id": a, "from_location_id": shop, "to_location_id": "00000000-0000-4000-8000-000000000000", "qty": 1}, 404, "not_found")
	// the two legs share a transfer id
	eq(t, b.count("select count(distinct transfer_id)::text from inventory.movements where kind in ('transfer_out','transfer_in')"), "1")

	// a location holding stock cannot be archived; the default one cannot either
	b.errIs("stock_location.update", map[string]any{"id": shop, "active": false}, 409, "conflict")
	b.errIs("stock_location.update", map[string]any{"id": mainID, "active": false}, 409, "conflict")
	b.ok("stock.transfer", map[string]any{"item_id": a, "from_location_id": shop, "to_location_id": mainID, "qty": 4})
	b.ok("stock_location.update", map[string]any{"id": shop, "active": false})
	b.errIs("stock.transfer", map[string]any{"item_id": a, "from_location_id": mainID, "to_location_id": shop, "qty": 1}, 409, "conflict")
	d := b.ok("stock_location.update", map[string]any{"id": shop, "active": true, "is_default": true})
	eq(t, d["is_default"], true)
	// invoices now issue from the new default
	inv := b.stockInvoice(a, 1)
	b.errIs("invoice.issue", map[string]any{"id": inv}, 409, "conflict") // Shop is empty
}

func TestInventoryRolesAgentsAndTenants(t *testing.T) {
	b := newBT(t, false)
	a := b.sitem("RB-1", nil)
	b.open13(a, 5, 1_000)
	member := b.memberAs("Mel", "member")
	admin := b.memberAs("Ada", "admin")
	guest := b.memberAs("Gus", "guest")

	b.okAs(member, "stock_item.list", map[string]any{})
	b.okAs(member, "stock_item.create", map[string]any{"sku": "RB-2", "name": "Member made"})
	b.okAs(member, "stock.transfer", map[string]any{"item_id": a, "from_location_id": b.okList("stock_location.list", map[string]any{})[0].(map[string]any)["id"], "to_location_id": b.ok("stock_location.create", map[string]any{"name": "Van"})["id"], "qty": 1})
	b.errAs(member, "stock.adjust", map[string]any{"item_id": a, "qty": -1, "reason": "x"}, 403, "forbidden")
	b.errAs(member, "stock_location.create", map[string]any{"name": "Nope"}, 403, "forbidden")
	b.errAs(member, "stock_location.update", map[string]any{"id": "00000000-0000-4000-8000-000000000000", "name": "x"}, 403, "forbidden")
	b.okAs(admin, "stock.adjust", map[string]any{"item_id": a, "qty": -1, "reason": "damaged"})
	b.okAs(admin, "stock_location.create", map[string]any{"name": "Godown"})
	for _, c := range []string{"stock_item.list", "stock_item.get", "stock_location.list", "stock.movement_list", "report.stock_valuation"} {
		b.errAs(guest, c, map[string]any{"id": a}, 403, "forbidden")
	}
	b.errAs(guest, "stock_item.create", map[string]any{"sku": "G", "name": "g"}, 403, "forbidden")
	if r := b.call(who{}, "stock_item.list", map[string]any{}); r.status != 401 {
		t.Fatalf("no auth: %d", r.status)
	}

	// cost is finance data: a member without finance access cannot read the valuation, quantities stay open
	b.ok("member.update", map[string]any{"user_id": member.userID, "finance_access": false})
	b.errAs(member, "report.stock_valuation", map[string]any{}, 403, "forbidden")
	b.okAs(member, "stock_item.get", map[string]any{"id": a})
	b.okAs(admin, "report.stock_valuation", map[string]any{})

	// machine callers: the manage_invoices group is needed, and an adjustment waits for a person to confirm
	b.enableGroups("read", "manage_invoices")
	agent := who{ws: b.owner.ws, bearer: b.must(b.owner, "agent.create", map[string]any{"name": "Stocker", "scopes": []string{"read", "manage_invoices"}})["api_key"].(string)}
	b.okAs(agent, "stock_item.list", map[string]any{})
	r := b.okAs(agent, "stock.adjust", map[string]any{"item_id": a, "qty": -1, "reason": "agent write-off"})
	eq(t, r["confirmation_required"], true)
	eq(t, b.onHand(a), "4")      // 5 minus the admin's 1; the agent's write-off did not run
	key := b.env.apiKey(b.owner) // an owner's API key is a machine caller too
	r = b.okAs(key, "stock.adjust", map[string]any{"item_id": a, "qty": -1, "reason": "key write-off"})
	eq(t, r["confirmation_required"], true)
	eq(t, b.onHand(a), "4")

	// another workspace sees nothing and cannot touch these ids
	o := b.env.tenant(false)
	eq(t, len(o.okList("stock_item.list", map[string]any{})), 0)
	o.errIs("stock_item.get", map[string]any{"id": a}, 404, "not_found")
	o.errIs("stock.adjust", map[string]any{"item_id": a, "qty": 1, "reason": "x", "unit_cost_minor": 1}, 404, "not_found")
	o.errIs("stock_item.update", map[string]any{"id": a, "name": "x"}, 404, "not_found")
	if r := o.call(o.owner, "invoice.create_draft", map[string]any{"space_id": o.space, "contact_id": o.contact,
		"lines": []any{map[string]any{"description": "x", "unit_price_minor": 1, "stock_item_id": a}}}); r.status != 400 {
		t.Fatalf("a line must not reference another workspace's item: %d %s", r.status, r.body)
	}
	if r := o.cmd(o.gosv.URL, o.owner, b.owner.ws, "stock_item.list", map[string]any{}, nil); r.status == 200 {
		t.Fatalf("foreign workspace path: %s", r.body)
	}
	eq(t, len(o.okList("stock_location.list", map[string]any{})), 1) // its own Main
	eq(t, b.count("select count(*)::text from inventory.locations where name = 'Main'"), "1")
}

func TestStockAdjustIdempotentReplay(t *testing.T) {
	b := newBT(t, false)
	a := b.sitem("ID-1", nil)
	h := map[string]string{"Idempotency-Key": "adj-" + rnd()}
	in := map[string]any{"item_id": a, "qty": 4, "reason": "opening", "unit_cost_minor": 100}
	r1 := b.cmd(b.gosv.URL, b.owner, b.owner.ws, "stock.adjust", in, h)
	r2 := b.cmd(b.gosv.URL, b.owner, b.owner.ws, "stock.adjust", in, h)
	if r1.status != 200 || r2.status != 200 || string(r1.body) != string(r2.body) {
		t.Fatalf("replay differs: %d %s / %d %s", r1.status, r1.body, r2.status, r2.body)
	}
	eq(t, b.onHand(a), "4")
	eq(t, b.count("select count(*)::text from inventory.movements"), "1")
}

func TestLowStockTileOnHome(t *testing.T) {
	b := newBT(t, false)
	if _, has := b.home(b.owner)["tiles"].(map[string]any)["low_stock"]; has {
		t.Fatal("a workspace without items has no stock tile")
	}
	a := b.sitem("LS-1", map[string]any{"reorder_level": 5})
	b.sitem("LS-2", nil) // no reorder level: never low
	b.open13(a, 5, 100)
	tile := b.home(b.owner)["tiles"].(map[string]any)["low_stock"].(map[string]any)
	eq(t, tile["count"], float64(1)) // at the reorder level counts as low
	eq(t, tile["items"], float64(2))
	eq(t, len(tile["sample"].([]any)), 1)
	b.open13(a, 1, 100)
	eq(t, b.home(b.owner)["tiles"].(map[string]any)["low_stock"].(map[string]any)["count"], float64(0))
	if got := b.okList("stock_item.list", map[string]any{"low": true}); len(got) != 0 {
		t.Fatalf("low filter: %v", got)
	}
	b.ok("stock.adjust", map[string]any{"item_id": a, "qty": -3, "reason": "sale"})
	if got := b.okList("stock_item.list", map[string]any{"low": true}); len(got) != 1 || !strings.Contains(fmt.Sprint(got[0]), "LS-1") {
		t.Fatalf("low filter: %v", got)
	}
	// archived items drop out of the tile and the default list
	b.ok("stock_item.update", map[string]any{"id": a, "active": false})
	eq(t, b.home(b.owner)["tiles"].(map[string]any)["low_stock"].(map[string]any)["count"], float64(0))
}

func TestStockLinkSurvivesQuoteConversion(t *testing.T) {
	b := newBT(t, false)
	a := b.sitem("Q-1", nil)
	b.open13(a, 5, 100)
	q := b.ok("quote.create_draft", map[string]any{"space_id": b.space, "contact_id": b.contact, "valid_until": day(10),
		"lines": []any{map[string]any{"description": "Goods", "quantity": 2, "unit_price_minor": 500, "stock_item_id": a}}})["id"].(string)
	b.ok("quote.send", map[string]any{"id": q})
	b.ok("quote.accept", map[string]any{"id": q})
	conv := b.ok("quote.convert_to_invoice", map[string]any{"id": q})
	inv := conv["invoice"].(map[string]any)["id"].(string)
	got := b.ok("invoice.get", map[string]any{"id": inv})["lines"].([]any)[0].(map[string]any)
	eq(t, got["stock_item_id"], a)
	b.issue(inv)
	eq(t, b.onHand(a), "3")
}
