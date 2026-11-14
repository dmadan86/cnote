package server_test

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/cnote/api/internal/modules/billing"
)

// WP2: share links. Public requests carry no cookie and no bearer; everything staff does goes through share.*.

type pl struct {
	*bt
	quoteID string // a sent quotation
	invID   string // an issued invoice
}

func newPL(t *testing.T) *pl {
	b := newBT(t, false)
	b.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{
		"upi_vpa": "acme.shop@okbank", "upi_payee_name": "Acme Traders", "bank_details": "HDFC 0001 IFSC HDFC0000001"}})
	q := b.quote(b.contact, "")
	b.ok("quote.update_draft", map[string]any{"id": q, "notes": "INTERNAL-MARGIN-NOTE"})
	b.q("quote.send", q)
	inv := b.draft(b.contact, 0)
	b.issue(inv)
	return &pl{bt: b, quoteID: q, invID: inv}
}

func (p *pl) share(note string, extra map[string]any) map[string]any {
	in := map[string]any{"note_id": note, "origin": "https://app.example.test"}
	for k, v := range extra {
		in[k] = v
	}
	return p.ok("share.create", in)
}

func (p *pl) pub(method, token, sub string, body any) resp {
	p.t.Helper()
	return p.do(p.gosv.URL, method, "/api/public/"+token+sub, who{}, body, nil)
}

func (p *pl) get(token string) resp { return p.pub("GET", token, "", nil) }

func (p *pl) decide(token, decision, name string) resp {
	return p.pub("POST", token, "/quote", map[string]any{"decision": decision, "name": name, "note": "ok by me"})
}

func linkEvents(b *bt, note string) []string {
	return b.dbStrings(b.owner.ws, "select kind from core.events where note_id = $1::uuid and kind like 'share.%' order by id", note)
}

func TestShareTokenIsRandomHashedAndNeverStored(t *testing.T) {
	p := newPL(t)
	a, b := p.share(p.quoteID, nil), p.share(p.quoteID, nil)
	ta, tb := a["token"].(string), b["token"].(string)
	if len(ta) != 43 || ta == tb {
		t.Fatalf("tokens %q %q", ta, tb)
	}
	eq(t, a["url"], "https://app.example.test/p/"+ta)
	h := sha256.Sum256([]byte(ta))
	eq(t, p.dbStrings(p.owner.ws, "select token_hash from core.public_links where id = $1::uuid", a["id"]), []string{hex.EncodeToString(h[:])})
	// the token appears in no column, event or idempotency row
	for _, tbl := range []string{"core.public_links", "core.events", "core.idempotency", "core.confirmations", "core.usage"} {
		eq(t, p.dbStrings(p.owner.ws, fmt.Sprintf("select count(*)::text from %s x where x::text like '%%' || $1 || '%%'", tbl), ta), []string{"0"})
	}
	// share.list never reveals a token either
	if l := p.okList("share.list", map[string]any{"note_id": p.quoteID}); len(l) != 2 || strings.Contains(fmt.Sprint(l), ta) {
		t.Fatalf("list %v", l)
	}
	// a replayed create with the same Idempotency-Key is not replayed (secret): two requests, two links
	r1 := p.cmd(p.gosv.URL, p.owner, p.owner.ws, "share.create", map[string]any{"note_id": p.invID}, map[string]string{"Idempotency-Key": "k1"})
	r2 := p.cmd(p.gosv.URL, p.owner, p.owner.ws, "share.create", map[string]any{"note_id": p.invID}, map[string]string{"Idempotency-Key": "k1"})
	if r1.status != 200 || r2.status != 200 || r1.json()["token"] == r2.json()["token"] {
		t.Fatalf("%s %s", r1.body, r2.body)
	}
}

func keys(v any, prefix string, out *[]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			*out = append(*out, prefix+k)
			keys(c, prefix+k+".", out)
		}
	case []any:
		for _, c := range x {
			keys(c, prefix+"[].", out)
		}
	}
}

