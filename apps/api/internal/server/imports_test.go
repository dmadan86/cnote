package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/modules/billing"
	"github.com/cnote/api/internal/worker"
)

// Data importers (docs/design/importers.md): upload -> preview -> run (worker) -> status, duplicates, gates, tenant
// isolation, the opening balances posting, opening documents out of every tax path, undo and idempotent runs.

// upload POSTs the raw file like the web does and returns the response.
func (b *bt) impUpload(w who, kind, body string, extra ...string) resp {
	b.t.Helper()
	q := url.Values{"kind": {kind}, "filename": {kind + ".csv"}}
	for i := 0; i+1 < len(extra); i += 2 {
		q.Set(extra[i], extra[i+1])
	}
	req, _ := http.NewRequest("POST", b.gosv.URL+"/api/w/"+w.ws+"/imports?"+q.Encode(), strings.NewReader(body))
	w.with(req.Header)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	bs, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, bs}
}

func (b *bt) impOK(kind, body string) string {
	b.t.Helper()
	r := b.impUpload(b.owner, kind, body)
	if r.status != 200 {
		b.t.Fatalf("upload %s: %d %s", kind, r.status, r.body)
	}
	return r.json()["id"].(string)
}

func (b *bt) ipreview(id string) map[string]any {
	return b.ok("data_import.preview", map[string]any{"id": id})
}

func (b *bt) icounts(id string) (ready, fixN, existing int) {
	c := b.ipreview(id)["counts"].(map[string]any)
	return int(n(c["ready"])), int(n(c["fix"])), int(n(c["existing"]))
}

func (b *bt) waitImport(id string) map[string]any {
	b.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		s := b.ok("data_import.status", map[string]any{"id": id})
		if st := s["status"]; st == "done" || st == "failed" {
			return s
		}
		time.Sleep(200 * time.Millisecond)
	}
	b.t.Fatal("import did not finish")
	return nil
}

func (b *bt) runImport(id string) map[string]any {
	b.ok("data_import.run", map[string]any{"id": id})
	return b.waitImport(id)
}

func (b *bt) contactNames() []string {
	var out []string
	for _, c := range b.okList("contact.list", map[string]any{"limit": 500}) {
		out = append(out, c.(map[string]any)["name"].(string))
	}
	return out
}

