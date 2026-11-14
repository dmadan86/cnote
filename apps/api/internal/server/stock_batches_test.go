package server_test

import (
	"fmt"
	"sync"
	"testing"
)

// WP21: batch and serial tracking on the stock ledger.

// trackedBill records a bill with one tracked line.
func (b *bt) trackedBill(vendor, number, item string, qty int, price int, tr map[string]any) string {
	b.t.Helper()
	id := b.ok("bill.create_draft", map[string]any{"space_id": b.space, "vendor_id": vendor, "vendor_bill_number": number, "bill_date": day(-3),
		"lines": []any{map[string]any{"description": "Goods", "quantity": qty, "unit_price_minor": price, "stock_item_id": item, "tracking": tr}}})["id"].(string)
	b.bq("bill.record", id)
	return id
}

func (b *bt) trackedInvoice(item string, qty int, tr map[string]any) string {
	b.t.Helper()
	line := map[string]any{"description": "Goods", "quantity": qty, "unit_price_minor": 30_000, "stock_item_id": item}
	if tr != nil {
		line["tracking"] = tr
	}
	return b.ok("invoice.create_draft", map[string]any{"space_id": b.space, "contact_id": b.contact, "invoice_date": day(0), "lines": []any{line}})["id"].(string)
}

// batches maps batch number to on-hand quantity for an item.
func (b *bt) batches(item string) map[string]string {
	b.t.Helper()
	out := map[string]string{}
	for _, r := range b.okList("stock_batch.list", map[string]any{"item_id": item, "include_empty": true}) {
		m := r.(map[string]any)
		out[m["number"].(string)] = m["on_hand"].(string)
	}
	return out
}

func (b *bt) batchItem(sku string) (item, vendor string) {
	return b.sitem(sku, map[string]any{"tracking": "batch"}), b.vendor("29", "Supplier "+sku)
}

func TestFEFOIssuePicksEarliestExpiryFirst(t *testing.T) {
	b := newBT(t, false)
	a, v := b.batchItem("FEFO-1")
	b.trackedBill(v, "F1", a, 5, 10_000, map[string]any{"batch": "LATE", "expiry_date": day(90)})
	b.trackedBill(v, "F2", a, 5, 20_000, map[string]any{"batch": "SOON", "mfg_date": day(-30), "expiry_date": day(30)})
	b.trackedBill(v, "F3", a, 5, 30_000, map[string]any{"batch": "NOEXP"})
	eq(t, b.onHand(a), "15")
	eq(t, b.batches(a), map[string]string{"LATE": "5", "SOON": "5", "NOEXP": "5"})
	list := b.okList("stock_batch.list", map[string]any{"item_id": a})
	eq(t, list[0].(map[string]any)["number"], "SOON") // listed first-expiry-first, no expiry last
	eq(t, list[2].(map[string]any)["number"], "NOEXP")

	b.issue(b.trackedInvoice(a, 7, nil)) // 5 from SOON then 2 from LATE
	eq(t, b.batches(a), map[string]string{"LATE": "3", "NOEXP": "5", "SOON": "0"})
	// weighted average per item: 15 units cost 5*100+5*200+5*300 = 3000 rupees, average 200; 7 units leave at 200 each
	qty, val, avg := b.itemValue(a)
	eq(t, [3]any{qty, val, avg}, [3]any{"8", int64(160_000), int64(20_000)})

	// explicit picks override FEFO; they must add up and fit
	b.errIs("invoice.issue", map[string]any{"id": b.trackedInvoice(a, 2, map[string]any{"picks": []any{map[string]any{"batch_id": b.batchID(a, "NOEXP"), "qty": 1}}})}, 400, "invalid")
	b.errIs("invoice.issue", map[string]any{"id": b.trackedInvoice(a, 4, map[string]any{"picks": []any{map[string]any{"batch_id": b.batchID(a, "LATE"), "qty": 4}}})}, 409, "conflict")
	b.issue(b.trackedInvoice(a, 2, map[string]any{"picks": []any{map[string]any{"batch_id": b.batchID(a, "NOEXP"), "qty": 2}}}))
	eq(t, b.batches(a), map[string]string{"LATE": "3", "NOEXP": "3", "SOON": "0"})
	b.errIs("invoice.issue", map[string]any{"id": b.trackedInvoice(a, 7, nil)}, 409, "conflict") // only 6 left
	b.errIs("stock.adjust", map[string]any{"item_id": a, "qty": -1, "reason": "x", "tracking": map[string]any{"picks": []any{map[string]any{"batch_id": b.batchID(a, "SOON"), "qty": 1}}}}, 409, "conflict")

	// a receipt must name its batch and may not contradict the batch dates
	b.errIs("bill.record", map[string]any{"id": b.draftBill(v, "F4", a, 1, nil)}, 400, "invalid")
	b.errIs("bill.record", map[string]any{"id": b.draftBill(v, "F5", a, 1, map[string]any{"batch": "late", "expiry_date": day(91)})}, 409, "conflict")
	b.errIs("bill.record", map[string]any{"id": b.draftBill(v, "F6", a, 1, map[string]any{"batch": "X", "mfg_date": day(5), "expiry_date": day(1)})}, 400, "invalid")
	b.trackedBill(v, "F7", a, 2, 10_000, map[string]any{"batch": "late"}) // same batch, number is case-insensitive
	eq(t, b.batches(a)["LATE"], "5")
	b.assertTBZero()
}