func uniqSorted(s []string) []string {
	m := map[string]bool{}
	for _, x := range s {
		m[x] = true
	}
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Golden: the public JSON is exactly the printed-on-the-document allowlist.
func TestPublicDocumentIsAnAllowlist(t *testing.T) {
	p := newPL(t)
	lineKeys := []string{"lines", "lines.[].cgst_minor", "lines.[].description", "lines.[].gst_rate_bp", "lines.[].hsn_sac", "lines.[].igst_minor",
		"lines.[].quantity", "lines.[].sgst_minor", "lines.[].taxable_minor", "lines.[].unit_price_minor"}
	common := []string{"buyer", "buyer.gstin", "buyer.name", "cgst_minor", "currency", "date", "igst_minor", "kind", "number", "place_of_supply",
		"reverse_charge", "seller", "seller.gstin", "seller.name", "sgst_minor", "status", "subtotal_minor", "total_minor"}
	wantQuote := uniqSorted(append(append(append([]string{}, common...), lineKeys...), "can_decide", "valid_until"))
	wantInvoice := uniqSorted(append(append(append([]string{}, common...), lineKeys...), "amount_due_minor", "due_date", "paid_minor",
		"payment", "payment.bank_details", "payment.upi_payee", "payment.upi_uri"))
	for note, want := range map[string][]string{p.quoteID: wantQuote, p.invID: wantInvoice} {
		tok := p.share(note, nil)["token"].(string)
		r := p.get(tok)
		if r.status != 200 {
			t.Fatalf("%d %s", r.status, r.body)
		}
		var got []string
		keys(r.json(), "", &got)
		eq(t, uniqSorted(got), want)
		for _, leak := range []string{"INTERNAL-MARGIN-NOTE", p.owner.ws, p.space, p.contact, p.quoteID, p.invID, "workspace_id", "note_id", "created_by"} {
			if strings.Contains(string(r.body), leak) {
				t.Fatalf("public JSON leaks %q: %s", leak, r.body)
			}
		}
		for _, h := range []string{"Cache-Control", "X-Robots-Tag", "Referrer-Policy"} {
			res, _ := http.Get(p.gosv.URL + "/api/public/" + tok)
			if res.Header.Get(h) == "" {
				t.Fatalf("missing header %s", h)
			}
			res.Body.Close()
		}
	}
	// the UPI URI carries the amount still due, the invoice number and INR
	d := p.get(p.share(p.invID, nil)["token"].(string)).json()
	u, err := url.Parse(d["payment"].(map[string]any)["upi_uri"].(string))
	if err != nil || u.Scheme != "upi" || u.Host != "pay" {
		t.Fatalf("%v %v", u, err)
	}
	q := u.Query()
	eq(t, q.Get("pa"), "acme.shop@okbank")
	eq(t, q.Get("cu"), "INR")
	eq(t, q.Get("tn"), d["number"])
	eq(t, q.Get("am"), fmt.Sprintf("%d.%02d", n(d["amount_due_minor"])/100, n(d["amount_due_minor"])%100))
	// after a part payment the QR asks for what is still owed, and the staff command agrees
	p.ok("payment.record", map[string]any{"invoice_id": p.invID, "amount_minor": 1_000, "method": "upi"})
	d = p.get(p.share(p.invID, nil)["token"].(string)).json()
	q = mustURL(t, d["payment"].(map[string]any)["upi_uri"].(string)).Query()
	eq(t, q.Get("am"), fmt.Sprintf("%d.%02d", n(d["amount_due_minor"])/100, n(d["amount_due_minor"])%100))
	eq(t, p.ok("invoice.upi_qr", map[string]any{"id": p.invID})["upi_uri"], d["payment"].(map[string]any)["upi_uri"])
}

func mustURL(t *testing.T, s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestUPIQRRules(t *testing.T) {
	b := newBT(t, false)
	inv := b.draft(b.contact, 0)
	b.errIs("invoice.upi_qr", map[string]any{"id": inv}, 409, "conflict") // draft
	b.issue(inv)
	b.errIs("invoice.upi_qr", map[string]any{"id": inv}, 400, "invalid") // no VPA configured
	b.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"upi_vpa": "not a vpa", "upi_payee_name": "Acme"}})
	b.errIs("invoice.upi_qr", map[string]any{"id": inv}, 400, "invalid")
	b.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"upi_vpa": "acme@okbank", "upi_payee_name": "Acme & Sons"}})
	u := mustURL(t, b.ok("invoice.upi_qr", map[string]any{"id": inv})["upi_uri"].(string))
	eq(t, u.Query().Get("pn"), "Acme & Sons")
	if strings.Contains(u.String(), "+") || strings.Contains(u.RawQuery, " ") {
		t.Fatalf("bad encoding %s", u)
	}
	b.errIs("invoice.upi_qr", map[string]any{"id": "00000000-0000-4000-8000-000000000000"}, 404, "not_found")
	eq(t, b.call(who{}, "invoice.upi_qr", map[string]any{"id": inv}).status, 401)
	o := b.env.tenant(false)
	o.errIs("invoice.upi_qr", map[string]any{"id": inv}, 404, "not_found") // another workspace
}

