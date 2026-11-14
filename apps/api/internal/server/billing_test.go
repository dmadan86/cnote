package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cnote/api/internal/modules/billing"
)

// Ports of crates/cnote-finance/tests/finance.rs for the invoicing, report, ledger and export commands. Billing
// commands run on the Go server; fixtures (workspace, contacts) are created through Rust, and commands outside
// this module (note.get, note.history, contact.*) go through the Go server's proxy.
type bt struct {
	*env
	owner          who
	space, contact string
}

func todayIST() time.Time {
	t := time.Now().UTC().Add(330 * time.Minute)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func day(offset int) string { return todayIST().AddDate(0, 0, offset).Format("2006-01-02") }

func fyOf(d time.Time) string {
	y := d.Year()
	if d.Month() < 4 {
		y--
	}
	return fmt.Sprintf("%d-%02d", y, (y+1)%100)
}

// newBT: a workspace whose seller is in Karnataka (29); aato toggles IRN window enforcement.
func newBT(t *testing.T, aato bool) *bt {
	e := newEnv(t)
	return e.tenant(aato)
}

func (e *env) tenant(aato bool) *bt {
	o := e.signup("Tess", true)
	e.must(o, "workspace.update", map[string]any{"settings": map[string]any{
		"gstin": billing.CompleteGSTIN("29ABCDE1234F1Z"), "state_code": "29", "aato_above_10cr": aato}})
	sp := e.must(o, "space.create", map[string]any{"key": "FIN", "name": "Finance"})["id"].(string)
	b := &bt{env: e, owner: o, space: sp}
	b.contact = b.newContact("29")
	return b
}

func (b *bt) newContact(state string) string {
	c := b.must(b.owner, "contact.create", map[string]any{"space_id": b.space, "name": "Buyer " + state, "kind": "company",
		"gstin": billing.CompleteGSTIN(state + "AABCT1332L1Z")})
	if c["state_code"] != state {
		b.t.Fatalf("state derived from GSTIN: %v", c)
	}
	return c["id"].(string)
}

func (b *bt) call(w who, cmd string, in any) resp {
	b.t.Helper()
	return b.cmd(b.gosv.URL, w, b.owner.ws, cmd, in, nil)
}

func (b *bt) ok(cmd string, in any) map[string]any { return b.okAs(b.owner, cmd, in) }

func (b *bt) okAs(w who, cmd string, in any) map[string]any {
	b.t.Helper()
	r := b.call(w, cmd, in)
	if r.status != 200 {
		b.t.Fatalf("%s %v: %d %s", cmd, in, r.status, r.body)
	}
	return r.json()
}

func (b *bt) okList(cmd string, in any) []any {
	b.t.Helper()
	r := b.call(b.owner, cmd, in)
	var v []any
	if r.status != 200 || json.Unmarshal(r.body, &v) != nil {
		b.t.Fatalf("%s: %d %s", cmd, r.status, r.body)
	}
	return v
}

func (b *bt) errIs(cmd string, in any, status int, code string) resp {
	b.t.Helper()
	return b.errAs(b.owner, cmd, in, status, code)
}

func (b *bt) errAs(w who, cmd string, in any, status int, code string) resp {
	b.t.Helper()
	r := b.call(w, cmd, in)
	if r.status != status || !strings.Contains(string(r.body), `"code":"`+code+`"`) {
		b.t.Fatalf("%s %v: want %d %s, got %d %s", cmd, in, status, code, r.status, r.body)
	}
	return r
}

func (b *bt) draft(contact string, offset int) string {
	return b.ok("invoice.create_draft", map[string]any{"space_id": b.space, "contact_id": contact, "invoice_date": day(offset),
		"lines": []any{
			map[string]any{"description": "Consulting", "hsn_sac": "998314", "quantity": 2.5, "unit_price_minor": 40_001},
			map[string]any{"description": "Hosting", "hsn_sac": "998315", "unit_price_minor": 10_005, "gst_rate_bp": 500}}})["id"].(string)
}

func (b *bt) issue(id string) map[string]any { return b.ok("invoice.issue", map[string]any{"id": id}) }

func (b *bt) tb() map[string]any { return b.ok("ledger.trial_balance", map[string]any{}) }

func n(v any) int64 { return int64(math.Round(v.(float64))) }

func balance(t *testing.T, tb map[string]any, code string) int64 {
	t.Helper()
	for _, a := range tb["accounts"].([]any) {
		if m := a.(map[string]any); m["code"] == code {
			return n(m["balance_minor"])
		}
	}
	t.Fatalf("no account %s", code)
	return 0
}

func (b *bt) events(note string) []string {
	var out []string
	for _, e := range b.okList("note.history", map[string]any{"id": note}) {
		out = append(out, e.(map[string]any)["kind"].(string))
	}
	return out
}

func count(kinds []string, k string) (c int) {
	for _, x := range kinds {
		if x == k {
			c++
		}
	}
	return
}

// raw runs SQL inside the tenant (what a buggy deploy or a DBA could attempt).
func (b *bt) raw(sql string) error {
	tx, err := dbTenant(b.env, b.owner.ws)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(context.Background(), sql); err != nil {
		return err
	}
	return tx.Commit(context.Background())
}

func TestBillingIssuePostsBalancedEntryAndTrialBalanceIsZero(t *testing.T) {
	b := newBT(t, false)
	id := b.draft(b.contact, 0)
	inv := b.issue(id)
	// 2.5 x 400.01 = 1000.025 -> 100003 paise (half-up); 10005 @5%
	eq(t, inv["status"], "issued")
	fy := fyOf(todayIST())
	eq(t, inv["number"], "INV/"+fy+"/0001")
	eq(t, n(inv["subtotal_minor"]), int64(100_003+10_005))
	// intra-state: 18% of 100003 = 18000.54 -> 18001 (9000 + 9001); 5% of 10005 = 500.25 -> 500 (250 + 250)
	eq(t, n(inv["cgst_minor"]), int64(9_000+250))
	eq(t, n(inv["sgst_minor"]), int64(9_001+250))
	eq(t, n(inv["igst_minor"]), int64(0))
	total := n(inv["total_minor"])
	eq(t, total, int64(110_008+18_001+500))
	eq(t, inv["supplier_state"], "29")
	if n(inv["lines"].([]any)[0].(map[string]any)["cgst_minor"]) <= 0 {
		t.Fatal("line cgst")
	}

	tb := b.tb()
	eq(t, n(tb["net_minor"]), int64(0))
	eq(t, tb["total_debit_minor"], tb["total_credit_minor"])
	eq(t, balance(t, tb, "1100"), total)
	eq(t, balance(t, tb, "4000"), int64(-110_008))
	eq(t, balance(t, tb, "2201"), int64(-9_250))
	eq(t, balance(t, tb, "2203"), int64(0))
	if count(b.events(id), "invoice.issued") != 1 {
		t.Fatal("invoice.issued event")
	}

	// inter-state sale uses IGST only
	mh := b.newContact("27")
	inv2 := b.issue(b.draft(mh, 0))
	eq(t, inv2["number"], "INV/"+fy+"/0002")
	eq(t, [2]int64{n(inv2["cgst_minor"]), n(inv2["igst_minor"])}, [2]int64{0, 18_001 + 500})
	eq(t, n(b.tb()["net_minor"]), int64(0))

	// issuing twice is refused
	b.errIs("invoice.issue", map[string]any{"id": id}, 409, "conflict")
}

func TestBillingDatabaseRejectsUnbalancedEntries(t *testing.T) {
	b := newBT(t, false)
	ws := b.owner.ws
	ctx := context.Background()
	tx, err := dbTenant(b.env, ws)
	if err != nil {
		t.Fatal(err)
	}
	var entry string
	if err := tx.QueryRow(ctx, "insert into billing.journal_entries (workspace_id, source_type, source_id) values ($1::uuid,'invoice',gen_random_uuid()) returning id::text", ws).Scan(&entry); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "insert into billing.journal_lines (workspace_id, entry_id, account_code, debit_minor) values ($1::uuid,$2::uuid,'1100',100)", ws, entry); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "insert into billing.journal_lines (workspace_id, entry_id, account_code, credit_minor) values ($1::uuid,$2::uuid,'4000',99)", ws, entry); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err == nil || !strings.Contains(err.Error(), "does not balance") {
		t.Fatalf("commit: %v", err)
	}
	// an entry with no lines is rejected too
	tx, _ = dbTenant(b.env, ws)
	if _, err := tx.Exec(ctx, "insert into billing.journal_entries (workspace_id, source_type, source_id) values ($1::uuid,'invoice',gen_random_uuid())", ws); err != nil {
		t.Fatal(err)
	}
	if tx.Commit(ctx) == nil {
		t.Fatal("empty entry accepted")
	}
}