func (b *bt) draftBill(vendor, number, item string, qty int, tr map[string]any) string {
	line := map[string]any{"description": "Goods", "quantity": qty, "unit_price_minor": 100, "stock_item_id": item}
	if tr != nil {
		line["tracking"] = tr
	}
	return b.ok("bill.create_draft", map[string]any{"space_id": b.space, "vendor_id": vendor, "vendor_bill_number": number, "bill_date": day(-3), "lines": []any{line}})["id"].(string)
}

func (b *bt) batchID(item, number string) string {
	return b.count(fmt.Sprintf("select id::text from inventory.batches where item_id = '%s' and number = '%s'", item, number))
}

func TestSerialsAreUniqueAcrossBillsInvoicesAndRaces(t *testing.T) {
	b := newBT(t, false)
	a := b.sitem("SER-1", map[string]any{"tracking": "serial"})
	v := b.vendor("29", "Serial supplier")
	b.trackedBill(v, "S1", a, 2, 50_000, map[string]any{"serials": []any{"SN-1", "SN-2"}})
	eq(t, b.onHand(a), "2")
	eq(t, len(b.okList("stock_serial.list", map[string]any{"item_id": a})), 2)
	// the same serial cannot come in twice, and the count must match the quantity
	b.errIs("bill.record", map[string]any{"id": b.draftBill(v, "S2", a, 1, map[string]any{"serials": []any{"SN-1"}})}, 409, "conflict")
	b.errIs("bill.record", map[string]any{"id": b.draftBill(v, "S3", a, 2, map[string]any{"serials": []any{"SN-3"}})}, 400, "invalid")
	b.errIs("bill.record", map[string]any{"id": b.draftBill(v, "S4", a, 2, map[string]any{"serials": []any{"SN-3", "SN-3"}})}, 400, "invalid")
	b.errIs("bill.record", map[string]any{"id": b.draftBill(v, "S5", a, 1, nil)}, 400, "invalid")
	eq(t, b.onHand(a), "2")

	// racing receipts of one new serial: exactly one wins
	var bills []string
	for i := range 4 {
		bills = append(bills, b.draftBill(v, fmt.Sprintf("SR%d", i), a, 1, map[string]any{"serials": []any{"SN-RACE"}}))
	}
	eq(t, b.winners(bills, "bill.record"), 1)
	eq(t, b.onHand(a), "3")

	// issuing: a serial leaves once, from where it is; serial items need serials
	b.errIs("invoice.issue", map[string]any{"id": b.trackedInvoice(a, 1, nil)}, 400, "invalid")
	b.errIs("invoice.issue", map[string]any{"id": b.trackedInvoice(a, 1, map[string]any{"serials": []any{"NOPE"}})}, 409, "conflict")
	inv := b.trackedInvoice(a, 1, map[string]any{"serials": []any{"SN-1"}})
	b.issue(inv)
	b.errIs("invoice.issue", map[string]any{"id": b.trackedInvoice(a, 1, map[string]any{"serials": []any{"SN-1"}})}, 409, "conflict") // sold already
	// racing invoices for one serial: exactly one wins
	var invs []string
	for range 4 {
		invs = append(invs, b.trackedInvoice(a, 1, map[string]any{"serials": []any{"SN-2"}}))
	}
	eq(t, b.winners(invs, "invoice.issue"), 1)
	eq(t, b.onHand(a), "1")

	// cancelling the invoice puts the serial back; the bill that brought it can then be cancelled only once it is back
	b.ok("invoice.cancel", map[string]any{"id": inv, "reason": "returned"})
	eq(t, len(b.okList("stock_serial.list", map[string]any{"item_id": a})), 2)
	b.issue(b.trackedInvoice(a, 1, map[string]any{"serials": []any{"SN-1"}})) // it can be sold again
	// a serial lives in one place: transfer moves it, and it cannot leave from the old place afterwards
	main := b.okList("stock_location.list", map[string]any{})[0].(map[string]any)["id"]
	van := b.ok("stock_location.create", map[string]any{"name": "Van"})["id"]
	b.ok("stock.transfer", map[string]any{"item_id": a, "from_location_id": main, "to_location_id": van, "qty": 1, "tracking": map[string]any{"serials": []any{"SN-RACE"}}})
	b.errIs("stock.transfer", map[string]any{"item_id": a, "from_location_id": main, "to_location_id": van, "qty": 1, "tracking": map[string]any{"serials": []any{"SN-RACE"}}}, 409, "conflict")
	eq(t, b.count("select (location_id = '"+van.(string)+"')::text from inventory.movements where serial = 'SN-RACE' group by location_id having sum(qty) > 0"), "true")
	eq(t, b.count("select count(*)::text from (select serial from inventory.movements where serial is not null group by item_id, serial having sum(qty) > 1) x"), "0")
	b.assertTBZero()
}