func TestPublicLinkUnknownRevokedExpiredAreIdentical(t *testing.T) {
	t.Setenv("PUBLIC_FAIL_BURST", "1000") // this test makes many deliberate misses
	p := newPL(t)
	live := p.share(p.quoteID, nil)
	revoked := p.share(p.quoteID, nil)
	expired := p.share(p.quoteID, nil)
	if p.get(live["token"].(string)).status != 200 {
		t.Fatal("live link")
	}
	r := p.ok("share.revoke", map[string]any{"id": revoked["id"]})
	eq(t, r["status"], "revoked")
	eq(t, p.ok("share.revoke", map[string]any{"id": revoked["id"]})["status"], "revoked") // idempotent
	eq(t, count(linkEvents(p.bt, p.quoteID), "share.revoked"), 1)
	if err := p.raw(fmt.Sprintf("update core.public_links set expires_at = now() - interval '1 minute' where id = '%s'", expired["id"])); err != nil {
		t.Fatal(err)
	}
	eq(t, p.okList("share.list", map[string]any{"note_id": p.quoteID})[0].(map[string]any)["status"] != nil, true)
	var statuses []string
	for _, l := range p.okList("share.list", map[string]any{"note_id": p.quoteID}) {
		statuses = append(statuses, l.(map[string]any)["status"].(string))
	}
	sort.Strings(statuses)
	eq(t, statuses, []string{"active", "expired", "revoked"})

	unknown := p.get(strings.Repeat("A", 43))
	for name, tok := range map[string]string{"revoked": revoked["token"].(string), "expired": expired["token"].(string),
		"short": "abc", "long": strings.Repeat("A", 200), "chars": strings.Repeat("!", 43), "dotdot": ".."} {
		g := p.get(tok)
		if g.status != 404 || string(g.body) != string(unknown.body) {
			t.Fatalf("%s: %d %s vs %d %s", name, g.status, g.body, unknown.status, unknown.body)
		}
		d := p.decide(tok, "accept", "Mallory")
		if d.status != 404 || string(d.body) != string(unknown.body) {
			t.Fatalf("%s decide: %d %s", name, d.status, d.body)
		}
	}
	eq(t, string(unknown.body), `{"error":{"code":"not_found","message":"link not found"}}`)
	eq(t, p.quoteStatus(), "sent") // nothing above changed the quotation
	// only GET / POST .../quote exist
	tok := live["token"].(string)
	for _, c := range []struct{ m, sub string }{{"POST", ""}, {"DELETE", ""}, {"GET", "/quote"}, {"POST", "/other"}, {"PUT", ""}} {
		if r := p.pub(c.m, tok, c.sub, map[string]any{}); r.status != 404 {
			t.Fatalf("%s %s -> %d", c.m, c.sub, r.status)
		}
	}
}