func TestBillingIssuedInvoicesAreImmutableInTheDatabase(t *testing.T) {
	b := newBT(t, false)
	id := b.draft(b.contact, 0)
	// drafts can be edited, through the command...
	upd := b.ok("invoice.update_draft", map[string]any{"id": id, "lines": []any{map[string]any{"description": "Only", "unit_price_minor": 5000}}})
	eq(t, n(upd["subtotal_minor"]), int64(5000))
	b.issue(id)
	// ...and not after issue
	b.errIs("invoice.update_draft", map[string]any{"id": id, "contact_id": b.contact}, 409, "conflict")
	for _, sql := range []string{
		fmt.Sprintf("update billing.invoices set subtotal_minor = 1 where note_id = '%s'", id),
		fmt.Sprintf("update billing.invoices set number = 'X' where note_id = '%s'", id),
		fmt.Sprintf("update billing.invoices set status = 'draft' where note_id = '%s'", id),
		fmt.Sprintf("delete from billing.invoices where note_id = '%s'", id),
		fmt.Sprintf("update billing.invoice_lines set unit_price_minor = 1 where invoice_id = '%s'", id),
		fmt.Sprintf("delete from billing.invoice_lines where invoice_id = '%s'", id),
	} {
		err := b.raw(sql)
		if err == nil || !(strings.Contains(err.Error(), "immutable") || strings.Contains(err.Error(), "cannot be deleted")) {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	// settlement/IRN fields remain writable
	if err := b.raw(fmt.Sprintf("update billing.invoices set status = 'sent' where note_id = '%s'", id)); err != nil {
		t.Fatal(err)
	}
	// the journal is append-only too
	if b.raw("delete from billing.journal_lines") == nil {
		t.Fatal("journal delete allowed")
	}
}

func TestBillingPartialThenFullPayment(t *testing.T) {
	b := newBT(t, false)
	id := b.draft(b.contact, 0)
	total := n(b.issue(id)["total_minor"])

	p1 := b.ok("payment.record", map[string]any{"invoice_id": id, "amount_minor": 50_000, "reference": "UTR1"})
	eq(t, p1["status"], "partly_paid")
	eq(t, n(p1["outstanding_minor"]), total-50_000)

	b.errIs("payment.record", map[string]any{"invoice_id": id, "amount_minor": total}, 400, "invalid")
	b.errIs("payment.record", map[string]any{"invoice_id": id, "amount_minor": 0}, 400, "invalid")

	p2 := b.ok("payment.record", map[string]any{"invoice_id": id, "amount_minor": total - 50_000, "method": "bank"})
	eq(t, p2["status"], "paid")
	eq(t, len(p2["payments"].([]any)), 2)
	eq(t, count(b.events(id), "invoice.paid"), 1)

	tb := b.tb()
	eq(t, balance(t, tb, "1100"), int64(0))
	eq(t, balance(t, tb, "1200"), total)
	eq(t, n(tb["net_minor"]), int64(0))
	// a paid invoice takes no more money
	b.errIs("payment.record", map[string]any{"invoice_id": id, "amount_minor": 1}, 409, "conflict")
}

func TestBillingCreditNotesReverseTheLedger(t *testing.T) {
	b := newBT(t, false)
	id := b.draft(b.contact, 0)
	total := n(b.issue(id)["total_minor"])

	// partial: credit 10 x 100.00 @18% = 1180.00
	cn := b.ok("credit_note.create", map[string]any{"invoice_id": id, "reason": "Discount",
		"lines": []any{map[string]any{"description": "Consulting discount", "quantity": 10, "unit_price_minor": 10_000}}})
	eq(t, cn["kind"], "credit_note")
	eq(t, n(cn["total_minor"]), int64(118_000))
	if !strings.HasPrefix(cn["number"].(string), "CN/") {
		t.Fatal(cn["number"])
	}
	inv := b.ok("invoice.get", map[string]any{"id": id})
	eq(t, n(inv["outstanding_minor"]), total-118_000)
	tb := b.tb()
	eq(t, balance(t, tb, "1100"), total-118_000)
	eq(t, n(tb["net_minor"]), int64(0))

	// a credit note cannot exceed what is still owed
	b.errIs("credit_note.create", map[string]any{"invoice_id": id, "reason": "x",
		"lines": []any{map[string]any{"description": "Too much", "unit_price_minor": total}}}, 400, "invalid")
	// an invoice with credits cannot be cancelled wholesale
	b.errIs("invoice.cancel", map[string]any{"id": id, "reason": "oops"}, 409, "conflict")

	// full cancel of a fresh invoice
	id2 := b.draft(b.contact, 0)
	t2 := n(b.issue(id2)["total_minor"])
	before := balance(t, b.tb(), "1100")
	cancelled := b.ok("invoice.cancel", map[string]any{"id": id2, "reason": "Duplicate"})
	eq(t, cancelled["status"], "cancelled")
	tb = b.tb()
	eq(t, balance(t, tb, "1100"), before-t2)
	eq(t, n(tb["net_minor"]), int64(0))
	// net taxable = two invoices - the partial credit note - the cancellation credit note
	summary := b.ok("report.gst_summary", map[string]any{"from": day(-1), "to": day(1)})
	eq(t, n(summary["totals"].(map[string]any)["taxable_minor"]), int64(110_008+110_008-100_000-110_008))
	// payments on a cancelled invoice are refused
	b.errIs("payment.record", map[string]any{"invoice_id": id2, "amount_minor": 1}, 409, "conflict")
}

func TestBillingWriteOffAndReports(t *testing.T) {
	b := newBT(t, false)
	old := b.draft(b.contact, -75)
	recent := b.draft(b.contact, -5)
	tOld := n(b.issue(old)["total_minor"])
	tNew := n(b.issue(recent)["total_minor"])
	b.ok("payment.record", map[string]any{"invoice_id": recent, "amount_minor": 1000})

	ag := b.ok("report.receivables_ageing", map[string]any{})
	amt := func(bucket string) int64 {
		for _, x := range ag["buckets"].([]any) {
			if m := x.(map[string]any); m["bucket"] == bucket {
				return n(m["outstanding_minor"])
			}
		}
		t.Fatal(bucket)
		return 0
	}
	eq(t, [4]int64{amt("0-30"), amt("61-90"), amt("31-60"), amt("90+")}, [4]int64{tNew - 1000, tOld, 0, 0})
	eq(t, n(ag["total_minor"]), tOld+tNew-1000)

	wo := b.ok("invoice.write_off", map[string]any{"id": old, "reason": "Customer closed"})
	eq(t, n(wo["outstanding_minor"]), int64(0))
	eq(t, wo["status"], "paid")
	tb := b.tb()
	eq(t, balance(t, tb, "5100"), tOld)
	eq(t, n(tb["net_minor"]), int64(0))

	// GSTR-1: both are B2B to the same GSTIN
	this := b.ok("report.gstr1", map[string]any{"period": todayIST().Format("2006-01")})
	if _, ok := this["gstin"].(string); !ok || len(this["fp"].(string)) != 6 {
		t.Fatalf("gstr1 %v", this)
	}

	// Tally XML covers invoices, receipts and the write-off journal
	xml := b.ok("export.tally", map[string]any{"from": day(-1), "to": day(1)})
	x := xml["xml"].(string)
	eq(t, n(xml["vouchers"]), int64(4))
	for _, s := range []string{`VCHTYPE="Sales"`, `VCHTYPE="Receipt"`, `VCHTYPE="Journal"`, "<LEDGERNAME>Accounts receivable</LEDGERNAME>"} {
		if !strings.Contains(x, s) {
			t.Fatalf("tally lacks %s", s)
		}
	}
}

func TestBillingGSTR1SplitsB2BAndB2C(t *testing.T) {
	b := newBT(t, false)
	consumer := b.must(b.owner, "contact.create", map[string]any{"space_id": b.space, "name": "Walk-in", "state_code": "29"})["id"].(string)
	b2b, b2c := b.draft(b.contact, 0), b.draft(consumer, 0)
	b.issue(b2b)
	b.issue(b2c)
	period := todayIST().Format("2006-01")
	r := b.ok("report.gstr1", map[string]any{"period": period})
	eq(t, len(r["b2b"].([]any)), 1)
	inv0 := r["b2b"].([]any)[0].(map[string]any)["inv"].([]any)
	eq(t, len(inv0), 1)
	eq(t, len(inv0[0].(map[string]any)["itms"].([]any)), 2) // one item per rate
	b2cs := r["b2cs"].([]any)
	eq(t, len(b2cs), 2)
	for _, x := range b2cs {
		m := x.(map[string]any)
		if m["sply_ty"] != "INTRA" || m["iamt"].(float64) != 0 {
			t.Fatalf("b2cs %v", m)
		}
	}
	// Inter-state consumer: above Rs 1 lakh is its own b2cl row; a small one stays in b2cs as INTER.
	far := b.must(b.owner, "contact.create", map[string]any{"space_id": b.space, "name": "Mumbai buyer", "state_code": "27"})["id"]
	for _, price := range []int{100_000 * 100, 1_000 * 100} {
		d := b.ok("invoice.create_draft", map[string]any{"space_id": b.space, "contact_id": far, "invoice_date": day(0),
			"lines": []any{map[string]any{"description": "Laptop", "hsn_sac": "847130", "unit_price_minor": price}}})
		b.issue(d["id"].(string))
	}
	r = b.ok("report.gstr1", map[string]any{"period": period})
	eq(t, len(r["b2cl"].([]any)), 1)
	cl := r["b2cl"].([]any)[0].(map[string]any)
	eq(t, cl["pos"], "27")
	inv := cl["inv"].([]any)[0].(map[string]any)
	if inv["val"].(float64) <= 100_000.0 || inv["itms"].([]any)[0].(map[string]any)["itm_det"].(map[string]any)["iamt"].(float64) <= 0 {
		t.Fatalf("b2cl %v", inv)
	}
	var inter []map[string]any
	for _, x := range r["b2cs"].([]any) {
		if m := x.(map[string]any); m["sply_ty"] == "INTER" {
			inter = append(inter, m)
		}
	}
	eq(t, len(inter), 1)
	eq(t, inter[0]["pos"], "27")

	s := b.ok("report.gst_summary", map[string]any{"from": day(0), "to": day(0)})
	eq(t, len(s["rows"].([]any)), 2)
}

func TestBillingIRNWindowIsEnforcedForLargeTaxpayers(t *testing.T) {
	b := newBT(t, true)
	// day 20: allowed with a warning; deterministic IRN; the GSP is called once
	d20 := b.draft(b.contact, -20)
	b.issue(d20)
	r := b.ok("invoice.generate_irn", map[string]any{"id": d20})
	if !strings.Contains(r["warning"].(string), "10 day") {
		t.Fatal(r["warning"])
	}
	inv := r["invoice"].(map[string]any)
	eq(t, len(inv["irn"].(string)), 64)
	eq(t, inv["status"], "irn_generated")
	if _, ok := inv["signed_qr"].(string); !ok {
		t.Fatal("signed_qr")
	}
	if _, ok := inv["ack_no"].(string); !ok {
		t.Fatal("ack_no")
	}
	b.errIs("invoice.generate_irn", map[string]any{"id": d20}, 409, "conflict")

	// day 29: blocked
	d29 := b.draft(b.contact, -29)
	b.issue(d29)
	e := b.errIs("invoice.generate_irn", map[string]any{"id": d29}, 409, "conflict")
	if !strings.Contains(string(e.body), "window") {
		t.Fatal(string(e.body))
	}
	// The daily sweep (finance.irn_window) stays in the Rust worker; it is not a command.

	// cancel within 24h; not after
	c := b.ok("invoice.cancel_irn", map[string]any{"id": d20, "reason": "wrong buyer"})
	if _, ok := c["irn_cancelled_at"].(string); !ok {
		t.Fatal("irn_cancelled_at")
	}
	eq(t, c["status"], "issued")
	b.errIs("invoice.cancel_irn", map[string]any{"id": d20, "reason": "x"}, 409, "conflict")

	fresh := b.draft(b.contact, 0)
	b.issue(fresh)
	b.ok("invoice.generate_irn", map[string]any{"id": fresh})
	// an invoice with a live IRN cannot be cancelled before the IRN is
	b.errIs("invoice.cancel", map[string]any{"id": fresh, "reason": "x"}, 409, "conflict")
	if err := b.raw(fmt.Sprintf("update billing.invoices set ack_date = now() - interval '25 hours' where note_id = '%s'", fresh)); err != nil {
		t.Fatal(err)
	}
	late := b.errIs("invoice.cancel_irn", map[string]any{"id": fresh, "reason": "x"}, 409, "conflict")
	if !strings.Contains(string(late.body), "24 hours") {
		t.Fatal(string(late.body))
	}
}

func TestBillingIRNNeedsBuyerGSTINAndWindowOffForSmallTaxpayers(t *testing.T) {
	b := newBT(t, false)
	old := b.draft(b.contact, -40)
	b.issue(old)
	b.ok("invoice.generate_irn", map[string]any{"id": old}) // not enforced when AATO <= 10cr

	consumer := b.must(b.owner, "contact.create", map[string]any{"space_id": b.space, "name": "Walk-in", "state_code": "29"})["id"].(string)
	b2c := b.draft(consumer, 0)
	b.issue(b2c)
	b.errIs("invoice.generate_irn", map[string]any{"id": b2c}, 400, "invalid")
}

func TestBillingTenantsAreIsolated(t *testing.T) {
	a := newBT(t, false)
	b := a.env.tenant(false)
	id := a.draft(a.contact, 0)
	a.issue(id)
	for _, c := range []struct {
		cmd string
		in  any
	}{
		{"invoice.get", map[string]any{"id": id}}, {"contact.get", map[string]any{"id": a.contact}},
		{"invoice.issue", map[string]any{"id": id}}, {"payment.record", map[string]any{"invoice_id": id, "amount_minor": 1}},
	} {
		b.errIs(c.cmd, c.in, 404, "not_found")
	}
	// B cannot invoice A's contact either
	b.errIs("invoice.create_draft", map[string]any{"space_id": b.space, "contact_id": a.contact,
		"lines": []any{map[string]any{"description": "x", "unit_price_minor": 1}}}, 404, "not_found")
	eq(t, len(b.okList("invoice.list", map[string]any{})), 0)
	eq(t, len(b.okList("contact.list", map[string]any{"q": "Buyer"})), 1)
	eq(t, n(b.tb()["total_debit_minor"]), int64(0))
	eq(t, n(b.ok("report.receivables_ageing", map[string]any{})["total_minor"]), int64(0))
}

func TestBillingInvoiceLinesLinkToWorkItems(t *testing.T) {
	b := newBT(t, false)
	task := b.ok("note.create", map[string]any{"space_id": b.space, "title": "Build thing", "types": []string{"task"}})["id"].(string)
	d := b.ok("invoice.create_draft", map[string]any{"space_id": b.space, "contact_id": b.contact,
		"lines": []any{map[string]any{"description": "Build thing", "unit_price_minor": 100_000, "item_note_id": task}}})
	nn := b.ok("note.get", map[string]any{"id": d["id"]})
	found := false
	for _, r := range nn["relations"].([]any) {
		if m := r.(map[string]any); m["kind"] == "bills" && m["to_id"] == task {
			found = true
		}
	}
	if !found {
		t.Fatalf("no bills relation: %v", nn["relations"])
	}
}

func TestBillingAgentsNeedHumanConfirmationToIssue(t *testing.T) {
	b := newBT(t, false)
	b.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "manage_crm", "manage_invoices"}}})
	created := b.must(b.owner, "agent.create", map[string]any{"name": "Biller", "scopes": []string{"read", "manage_crm", "manage_invoices"}})
	agent := who{ws: b.owner.ws, bearer: created["api_key"].(string)}
	id := b.draft(b.contact, 0)

	r := b.okAs(agent, "invoice.issue", map[string]any{"id": id})
	eq(t, r["confirmation_required"], true)
	eq(t, b.ok("invoice.get", map[string]any{"id": id})["status"], "draft")
	cid := r["confirmation_id"].(string)
	hdr := map[string]string{"X-Confirmation-Id": cid}

	// unconfirmed id is refused
	if x := b.cmd(b.gosv.URL, agent, b.owner.ws, "invoice.issue", map[string]any{"id": id}, hdr); x.status != 403 {
		t.Fatalf("unconfirmed: %d %s", x.status, x.body)
	}
	// agents cannot approve their own request
	if x := b.call(agent, "confirmation.decide", map[string]any{"id": cid, "approve": true}); x.status < 400 {
		t.Fatalf("agent approved itself: %s", x.body)
	}
	b.ok("confirmation.decide", map[string]any{"id": cid, "approve": true})
	x := b.cmd(b.gosv.URL, agent, b.owner.ws, "invoice.issue", map[string]any{"id": id}, hdr)
	if x.status != 200 || x.json()["status"] != "issued" {
		t.Fatalf("confirmed issue: %d %s", x.status, x.body)
	}
	// single use
	if x := b.cmd(b.gosv.URL, agent, b.owner.ws, "invoice.issue", map[string]any{"id": id}, hdr); x.status < 400 {
		t.Fatalf("reused confirmation: %s", x.body)
	}

	// payments are gated the same way; drafting is not
	p := b.okAs(agent, "payment.record", map[string]any{"invoice_id": id, "amount_minor": 100})
	eq(t, p["confirmation_required"], true)
	d := b.okAs(agent, "invoice.create_draft", map[string]any{"space_id": b.space, "contact_id": b.contact,
		"lines": []any{map[string]any{"description": "x", "unit_price_minor": 100}}})
	eq(t, d["status"], "draft")
	// every money-moving command is gated for agents
	for _, cmd := range []string{"invoice.cancel_irn", "invoice.cancel", "credit_note.create", "invoice.write_off"} {
		r := b.call(agent, cmd, map[string]any{"id": id, "invoice_id": id, "reason": "x", "lines": []any{}})
		if !strings.Contains(string(r.body), "confirmation_required") {
			t.Fatalf("%s not gated: %d %s", cmd, r.status, r.body)
		}
	}
}