func (b *bt) winners(ids []string, cmd string) int {
	b.t.Helper()
	var wg sync.WaitGroup
	status := make([]int, len(ids))
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status[i] = b.call(b.owner, cmd, map[string]any{"id": id}).status
		}()
	}
	wg.Wait()
	won := 0
	for _, s := range status {
		switch s {
		case 200:
			won++
		case 409:
		default:
			b.t.Fatalf("unexpected status %v", status)
		}
	}
	return won
}

func TestExpiredBatchesCannotBeIssuedWithoutAnAdminOverride(t *testing.T) {
	b := newBT(t, false)
	a, v := b.batchItem("EXP-1")
	b.trackedBill(v, "E1", a, 4, 10_000, map[string]any{"batch": "OLD", "expiry_date": day(-2)})
	b.trackedBill(v, "E2", a, 3, 10_000, map[string]any{"batch": "FRESH", "expiry_date": day(60)})
	old := b.batchID(a, "OLD")
	eq(t, b.okList("stock_batch.list", map[string]any{"item_id": a})[0].(map[string]any)["expired"], true)

	b.issue(b.trackedInvoice(a, 3, nil)) // FEFO skips the expired batch
	eq(t, b.batches(a), map[string]string{"OLD": "4", "FRESH": "0"})
	b.errIs("invoice.issue", map[string]any{"id": b.trackedInvoice(a, 1, nil)}, 409, "conflict")
	picks := map[string]any{"picks": []any{map[string]any{"batch_id": old, "qty": 1}}}
	b.errIs("invoice.issue", map[string]any{"id": b.trackedInvoice(a, 1, picks)}, 409, "conflict")
	eq(t, b.batches(a)["OLD"], "4")

	// a member cannot override; an admin can, with a reason that stays on the ledger
	member := b.memberAs("Mel", "member")
	over := map[string]any{"override_expired_reason": "clearance sale to a scrap dealer"}
	inv := b.trackedInvoice(a, 2, over)
	b.errAs(member, "invoice.issue", map[string]any{"id": inv}, 403, "forbidden")
	b.okAs(b.memberAs("Ada", "admin"), "invoice.issue", map[string]any{"id": inv})
	eq(t, b.batches(a)["OLD"], "2")
	eq(t, b.count("select reason from inventory.movements where kind = 'issue' and batch_id = '"+old+"'"), "expired stock issued: clearance sale to a scrap dealer")
	// the override does not widen anything else: writing expired stock off is an admin adjustment
	b.ok("stock.adjust", map[string]any{"item_id": a, "qty": -2, "reason": "expired, destroyed", "tracking": map[string]any{"picks": []any{map[string]any{"batch_id": old, "qty": 2}}}})
	eq(t, b.batches(a)["OLD"], "0")
}