func (p *pl) quoteStatus() string {
	return p.ok("quote.get", map[string]any{"id": p.quoteID})["status"].(string)
}

func TestCustomerAcceptsQuoteOnceViaLink(t *testing.T) {
	p := newPL(t)
	tok := p.share(p.quoteID, nil)["token"].(string)
	eq(t, p.get(tok).json()["can_decide"], true)
	for _, bad := range []map[string]any{{"decision": "maybe", "name": "A"}, {"decision": "accept", "name": ""}, {"decision": "accept", "name": "  "},
		{"decision": "accept", "name": strings.Repeat("x", 121)}, {"decision": "accept", "name": "A", "note": strings.Repeat("n", 1001)}, {}} {
		if r := p.pub("POST", tok, "/quote", bad); r.status != 400 {
			t.Fatalf("%v -> %d %s", bad, r.status, r.body)
		}
	}
	eq(t, p.quoteStatus(), "sent")
	r := p.decide(tok, "accept", "Ravi Kumar")
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	dec := r.json()["decision"].(map[string]any)
	eq(t, dec["status"], "accepted")
	eq(t, dec["by_name"], "Ravi Kumar")
	eq(t, r.json()["can_decide"], false)
	// replaying the same decision is a no-op that answers the same; the opposite decision conflicts
	if r2 := p.decide(tok, "accept", "Someone Else"); r2.status != 200 || r2.json()["decision"].(map[string]any)["by_name"] != "Ravi Kumar" {
		t.Fatalf("replay %d %s", r2.status, r2.body)
	}
	if r3 := p.decide(tok, "decline", "Ravi Kumar"); r3.status != 409 {
		t.Fatalf("decline after accept: %d %s", r3.status, r3.body)
	}
	q := p.ok("quote.get", map[string]any{"id": p.quoteID})
	eq(t, q["status"], "accepted")
	eq(t, q["accepted_via"], "link")
	eq(t, q["accepted_by_name"], "Ravi Kumar")
	eq(t, q["accepted_note"], "ok by me")
	k := p.events(p.quoteID)
	eq(t, count(k, "quote.accepted"), 1)
	if got := p.dbStrings(p.owner.ws, "select payload->>'via' from core.events where note_id=$1::uuid and kind='quote.accepted'", p.quoteID); !reflect.DeepEqual(got, []string{"link"}) {
		t.Fatalf("event via %v", got)
	}
	// the link still shows the final state (a customer reopening it) and the owner can convert as usual
	eq(t, p.get(tok).json()["status"], "accepted")
	eq(t, p.q("quote.convert_to_invoice", p.quoteID)["quotation"].(map[string]any)["status"], "converted")
}

func TestCustomerDeclinesAndExpiredQuoteCannotBeAccepted(t *testing.T) {
	p := newPL(t)
	tok := p.share(p.quoteID, nil)["token"].(string)
	if r := p.decide(tok, "decline", "Asha"); r.status != 200 || r.json()["decision"].(map[string]any)["status"] != "rejected" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	eq(t, count(p.events(p.quoteID), "quote.rejected"), 1)
	if r := p.decide(tok, "accept", "Asha"); r.status != 409 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	e := p.quote(p.contact, day(-1))
	p.q("quote.send", e)
	et := p.share(e, nil)["token"].(string)
	d := p.get(et).json()
	eq(t, d["status"], "expired")
	eq(t, d["can_decide"], false)
	if r := p.decide(et, "accept", "Late"); r.status != 409 {
		t.Fatalf("expired accept %d %s", r.status, r.body)
	}
	// a staff accept stays "staff"
	s := p.quote(p.contact, "")
	p.q("quote.send", s)
	p.q("quote.accept", s)
	eq(t, p.ok("quote.get", map[string]any{"id": s})["accepted_via"], "staff")
	// concurrent taps: one accept event
	c := p.quote(p.contact, "")
	p.q("quote.send", c)
	ct := p.share(c, nil)["token"].(string)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); p.decide(ct, "accept", "Racer") }()
	}
	wg.Wait()
	eq(t, count(p.events(c), "quote.accepted"), 1)
}