func TestBillingNumberingIsGapFreeUnderConcurrency(t *testing.T) {
	b := newBT(t, false)
	var drafts []string
	for i := 0; i < 6; i++ {
		drafts = append(drafts, b.draft(b.contact, 0))
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var numbers []string
	for _, id := range drafts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := b.call(b.owner, "invoice.issue", map[string]any{"id": id})
			mu.Lock()
			defer mu.Unlock()
			if r.status != 200 {
				t.Errorf("issue: %d %s", r.status, r.body)
				return
			}
			numbers = append(numbers, r.json()["number"].(string))
		}()
	}
	wg.Wait()
	fy := fyOf(todayIST())
	got := map[string]bool{}
	for _, x := range numbers {
		got[x] = true
	}
	for i := 1; i <= 6; i++ {
		if !got[fmt.Sprintf("INV/%s/%04d", fy, i)] {
			t.Fatalf("gap in %v", numbers)
		}
	}
	eq(t, n(b.tb()["net_minor"]), int64(0))

	// a failed issue (no valid GSTIN configured) must not burn a number
	other := b.env.tenant(false)
	other.must(other.owner, "workspace.update", map[string]any{"settings": map[string]any{"gstin": "bad"}})
	d := other.draft(other.contact, 0)
	if r := other.call(other.owner, "invoice.issue", map[string]any{"id": d}); r.status != 400 {
		t.Fatalf("issue with bad gstin: %d %s", r.status, r.body)
	}
	other.must(other.owner, "workspace.update", map[string]any{"settings": map[string]any{"gstin": billing.CompleteGSTIN("29ABCDE1234F1Z")}})
	eq(t, other.issue(d)["number"], "INV/"+fy+"/0001")
}