func TestCancelRestoresBatchesAndSerials(t *testing.T) {
	b := newBT(t, false)
	a, v := b.batchItem("CAN-1")
	bill := b.trackedBill(v, "C1", a, 6, 10_000, map[string]any{"batch": "B1", "expiry_date": day(40)})
	b.trackedBill(v, "C2", a, 4, 10_000, map[string]any{"batch": "B2", "expiry_date": day(80)})
	inv := b.trackedInvoice(a, 8, nil) // 6 from B1, 2 from B2
	b.issue(inv)
	eq(t, b.batches(a), map[string]string{"B1": "0", "B2": "2"})
	b.errIs("bill.cancel", map[string]any{"id": bill}, 409, "conflict") // its batch has been sold
	b.ok("invoice.cancel", map[string]any{"id": inv, "reason": "order cancelled"})
	eq(t, b.batches(a), map[string]string{"B1": "6", "B2": "4"})
	qty, val, _ := b.itemValue(a)
	eq(t, [2]any{qty, val}, [2]any{"10", int64(100_000)})
	b.bq("bill.cancel", bill)
	eq(t, b.batches(a), map[string]string{"B1": "0", "B2": "4"})

	s := b.sitem("CAN-2", map[string]any{"tracking": "serial"})
	sb := b.trackedBill(v, "C3", s, 2, 5_000, map[string]any{"serials": []any{"A1", "A2"}})
	si := b.trackedInvoice(s, 1, map[string]any{"serials": []any{"A1"}})
	b.issue(si)
	b.errIs("bill.cancel", map[string]any{"id": sb}, 409, "conflict")
	b.ok("invoice.cancel", map[string]any{"id": si, "reason": "returned"})
	eq(t, len(b.okList("stock_serial.list", map[string]any{"item_id": s})), 2)
	b.bq("bill.cancel", sb)
	eq(t, len(b.okList("stock_serial.list", map[string]any{"item_id": s})), 0)
	eq(t, b.onHand(s), "0")
	b.assertTBZero()
}

func TestTrackingModeSwitchNeedsZeroStock(t *testing.T) {
	b := newBT(t, false)
	a := b.sitem("SW-1", nil)
	eq(t, b.ok("stock_item.get", map[string]any{"id": a})["item"].(map[string]any)["tracking"], "none")
	b.errIs("stock_item.update", map[string]any{"id": a, "tracking": "lot"}, 400, "invalid")
	b.errIs("stock_item.create", map[string]any{"sku": "SW-X", "name": "x", "tracking": "lot"}, 400, "invalid")
	b.open13(a, 3, 100)
	b.errIs("stock_item.update", map[string]any{"id": a, "tracking": "batch"}, 409, "conflict")
	b.ok("stock_item.update", map[string]any{"id": a, "tracking": "none", "name": "same mode is fine"})
	b.ok("stock.adjust", map[string]any{"item_id": a, "qty": -3, "reason": "clear"})
	eq(t, b.ok("stock_item.update", map[string]any{"id": a, "tracking": "batch"})["tracking"], "batch")
	// untracked stock has no batch: adding tracked stock needs the batch, and plain tracking on an untracked item is refused
	b.errIs("stock.adjust", map[string]any{"item_id": a, "qty": 2, "reason": "found", "unit_cost_minor": 100}, 400, "invalid")
	b.ok("stock.adjust", map[string]any{"item_id": a, "qty": 2, "reason": "found", "unit_cost_minor": 100, "tracking": map[string]any{"batch": "OPEN", "expiry_date": day(10)}})
	b.errIs("stock_item.update", map[string]any{"id": a, "tracking": "serial"}, 409, "conflict")
	u := b.sitem("SW-2", nil)
	b.errIs("stock.adjust", map[string]any{"item_id": u, "qty": 2, "reason": "x", "unit_cost_minor": 1, "tracking": map[string]any{"batch": "B"}}, 400, "invalid")
	if r := b.call(b.owner, "invoice.create_draft", map[string]any{"space_id": b.space, "contact_id": b.contact, "invoice_date": day(0),
		"lines": []any{map[string]any{"description": "x", "unit_price_minor": 1, "tracking": "B1"}}}); r.status != 400 {
		t.Fatalf("tracking must be an object: %d %s", r.status, r.body)
	}
}