func TestInvoiceLinkCannotAcceptAndDraftsAreNotShareable(t *testing.T) {
	p := newPL(t)
	tok := p.share(p.invID, nil)["token"].(string)
	eq(t, p.decide(tok, "accept", "x").status, 404)
	eq(t, p.get(tok).json()["kind"], "invoice")
	draftQ := p.quote(p.contact, "")
	p.errIs("share.create", map[string]any{"note_id": draftQ}, 409, "conflict")
	p.errIs("share.create", map[string]any{"note_id": p.draft(p.contact, 0)}, 409, "conflict")
	p.errIs("share.create", map[string]any{"note_id": p.contact}, 400, "invalid") // not a document
	p.errIs("share.create", map[string]any{"note_id": p.quoteID, "expires_in_days": 0}, 400, "invalid")
	p.errIs("share.create", map[string]any{"note_id": p.quoteID, "expires_in_days": 366}, 400, "invalid")
	p.errIs("share.create", map[string]any{"note_id": p.quoteID, "origin": "javascript:alert(1)"}, 400, "invalid")
	p.errIs("share.create", map[string]any{"note_id": p.quoteID, "origin": "https://x.test/path"}, 400, "invalid")
	p.errIs("share.create", map[string]any{"note_id": "nope"}, 400, "invalid")
	p.errIs("share.create", map[string]any{}, 400, "invalid")
	// cancelling the invoice withdraws it
	p.ok("invoice.cancel", map[string]any{"id": p.invID, "reason": "test"})
	eq(t, p.get(tok).status, 200) // still readable: the page shows it as cancelled, without payment options
	if d := p.get(tok).json(); d["status"] != "cancelled" || d["payment"] != nil {
		t.Fatalf("%v", d)
	}
}

func TestViewTrackingCountsEveryViewAndEventsOncePerDay(t *testing.T) {
	p := newPL(t)
	l := p.share(p.quoteID, nil)
	tok := l["token"].(string)
	for i := 0; i < 3; i++ {
		p.get(tok)
	}
	v := p.okList("share.list", map[string]any{"note_id": p.quoteID})[0].(map[string]any)
	eq(t, n(v["view_count"]), int64(3))
	if v["last_viewed_at"] == nil {
		t.Fatal("last_viewed_at")
	}
	eq(t, linkEvents(p.bt, p.quoteID), []string{"share.created", "share.viewed"})
	// next day: another event
	if err := p.raw("update core.public_links set last_viewed_at = now() - interval '1 day'"); err != nil {
		t.Fatal(err)
	}
	p.get(tok)
	eq(t, count(linkEvents(p.bt, p.quoteID), "share.viewed"), 2)
}