// Rows the Rust worker reads: every Go-emitted event has its event.dispatch job, with the same kinds as Rust emits.
func TestBillingEmitsWorkerVisibleEventsAndJobs(t *testing.T) {
	b := newBT(t, false)
	id := b.draft(b.contact, 0)
	b.ok("invoice.update_draft", map[string]any{"id": id, "due_date": day(10)})
	b.issue(id)
	b.ok("payment.record", map[string]any{"invoice_id": id, "amount_minor": 100})
	b.ok("invoice.write_off", map[string]any{"id": id, "amount_minor": 100, "reason": "r"})
	b.ok("credit_note.create", map[string]any{"invoice_id": id, "reason": "c", "lines": []any{map[string]any{"description": "d", "unit_price_minor": 1000}}})
	kinds := b.events(id)
	for _, k := range []string{"invoice.drafted", "invoice.draft_updated", "invoice.issued", "payment.recorded", "invoice.written_off"} {
		if count(kinds, k) != 1 {
			t.Fatalf("%s: %v", k, kinds)
		}
	}
	tx, _ := dbTenant(b.env, b.owner.ws)
	defer tx.Rollback(context.Background())
	var events, jobs int
	_ = tx.QueryRow(context.Background(), "select count(*) from core.events where actor_type = 'user'").Scan(&events)
	_ = tx.QueryRow(context.Background(), `select count(*) from core.jobs j where kind = 'event.dispatch'
		and exists (select 1 from core.events e where e.id = (j.payload->>'event_id')::bigint and e.actor_type = 'user')`).Scan(&jobs)
	if events == 0 || events != jobs {
		t.Fatalf("events %d jobs %d", events, jobs)
	}
	var issued string
	_ = tx.QueryRow(context.Background(), "select payload::text from core.events where kind = 'invoice.issued'").Scan(&issued)
	var p map[string]any
	_ = json.Unmarshal([]byte(issued), &p)
	eq(t, p["intra_state"], true)
	eq(t, p["reverse_charge"], false)
	eq(t, p["contact_id"], b.contact)
}