func hasStr(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

var (
	gstinMH = billing.CompleteGSTIN("27AAPFU0939F1Z")
	gstinKA = billing.CompleteGSTIN("29ABCDE1234F1Z")
)

func contactsCSV() string {
	return "Name,Type,Email,Phone,GSTIN,State\n" +
		"Acme Traders,customer,acme@example.test,9876500001,,Karnataka\n" +
		"Bharat Supplies,vendor,,9876500002," + gstinMH + ",\n" +
		"Bad Gstin Co,customer,,,27AAAAA0000A1Z5,\n" +
		"Dup Of Existing,customer,,," + gstinKA + ",\n" +
		"=cmd|' /C calc'!A0,customer,,,,\n"
}

func TestImportContactsEndToEnd(t *testing.T) {
	b := newBT(t, false)
	b.ok("contact.create", map[string]any{"space_id": b.space, "name": "Already Here", "kind": "company", "gstin": gstinKA})
	id := b.impOK("contacts", contactsCSV())
	p := b.ipreview(id)
	ready, fixN, existing := b.icounts(id)
	eq(t, ready, 3) // Acme, Bharat, and the formula-looking name (a valid name; the error CSV neutralises it)
	eq(t, fixN, 1)
	eq(t, existing, 1)
	rows := p["rows"].([]any)
	eq(t, len(rows), 5)
	if r := rows[2].(map[string]any); r["status"] != "fix" || r["reason"] == nil {
		t.Fatalf("bad GSTIN row: %v", r)
	}
	if r := rows[3].(map[string]any); r["status"] != "existing" {
		t.Fatalf("duplicate row: %v", r)
	}
	if cols := p["columns"].([]any); len(cols) != 6 || cols[0].(map[string]any)["field"] != "name" {
		t.Fatalf("columns: %v", cols)
	}
	errs := b.ok("data_import.errors", map[string]any{"id": id})
	if !strings.Contains(errs["csv"].(string), "Bad Gstin Co") || errs["filename"] == "" {
		t.Fatalf("errors csv: %v", errs)
	}
	// nothing is written before run
	eq(t, hasStr(b.contactNames(), "Acme Traders"), false)

	s := b.runImport(id)
	eq(t, s["status"], "done")
	eq(t, n(s["added"]), int64(3))
	eq(t, n(s["skipped"]), int64(2))
	eq(t, n(s["total"]), int64(5))
	names := b.contactNames()
	eq(t, hasStr(names, "Acme Traders") && hasStr(names, "Bharat Supplies"), true)
	eq(t, hasStr(names, "Bad Gstin Co"), false)
	bh := b.okList("vendor.list", map[string]any{})
	eq(t, len(bh), 1)
	// the GSTIN gave the state; the audit trail has the events
	for _, k := range []string{"data_import.started", "data_import.completed"} {
		if len(b.auditKind(k)) == 0 {
			t.Errorf("no %s event", k)
		}
	}
	// a second run is a no-op, and the same file again finds everything already there
	b.ok("data_import.run", map[string]any{"id": id})
	eq(t, len(b.contactNames()), 5) // Buyer 29, Already Here + 3
	id2 := b.impOK("contacts", contactsCSV())
	ready, _, existing = b.icounts(id2)
	eq(t, ready, 0)
	eq(t, existing, 4)
	b.errIs("data_import.run", map[string]any{"id": id2}, 400, "invalid") // nothing ready

	list := b.okList("data_import.list", map[string]any{})
	eq(t, len(list) >= 2, true)

	// undo removes the three added contacts, not the one that was already there
	u := b.ok("data_import.undo", map[string]any{"id": id})
	eq(t, u["status"], "undone")
	names = b.contactNames()
	eq(t, hasStr(names, "Acme Traders"), false)
	eq(t, hasStr(names, "Already Here"), true)
	b.ok("data_import.undo", map[string]any{"id": id}) // again: nothing to do
}

func TestImportGatesAndTenantIsolation(t *testing.T) {
	b := newBT(t, false)
	member := b.memberAs("Mia", "member")
	noFin := b.memberAs("Nolan", "admin")
	b.ok("member.update", map[string]any{"user_id": noFin.userID, "finance_access": false})
	// an admin keeps finance access whatever the flag says; a plain member is refused everywhere
	if r := b.impUpload(member, "contacts", contactsCSV()); r.status != 403 {
		t.Fatalf("member upload: %d %s", r.status, r.body)
	}
	id := b.impOK("contacts", contactsCSV())
	for _, cmd := range []string{"data_import.preview", "data_import.run", "data_import.status", "data_import.errors", "data_import.undo"} {
		b.errAs(member, cmd, map[string]any{"id": id}, 403, "forbidden")
	}
	for _, cmd := range []string{"data_import.list", "opening.get", "opening.post", "opening.reverse", "data_import.template"} {
		if cmd == "data_import.template" {
			b.errAs(member, cmd, map[string]any{"kind": "contacts"}, 403, "forbidden")
			continue
		}
		b.errAs(member, cmd, map[string]any{}, 403, "forbidden")
	}
	b.errAs(member, "opening.save", map[string]any{"lines": []any{}}, 403, "forbidden")
	if r := b.impUpload(b.owner, "nonsense", "a,b"); r.status != 400 {
		t.Fatalf("bad kind: %d", r.status)
	}
	if r := b.impUpload(b.owner, "contacts", strings.Repeat("a,b\n", 3_000_000)); r.status != 400 {
		t.Fatalf("over 10 MB: %d", r.status)
	}
	if r := b.impUpload(who{ws: b.owner.ws}, "contacts", contactsCSV()); r.status != 401 {
		t.Fatalf("anonymous: %d", r.status)
	}

	// another workspace cannot see, preview, run, undo or read the errors of this job
	other := newBT(t, false)
	for _, cmd := range []string{"data_import.preview", "data_import.run", "data_import.status", "data_import.errors", "data_import.undo"} {
		if r := other.call(other.owner, cmd, map[string]any{"id": id}); r.status != 404 {
			t.Errorf("%s across tenants: %d %s", cmd, r.status, r.body)
		}
	}
	for _, j := range other.okList("data_import.list", map[string]any{}) {
		t.Fatalf("foreign job listed: %v", j)
	}
	if tpl := b.ok("data_import.template", map[string]any{"kind": "items"}); len(tpl["fields"].([]any)) == 0 || tpl["sample_csv"] == "" {
		t.Fatalf("template: %v", tpl)
	}
}

func itemsCSV() string {
	return "SKU,Name,HSN/SAC,GST rate,Unit,Sale price,Opening stock,Opening stock value,Location\n" +
		"W-1,Steel widget,7318,18,Nos,250.00,10,\"1,000.00\",Main\n" +
		"W-2,Gadget,,12,pcs,99.50,,,\n" +
		"W-3,No rate item,,,pcs,10,,,\n"
}

func TestImportItemsWithOpeningStockAndUndo(t *testing.T) {
	b := newBT(t, false)
	b.sitem("W-9", map[string]any{"name": "Existing nine"})
	id := b.impOK("items", itemsCSV()+"W-9,Whatever,,18,pcs,1,,,\n")
	ready, fixN, existing := b.icounts(id)
	eq(t, []int{ready, fixN, existing}, []int{2, 1, 1})
	s := b.runImport(id)
	eq(t, n(s["added"]), int64(2))
	var w1 string
	for _, it := range b.okList("stock_item.list", map[string]any{}) {
		if m := it.(map[string]any); m["sku"] == "W-1" {
			w1 = m["id"].(string)
		}
	}
	eq(t, w1 != "", true)
	eq(t, b.onHand(w1), "10")
	_, value, _ := b.itemValue(w1)
	eq(t, value, int64(100_000))

	// stock sold since the import blocks the undo; nothing changes
	inv := b.ok("invoice.create_draft", map[string]any{"space_id": b.space, "contact_id": b.contact, "invoice_date": day(0),
		"lines": []any{map[string]any{"description": "w", "quantity": 4, "unit_price_minor": 25_000, "stock_item_id": w1}}})["id"].(string)
	b.issue(inv)
	r := b.errIs("data_import.undo", map[string]any{"id": id}, 409, "conflict")
	if !strings.Contains(string(r.body), "W-1") || !strings.Contains(string(r.body), "row 2") {
		t.Fatalf("reason per row: %s", r.body)
	}
	eq(t, b.onHand(w1), "6")
	eq(t, b.ok("data_import.status", map[string]any{"id": id})["status"], "done")

	// cancel the sale: stock is back, undo reverses the whole job
	b.ok("invoice.cancel", map[string]any{"id": inv, "reason": "test"})
	eq(t, b.ok("data_import.undo", map[string]any{"id": id})["status"], "undone")
	eq(t, b.onHand(w1), "0")
	for _, it := range b.okList("stock_item.list", map[string]any{}) {
		if m := it.(map[string]any); m["sku"] == "W-1" {
			eq(t, m["active"], false)
		}
	}
}

const docsCSV = "Type,Party name,Document number,Date,Due date,Amount\n" +
	"invoice,Acme Traders,OLD-1,15-03-2026,14-04-2026,\"1,18,000.00\"\n" +
	"bill,Bharat Supplies,B-77,01-03-2026,,50000\n" +
	"invoice,Nobody Ltd,OLD-2,15-03-2026,,100\n"

// openingFixture: contacts imported, then the opening documents; returns the document job.
func (b *bt) openingFixture() (contacts, docs string, inv, bill string) {
	b.t.Helper()
	contacts = b.impOK("contacts", contactsCSV())
	b.runImport(contacts)
	id := b.impOK("opening_docs", docsCSV)
	ready, fixN, _ := b.icounts(id)
	eq(b.t, []int{ready, fixN}, []int{2, 1})
	b.runImport(id)
	for _, i := range b.okList("invoice.list", map[string]any{"limit": 100}) {
		if m := i.(map[string]any); m["number"] == "OLD-1" {
			inv = m["id"].(string)
		}
	}
	for _, i := range b.okList("bill.list", map[string]any{"limit": 100}) {
		if m := i.(map[string]any); m["vendor_bill_number"] == "B-77" {
			bill = m["id"].(string)
		}
	}
	eq(b.t, inv != "" && bill != "", true)
	return contacts, id, inv, bill
}

func (b *bt) acctBal(code string) (dr, cr int64) {
	for _, a := range b.tb()["accounts"].([]any) {
		if m := a.(map[string]any); m["code"] == code {
			return n(m["debit_minor"]), n(m["credit_minor"])
		}
	}
	return 0, 0
}

func TestOpeningBalancesPostAndReverse(t *testing.T) {
	b := newBT(t, false)
	_, docs, inv, bill := b.openingFixture()
	// opening documents never post on their own
	d, c := b.acctBal("1100")
	eq(t, []int64{d, c}, []int64{0, 0})
	// the documents behave as normal ones for payment and ageing
	age := b.ok("report.receivables_ageing", map[string]any{})
	eq(t, n(age["total_minor"]), int64(11_800_000))
	if r := b.call(b.owner, "credit_note.create", map[string]any{"invoice_id": inv, "reason": "x", "lines": []any{map[string]any{"description": "x", "unit_price_minor": 1}}}); r.status != 409 {
		t.Fatalf("credit note on opening invoice: %d %s", r.status, r.body)
	}
	b.errIs("invoice.cancel", map[string]any{"id": inv, "reason": "x"}, 409, "conflict")
	b.errIs("invoice.generate_irn", map[string]any{"id": inv}, 409, "conflict")
	b.errIs("bill.cancel", map[string]any{"id": bill}, 409, "conflict")

	g := b.ok("opening.get", map[string]any{})
	eq(t, g["status"], "draft")
	eq(t, n(g["invoices"].(map[string]any)["total_minor"]), int64(11_800_000))
	eq(t, n(g["bills"].(map[string]any)["total_minor"]), int64(5_000_000))
	b.errIs("opening.save", map[string]any{"go_live": "2026-04-01", "lines": []any{map[string]any{"account_code": "1100", "dr_minor": 5}}}, 400, "invalid")
	b.errIs("opening.save", map[string]any{"go_live": "2026-04-01", "lines": []any{map[string]any{"account_code": "1200", "dr_minor": 5, "cr_minor": 5}}}, 400, "invalid")
	b.errIs("opening.save", map[string]any{"go_live": "2026-13-01", "lines": []any{}}, 400, "invalid")
	g = b.ok("opening.save", map[string]any{"go_live": "2026-04-01", "lines": []any{map[string]any{"account_code": "1200", "dr_minor": 2_000_000}}})
	// debits 11.8M + 2M, credits 5M: 3900 takes a credit of 8.8M
	eq(t, n(g["difference_minor"]), int64(8_800_000))

	post := b.ok("opening.post", map[string]any{})
	eq(t, post["status"], "posted")
	for code, want := range map[string][2]int64{"1100": {11_800_000, 0}, "2100": {0, 5_000_000}, "1200": {2_000_000, 0}, "3900": {0, 8_800_000}} {
		d, c := b.acctBal(code)
		eq(t, []int64{d, c}, want[:])
	}
	entries := adminScalar(b.env, b.owner.ws, "select count(*) from billing.journal_entries where source_type = 'opening_balance'")
	eq(t, entries, "1")
	if at := adminScalar(b.env, b.owner.ws, "select (posted_at at time zone 'Asia/Kolkata')::date::text from billing.journal_entries where source_type = 'opening_balance'"); at != "2026-03-31" {
		t.Fatalf("dated %s, want the day before go-live", at)
	}
	b.errIs("opening.post", map[string]any{}, 409, "conflict") // unique
	b.errIs("opening.save", map[string]any{"go_live": "2026-04-01", "lines": []any{}}, 409, "conflict")
	// while posted, more opening documents and the undo of the document import are refused
	b.errIs("data_import.undo", map[string]any{"id": docs}, 409, "conflict")
	more := b.impOK("opening_docs", "Type,Party name,Document number,Date,Amount\ninvoice,Acme Traders,OLD-9,01-03-2026,10\n")
	b.errIs("data_import.run", map[string]any{"id": more}, 409, "conflict")

	rev := b.ok("opening.reverse", map[string]any{})
	eq(t, rev["status"], "reversed")
	for _, code := range []string{"1100", "2100", "1200", "3900"} {
		d, c := b.acctBal(code)
		eq(t, d, c) // the exact negation
	}
	b.errIs("opening.reverse", map[string]any{}, 409, "conflict")
	eq(t, adminScalar(b.env, b.owner.ws, "select count(*) from billing.journal_entries where source_type = 'opening_balance_reversal'"), "1")
	// saving a reversed page makes it a draft again; it can be posted once more
	b.ok("opening.save", map[string]any{"go_live": "2026-04-01", "lines": []any{}})
	eq(t, b.ok("opening.post", map[string]any{})["status"], "posted")
}

func TestOpeningDocumentsAreOutOfEveryTaxPath(t *testing.T) {
	b := newBT(t, true) // aato above 10 crore: the IRN window sweep is on
	// control documents that must show up
	ctl := b.issue(b.draft(b.contact, -25))["id"].(string)
	_, _, _, bill := b.openingFixture()
	old := b.impOK("opening_docs", "Type,Party name,Document number,Date,Amount\ninvoice,Bharat Supplies,OLD-5,"+todayIST().AddDate(0, 0, -25).Format("02-01-2006")+",500\n")
	b.runImport(old)
	var oldInv string
	for _, i := range b.okList("invoice.list", map[string]any{"limit": 100}) {
		if m := i.(map[string]any); m["number"] == "OLD-5" {
			oldInv = m["id"].(string)
		}
	}
	eq(t, oldInv != "", true)
	b.ok("opening.save", map[string]any{"go_live": "2026-04-01", "lines": []any{map[string]any{"account_code": "1200", "dr_minor": 1000}}})
	b.ok("opening.post", map[string]any{})

	span := map[string]any{"from": "2020-01-01", "to": "2030-12-31"}
	t.Run("gst summary", func(t *testing.T) {
		s := b.ok("report.gst_summary", span)
		blob, _ := json.Marshal(s)
		eq(t, n(s["totals"].(map[string]any)["taxable_minor"]) < 11_800_000, true)
		_ = blob
	})
	t.Run("gstr1", func(t *testing.T) {
		for _, period := range []string{"2026-03", todayIST().Format("2006-01"), todayIST().AddDate(0, 0, -25).Format("2006-01")} {
			if r := b.ok("report.gstr1", map[string]any{"period": period}); strings.Contains(string(mustMarshal(r)), "OLD-1") {
				t.Fatalf("opening invoice in GSTR-1 %s", period)
			}
		}
	})
	t.Run("itc summary", func(t *testing.T) {
		s := b.ok("report.itc_summary", span)
		eq(t, n(s["totals"].(map[string]any)["taxable_minor"]), int64(0))
	})
	t.Run("tds", func(t *testing.T) {
		vendor := b.vendorID("Bharat Supplies")
		b.ok("vendor.set_tax", map[string]any{"id": vendor, "pan": "ABCDE1234F", "default_tds_code": "professional"})
		pay := b.ok("bill.pay", map[string]any{"id": bill, "amount_minor": 2_000_000})
		eq(t, n(pay["paid_minor"]), int64(2_000_000))
		eq(t, adminScalar(b.env, b.owner.ws, "select coalesce(sum(tds_minor),0) from billing.purchase_payments"), "0")
		eq(t, n(b.ok("report.tds_payable", span)["total_tds_minor"]), int64(0))
		// the payment is real money: the ageing sees what is still owed
		eq(t, n(b.ok("report.payables_ageing", map[string]any{})["total_minor"]), int64(3_000_000))
	})
	t.Run("tally export", func(t *testing.T) {
		x := b.ok("export.tally", map[string]any{"from": "2020-01-01", "to": "2030-12-31"})["xml"].(string)
		if strings.Contains(x, "Opening balance") || strings.Contains(x, "OLD-1") {
			t.Fatalf("opening entry or document in the Tally export: %.300s", x)
		}
		eq(t, strings.Contains(x, "Sales"), true) // the control invoice is there
	})
	t.Run("report builder", func(t *testing.T) {
		r := b.ok("report.run", map[string]any{"spec": map[string]any{"dataset": "invoices", "columns": []string{"number"}}})
		if strings.Contains(string(mustMarshal(r)), "OLD-1") {
			t.Fatal("opening invoice in the invoices dataset")
		}
		r = b.ok("report.run", map[string]any{"spec": map[string]any{"dataset": "purchase_bills", "columns": []string{"vendor_bill_number"}}})
		if strings.Contains(string(mustMarshal(r)), "B-77") {
			t.Fatal("opening bill in the purchase bills dataset")
		}
	})
	t.Run("irn window sweep", func(t *testing.T) {
		w := worker.New(b.pool, command.NewRegistry(modules.Commands()...))
		if _, err := worker.IRNWindowSweep(context.Background(), w, todayIST()); err != nil {
			t.Fatal(err)
		}
		eq(t, count(b.events(ctl), "invoice.irn_window_warning"), 1)
		eq(t, count(b.events(oldInv), "invoice.irn_window_warning"), 0)
		eq(t, adminScalar(b.env, b.owner.ws, "select count(*) from billing.invoices where note_id = '"+oldInv+"' and buyer_gstin is not null"), "1") // it would be warned if it were not opening
	})
}

func mustMarshal(v any) []byte { x, _ := json.Marshal(v); return x }

func (b *bt) vendorID(name string) string {
	for _, v := range b.okList("vendor.list", map[string]any{}) {
		if m := v.(map[string]any); m["name"] == name {
			return m["id"].(string)
		}
	}
	b.t.Fatalf("vendor %s", name)
	return ""
}

func TestImportUndoPreflightRefusals(t *testing.T) {
	b := newBT(t, false)
	cid, docs, inv, _ := b.openingFixture()

	// a contact used on a (opening) document blocks the contact undo
	r := b.errIs("data_import.undo", map[string]any{"id": cid}, 409, "conflict")
	if !strings.Contains(string(r.body), "used on a document") {
		t.Fatalf("reason: %s", r.body)
	}
	eq(t, hasStr(b.contactNames(), "Acme Traders"), true)
	// a payment on an opening invoice blocks the document undo
	b.ok("payment.record", map[string]any{"invoice_id": inv, "amount_minor": 100_000, "method": "bank"})
	r = b.errIs("data_import.undo", map[string]any{"id": docs}, 409, "conflict")
	if !strings.Contains(string(r.body), "payment") {
		t.Fatalf("reason: %s", r.body)
	}
	// nothing was cancelled by the refused undo (all or nothing)
	eq(t, len(b.okList("invoice.list", map[string]any{"limit": 100})), 1)
	eq(t, adminScalar(b.env, b.owner.ws, "select count(*) from billing.purchase_bills where opening and status <> 'cancelled'"), "1")
	eq(t, b.ok("data_import.status", map[string]any{"id": docs})["status"], "done")
}

func TestImportUndoOpeningDocumentsAndRerun(t *testing.T) {
	b := newBT(t, false)
	_, docs, _, _ := b.openingFixture()
	eq(t, b.ok("data_import.undo", map[string]any{"id": docs})["status"], "undone")
	eq(t, adminScalar(b.env, b.owner.ws, "select count(*) from billing.invoices where opening and status <> 'cancelled'"), "0")
	// the numbers are free again: the same file imports again
	again := b.impOK("opening_docs", docsCSV)
	ready, _, _ := b.icounts(again)
	eq(t, ready, 2)
	eq(t, n(b.runImport(again)["added"]), int64(2))
}

func TestImportRunIsIdempotent(t *testing.T) {
	b := newBT(t, false)
	id := b.impOK("contacts", contactsCSV())
	// two runs at once: one claims the job, the other is a no-op
	done := make(chan map[string]any, 2)
	for range 2 {
		go func() { done <- b.call(b.owner, "data_import.run", map[string]any{"id": id}).json() }()
	}
	<-done
	<-done
	b.waitImport(id)
	eq(t, adminScalar(b.env, b.owner.ws, "select count(*) from importers.rows where job_id = '"+id+"' and status = 'added'"), "4")
	// a duplicate worker job after completion changes nothing
	b.adminSQL("insert into core.jobs (workspace_id, kind, payload, run_at) select workspace_id, 'data_import.run', jsonb_build_object('job_id', id), now() from importers.jobs where id = '" + id + "'")
	time.Sleep(2 * time.Second)
	eq(t, len(b.contactNames()), 5) // Buyer 29 + 4
	// resume: a row put back to ready (a crash before its status was written) is applied once, the others untouched
	b.adminSQL("update importers.jobs set status = 'queued', finished_at = null where id = '" + id + "'")
	b.adminSQL("update importers.rows set status = 'ready', target_id = null where job_id = '" + id + "' and row_no = 2")
	b.adminSQL("insert into core.jobs (workspace_id, kind, payload, run_at) select workspace_id, 'data_import.run', jsonb_build_object('job_id', id), now() from importers.jobs where id = '" + id + "'")
	b.waitImport(id)
	names := b.contactNames()
	eq(t, len(names), 6) // Acme Traders is created a second time: the duplicate guard is the preview, so the run trusts its rows
	if os.Getenv("IMPORT_DEBUG") != "" {
		t.Log(names)
	}
}

func TestImportTallyMasters(t *testing.T) {
	b := newBT(t, false)
	xml, err := os.ReadFile("../dataimport/testdata/masters_prime2_utf8.xml")
	if err != nil {
		t.Fatal(err)
	}
	r := b.impUpload(b.owner, "tally", string(xml))
	if r.status != 200 {
		t.Fatalf("tally upload: %d %s", r.status, r.body)
	}
	id := r.json()["id"].(string)
	p := b.ipreview(id)
	found := p["tally"].(map[string]any)["found"].(map[string]any)
	eq(t, n(found["customers"]) > 0, true)
	eq(t, n(found["items"]) > 0, true)
	// skip the items; ledgers fill the opening-balances draft, never a posting
	b.ok("data_import.run", map[string]any{"id": id, "include": map[string]any{"customers": true, "vendors": true, "ledgers": true, "items": false}})
	b.waitImport(id)
	eq(t, len(b.okList("stock_item.list", map[string]any{})), 0)
	eq(t, len(b.contactNames()) > 0, true)
	g := b.ok("opening.get", map[string]any{})
	eq(t, g["status"], "draft")
	eq(t, len(g["lines"].([]any)) > 0, true)
	eq(t, adminScalar(b.env, b.owner.ws, "select count(*) from billing.journal_entries where source_type = 'opening_balance'"), "0")
	// undo takes the contacts and the draft lines back
	b.ok("data_import.undo", map[string]any{"id": id})
	eq(t, len(b.ok("opening.get", map[string]any{})["lines"].([]any)), 0)
}