func TestShareCrossTenant(t *testing.T) {
	p := newPL(t)
	mine := p.share(p.quoteID, nil)
	other := p.env.tenant(false)
	oq := other.quote(other.contact, "")
	other.q("quote.send", oq)
	theirs := other.share2(oq)
	// each token opens only its own workspace's document
	eq(t, p.get(mine["token"].(string)).json()["number"], p.ok("quote.get", map[string]any{"id": p.quoteID})["number"])
	eq(t, other.pubGet(theirs["token"].(string)).json()["buyer"].(map[string]any)["name"], "Buyer 29")
	// the other workspace cannot share, list or revoke my documents or links
	other.errIs("share.create", map[string]any{"note_id": p.quoteID}, 404, "not_found")
	other.errIs("share.list", map[string]any{"note_id": p.quoteID}, 404, "not_found")
	other.errIs("share.revoke", map[string]any{"id": mine["id"]}, 404, "not_found")
	eq(t, p.get(mine["token"].(string)).status, 200)
	// path trickery: my workspace id with their note, their command with my ids
	if r := other.cmd(other.gosv.URL, other.owner, p.owner.ws, "share.list", map[string]any{"note_id": p.quoteID}, nil); r.status == 200 {
		t.Fatalf("foreign workspace path: %s", r.body)
	}
	// RLS: the other tenant's connection sees none of my link rows
	eq(t, other.dbStrings(other.owner.ws, "select count(*)::text from core.public_links where note_id = $1::uuid", p.quoteID), []string{"0"})
	// a link row for another workspace's note cannot even be written (composite FK)
	if err := other.raw(fmt.Sprintf("insert into core.public_links (workspace_id, note_id, token_hash, purpose, created_by) values ('%s','%s','x','view','%s')",
		other.owner.ws, p.quoteID, other.owner.userID)); err == nil {
		t.Fatal("cross-tenant link row accepted")
	}
	// member removed or demoted: the link dies
	m := p.addMember(p.owner, p.signup("Mem", false), "member")
	mt := p.okAs(m, "share.create", map[string]any{"note_id": p.quoteID})["token"].(string)
	eq(t, p.get(mt).status, 200)
	if err := p.raw(fmt.Sprintf("update core.memberships set role = 'guest' where user_id = '%s'", m.userID)); err != nil {
		t.Fatal(err)
	}
	eq(t, p.get(mt).status, 404)
	eq(t, p.decide(mt, "accept", "x").status, 404)
	eq(t, p.quoteStatus(), "sent")
}

func (b *bt) share2(note string) map[string]any {
	return b.ok("share.create", map[string]any{"note_id": note})
}
func (b *bt) pubGet(tok string) resp {
	return b.do(b.gosv.URL, "GET", "/api/public/"+tok, who{}, nil, nil)
}

func TestShareRolesAgentsAndAPIKeys(t *testing.T) {
	p := newPL(t)
	member := p.addMember(p.owner, p.signup("M", false), "member")
	guest := p.addMember(p.owner, p.signup("G", false), "guest")
	in := map[string]any{"note_id": p.quoteID}
	p.errAs(guest, "share.create", in, 403, "forbidden")
	p.errAs(guest, "share.email", map[string]any{"note_id": p.quoteID, "to": "a@b.test"}, 403, "forbidden")
	p.errAs(guest, "share.revoke", map[string]any{"id": "00000000-0000-0000-0000-000000000000"}, 403, "forbidden")
	l := p.okAs(member, "share.create", in)
	p.errAs(guest, "share.list", in, 404, "not_found") // a guest without access to the page does not see its links
	p.okAs(member, "share.revoke", map[string]any{"id": l["id"]})
	if r := p.call(who{}, "share.create", in); r.status != 401 {
		t.Fatalf("no auth %d", r.status)
	}

	// agents: send_messages is off by default; once enabled the call needs a human confirmation
	created := p.must(p.owner, "agent.create", map[string]any{"name": "Sender", "scopes": []string{"read", "send_messages", "write_notes"}})
	agent := who{ws: p.owner.ws, bearer: created["api_key"].(string)}
	p.errAs(agent, "share.create", in, 403, "forbidden")
	p.errAs(agent, "share.email", map[string]any{"note_id": p.quoteID, "to": "a@b.test"}, 403, "forbidden")
	p.must(p.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "write_notes", "send_messages"}}})
	before := p.dbStrings(p.owner.ws, "select count(*)::text from core.public_links")
	r := p.okAs(agent, "share.create", in)
	eq(t, r["confirmation_required"], true)
	r = p.okAs(agent, "share.email", map[string]any{"note_id": p.quoteID, "to": "a@b.test"})
	eq(t, r["confirmation_required"], true)
	eq(t, p.dbStrings(p.owner.ws, "select count(*)::text from core.public_links"), before) // nothing was created or sent
	eq(t, p.dbStrings(p.owner.ws, "select count(*)::text from core.confirmations where command in ('share.create','share.email') and status = 'pending'"), []string{"2"})
	// revoking is safe for agents (no confirmation) and allowed with write_notes
	eq(t, p.okAs(agent, "share.revoke", map[string]any{"id": l["id"]})["status"], "revoked")
	// a plain API key is also a machine caller: confirmation required
	key := p.apiKey(p.owner)
	if r := p.okAs(key, "share.create", in); r["confirmation_required"] != true {
		t.Fatalf("api key: %v", r)
	}
}