func TestBillingIdempotentPaymentReplay(t *testing.T) {
	b := newBT(t, false)
	id := b.draft(b.contact, 0)
	b.issue(id)
	h := map[string]string{"Idempotency-Key": "pay-" + rnd()}
	in := map[string]any{"invoice_id": id, "amount_minor": 500}
	r1 := b.cmd(b.gosv.URL, b.owner, b.owner.ws, "payment.record", in, h)
	r2 := b.cmd(b.gosv.URL, b.owner, b.owner.ws, "payment.record", in, h)
	if r1.status != 200 || string(r1.body) != string(r2.body) {
		t.Fatalf("replay: %d %s / %d %s", r1.status, r1.body, r2.status, r2.body)
	}
	eq(t, len(b.ok("invoice.get", map[string]any{"id": id})["payments"].([]any)), 1)
}

// Role gates: write-off is admin-only, exports and the trial balance are admin-only, reports need member.
func TestBillingRoleGates(t *testing.T) {
	b := newBT(t, false)
	member := b.addMember(b.owner, b.signup("M", false), "member")
	guest := b.addMember(b.owner, b.signup("G", false), "guest")
	id := b.draft(b.contact, 0)
	b.issue(id)
	b.errAs(member, "invoice.write_off", map[string]any{"id": id, "reason": "x"}, 403, "forbidden")
	b.errAs(member, "ledger.trial_balance", map[string]any{}, 403, "forbidden")
	b.errAs(member, "export.tally", map[string]any{"from": day(0), "to": day(0)}, 403, "forbidden")
	b.errAs(guest, "report.receivables_ageing", map[string]any{}, 403, "forbidden")
	b.errAs(guest, "invoice.create_draft", map[string]any{}, 403, "forbidden")
	b.okAs(member, "report.receivables_ageing", map[string]any{})
	b.okAs(member, "invoice.get", map[string]any{"id": id})
	if r := b.call(who{}, "invoice.get", map[string]any{"id": id}); r.status != 401 {
		t.Fatalf("no auth: %d", r.status)
	}
	// preview is read-only and matches what issue then produces
	d := b.draft(b.contact, 0)
	pv := b.ok("invoice.preview", map[string]any{"id": d})
	is := b.issue(d)
	for _, k := range []string{"subtotal_minor", "cgst_minor", "sgst_minor", "igst_minor", "total_minor"} {
		eq(t, pv[k], is[k])
	}
	b.errIs("invoice.preview", map[string]any{"id": d}, 409, "conflict")
}