func TestExpiryReportAndHomeTile(t *testing.T) {
	b := newBT(t, false)
	if _, has := b.home(b.owner)["tiles"].(map[string]any)["expiring_stock"]; has {
		t.Fatal("no batch items, no tile")
	}
	a, v := b.batchItem("TILE-1")
	b.trackedBill(v, "T1", a, 2, 100, map[string]any{"batch": "GONE", "expiry_date": day(-1)})
	b.trackedBill(v, "T2", a, 2, 100, map[string]any{"batch": "SOON", "expiry_date": day(20)})
	b.trackedBill(v, "T3", a, 2, 100, map[string]any{"batch": "LATER", "expiry_date": day(45)})
	b.trackedBill(v, "T4", a, 2, 100, map[string]any{"batch": "NONE"})
	rep := b.ok("report.stock_expiry", map[string]any{})
	eq(t, len(rep["batches"].([]any)), 2) // expired and within 30 days
	eq(t, len(b.ok("report.stock_expiry", map[string]any{"days": 60})["batches"].([]any)), 3)
	b.errIs("report.stock_expiry", map[string]any{"days": -1}, 400, "invalid")
	tile := b.home(b.owner)["tiles"].(map[string]any)["expiring_stock"].(map[string]any)
	eq(t, [2]any{tile["soon"], tile["expired"]}, [2]any{float64(1), float64(1)})
	// batches without stock drop out
	b.issue(b.trackedInvoice(a, 2, map[string]any{"override_expired_reason": "scrap"}))
	eq(t, b.home(b.owner)["tiles"].(map[string]any)["expiring_stock"].(map[string]any)["soon"], float64(1))
}

func TestBatchRolesAgentsAndTenants(t *testing.T) {
	b := newBT(t, false)
	a, v := b.batchItem("BR-1")
	b.trackedBill(v, "R1", a, 3, 100, map[string]any{"batch": "B1", "expiry_date": day(10)})
	member := b.memberAs("Mel", "member")
	guest := b.memberAs("Gus", "guest")
	for _, c := range []string{"stock_batch.list", "stock_serial.list", "report.stock_expiry"} {
		b.okAs(member, c, map[string]any{"item_id": a})
		b.errAs(guest, c, map[string]any{"item_id": a}, 403, "forbidden")
	}
	b.errAs(member, "stock.adjust", map[string]any{"item_id": a, "qty": -1, "reason": "x"}, 403, "forbidden")
	// an agent's tracked write-off waits for a person, like any adjustment
	b.enableGroups("read", "manage_invoices")
	agent := who{ws: b.owner.ws, bearer: b.must(b.owner, "agent.create", map[string]any{"name": "Batcher", "scopes": []string{"read", "manage_invoices"}})["api_key"].(string)}
	b.okAs(agent, "stock_batch.list", map[string]any{})
	r := b.okAs(agent, "stock.adjust", map[string]any{"item_id": a, "qty": -1, "reason": "agent", "tracking": map[string]any{"picks": []any{map[string]any{"batch_id": b.batchID(a, "B1"), "qty": 1}}}})
	eq(t, r["confirmation_required"], true)
	eq(t, b.batches(a)["B1"], "3")
	// a different workspace sees no batches, cannot read them, and cannot issue them
	o := b.env.tenant(false)
	eq(t, len(o.okList("stock_batch.list", map[string]any{})), 0)
	eq(t, len(o.okList("stock_batch.list", map[string]any{"item_id": a})), 0)
	o.errIs("stock.adjust", map[string]any{"item_id": a, "qty": -1, "reason": "x", "tracking": map[string]any{"picks": []any{map[string]any{"batch_id": b.batchID(a, "B1"), "qty": 1}}}}, 404, "not_found")
	if err := b.raw("insert into inventory.batches (workspace_id, item_id, number) values ('" + o.owner.ws + "', '" + a + "', 'X')"); err == nil {
		t.Fatal("a batch of another workspace's item must be refused by the composite foreign key")
	}
}