func TestShareConfirmedCreateWorksForAgentOnBehalfOfOwner(t *testing.T) {
	p := newPL(t)
	p.must(p.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "send_messages"}}})
	key := p.apiKey(p.owner)
	in := map[string]any{"note_id": p.quoteID}
	r := p.okAs(key, "share.create", in)
	id := r["confirmation_id"].(string)
	p.must(p.owner, "confirmation.decide", map[string]any{"id": id, "approve": true})
	res := p.cmd(p.gosv.URL, key, p.owner.ws, "share.create", in, map[string]string{"X-Confirmation-Id": id})
	if res.status != 200 || len(res.json()["token"].(string)) != 43 {
		t.Fatalf("%d %s", res.status, res.body)
	}
	if again := p.cmd(p.gosv.URL, key, p.owner.ws, "share.create", in, map[string]string{"X-Confirmation-Id": id}); again.status != 403 {
		t.Fatalf("confirmation reused: %d %s", again.status, again.body)
	}
}

func TestWhatsAppLinkAndMessage(t *testing.T) {
	p := newPL(t)
	c := p.must(p.owner, "contact.create", map[string]any{"space_id": p.space, "name": "Meera & Co", "kind": "company", "phone": "098765 43210",
		"gstin": billing.CompleteGSTIN("29AABCT1332L1Z")})
	q := p.ok("quote.create_draft", map[string]any{"space_id": p.space, "contact_id": c["id"], "lines": []any{map[string]any{"description": "x", "unit_price_minor": 100_00}}})["id"].(string)
	p.q("quote.send", q)
	l := p.share(q, nil)
	wa := mustURL(t, l["whatsapp_url"].(string))
	eq(t, wa.Host, "wa.me")
	eq(t, wa.Path, "/919876543210")
	text := wa.Query().Get("text")
	for _, w := range []string{"Meera & Co", "quotation", l["url"].(string), "Rs 118.00"} {
		if !strings.Contains(text, w) {
			t.Fatalf("%q missing in %q", w, text)
		}
	}
	if strings.Contains(wa.RawQuery, "+") {
		t.Fatalf("plus-encoded spaces: %s", wa.RawQuery)
	}
	// an explicit phone wins; no phone anywhere leaves the chat open
	eq(t, mustURL(t, p.share(q, map[string]any{"phone": "+44 7700 900123"})["whatsapp_url"].(string)).Path, "/447700900123")
	eq(t, mustURL(t, p.share(p.quoteID, nil)["whatsapp_url"].(string)).Path, "/") // contact "Buyer 29" has no phone
	// the workspace's own template
	p.must(p.owner, "workspace.update", map[string]any{"settings": map[string]any{"share_message": "Namaste {name}: {doc} {number} {link}"}})
	m := p.share(q, nil)["message"].(string)
	if !strings.HasPrefix(m, "Namaste Meera & Co: quotation QT/") {
		t.Fatalf("%q", m)
	}
}

func TestPublicRateLimits(t *testing.T) {
	t.Setenv("PUBLIC_FAIL_BURST", "3")
	t.Setenv("PUBLIC_TOKEN_BURST", "2")
	p := newPL(t)
	tok := p.share(p.quoteID, nil)["token"].(string)
	eq(t, p.get(tok).status, 200)
	eq(t, p.get(tok).status, 200)
	r := p.get(tok) // per-token budget (2) is spent
	eq(t, r.status, 429)
	eq(t, r.json()["error"].(map[string]any)["code"], "rate_limited")
	// guessing: three misses, then the address is locked out, even for a valid link
	for i := 0; i < 3; i++ {
		eq(t, p.get(fmt.Sprintf("%043d", i)).status, 404)
	}
	other := p.share(p.invID, nil)["token"].(string)
	eq(t, p.get(other).status, 429)
	eq(t, p.decide(other, "accept", "x").status, 429)
}

// A minimal SMTP sink: records what a real mail provider would receive.
type smtpSink struct {
	ln   net.Listener
	mu   sync.Mutex
	msgs []string
}

func startSMTP(t *testing.T) *smtpSink {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &smtpSink{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *smtpSink) serve(c net.Conn) {
	defer c.Close()
	r, w := bufio.NewReader(c), func(l string) { fmt.Fprintf(c, "%s\r\n", l) }
	w("220 sink ready")
	var data strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			w("250 sink")
		case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
			w("250 ok")
		case cmd == "DATA":
			w("354 go")
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				data.WriteString(l)
			}
			s.mu.Lock()
			s.msgs = append(s.msgs, data.String())
			s.mu.Unlock()
			w("250 queued")
		case cmd == "QUIT":
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func TestShareEmail(t *testing.T) {
	p := newPL(t)
	in := map[string]any{"note_id": p.quoteID, "to": "customer@example.test", "origin": "https://app.example.test", "message": "Please review."}
	p.errIs("share.email", in, 400, "invalid") // SMTP not configured here
	eq(t, p.dbStrings(p.owner.ws, "select count(*)::text from core.public_links"), []string{"0"})
	sink := startSMTP(t)
	t.Setenv("SMTP_URL", "smtp://"+sink.ln.Addr().String())
	t.Setenv("EMAIL_FROM", "billing@acme.example.test")
	for _, bad := range []string{"", "not-an-email", "a@b.test\r\nBcc: x@y.test", "Name <a@b.test>", "a@b.test, c@d.test"} {
		bad := map[string]any{"note_id": p.quoteID, "to": bad}
		p.errIs("share.email", bad, 400, "invalid")
	}
	r := p.ok("share.email", in)
	eq(t, r["sent"], true)
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.msgs) != 1 {
		t.Fatalf("messages %d", len(sink.msgs))
	}
	m := sink.msgs[0]
	for _, w := range []string{"To: customer@example.test", "From: billing@acme.example.test", "Subject: Quotation QT/", "Please review.", "https://app.example.test/p/"} {
		if !strings.Contains(m, w) {
			t.Fatalf("%q missing in %q", w, m)
		}
	}
	// the emailed token opens the document and is not stored
	tok := m[strings.Index(m, "/p/")+3:]
	tok = tok[:43]
	eq(t, p.get(tok).status, 200)
	eq(t, p.dbStrings(p.owner.ws, "select count(*)::text from core.events x where x::text like '%' || $1 || '%'", tok), []string{"0"})
	eq(t, linkEvents(p.bt, p.quoteID), []string{"share.created", "share.emailed", "share.viewed"})
	// a failing mail server leaves no link behind
	sink.ln.Close()
	p.errIs("share.email", in, 500, "internal")
	eq(t, len(p.okList("share.list", map[string]any{"note_id": p.quoteID})), 1)
}

func TestWhatsAppReminderWording(t *testing.T) {
	p := newPL(t)
	text := mustURL(t, p.share(p.invID, map[string]any{"reminder": true})["whatsapp_url"].(string)).Query().Get("text")
	if !strings.Contains(text, "reminder") || !strings.Contains(text, "/p/") {
		t.Fatalf("%q", text)
	}
}
