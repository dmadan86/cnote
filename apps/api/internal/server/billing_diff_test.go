package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cnote/api/internal/modules/billing"
)

// Twin-fixture differential test for the billing module: one script runs against fixture A through Rust and an
// identical fixture B through Go; every response (status and body, ids and timestamps normalized by order of
// appearance) and the resulting billing, ledger, event, confirmation and idempotency rows must be equal. Then
// every read command is compared as raw bytes between both servers on each fixture.

type bx struct {
	*runner
	biller who
	ctc    map[string]string // contacts by label
	prv    string            // private space
}

func (x *bx) who(n string) who {
	if n == "biller" {
		return x.biller
	}
	return x.f.who(n)
}

var slugRe = regexp.MustCompile(`"slug":"[^"]*"`)

func (x *bx) norm(s string) string { return x.runner.norm(slugRe.ReplaceAllString(s, `"slug":"S"`)) }

func (x *bx) call(caller, cmd string, in any, hdr ...map[string]string) map[string]any {
	x.e.t.Helper()
	time.Sleep(30 * time.Millisecond)
	var h map[string]string
	if len(hdr) > 0 {
		h = hdr[0]
	}
	res := x.e.cmd(x.base, x.who(caller), x.f.owner.ws, cmd, in, h)
	body := orderless(cmd, res.status, string(res.body))
	x.transcript = append(x.transcript, fmt.Sprintf("%s %s -> %d %s", caller, cmd, res.status, x.norm(body)))
	return res.json()
}

func (x *bx) raw(caller, cmd, body string) {
	x.e.t.Helper()
	time.Sleep(30 * time.Millisecond)
	w := x.who(caller)
	w.ws = x.f.owner.ws
	res := x.e.rawJSON(x.base, w, cmd, body)
	x.transcript = append(x.transcript, fmt.Sprintf("%s %s RAW %s -> %d %s", caller, cmd, x.norm(body), res.status, x.norm(orderless(cmd, res.status, string(res.body)))))
}

func (x *bx) id(name, caller, cmd string, in any) {
	x.e.t.Helper()
	if id, ok := x.call(caller, cmd, in)["id"].(string); ok {
		x.vars[name] = id
	}
}

func (x *bx) sql(q string) {
	x.e.t.Helper()
	tx, err := dbTenant(x.e, x.f.owner.ws)
	if err != nil {
		x.e.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(context.Background(), q); err != nil {
		x.e.t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		x.e.t.Fatal(err)
	}
}

func (x *bx) setup() {
	e, f := x.e, x.f
	e.must(f.owner, "workspace.update", map[string]any{"settings": map[string]any{
		"gstin": billing.CompleteGSTIN("29ABCDE1234F1Z"), "state_code": "29", "aato_above_10cr": false,
		"agent_tool_groups": []string{"read", "write_notes", "manage_crm", "manage_invoices"}}})
	x.biller = who{ws: f.owner.ws, bearer: e.must(f.owner, "agent.create", map[string]any{"name": "biller-bot", "scopes": []string{"read", "manage_crm", "manage_invoices"}})["api_key"].(string)}
	x.prv = e.must(f.owner, "space.create", map[string]any{"key": "PRV", "name": "Private fin", "visibility": "private"})["id"].(string)
	g := func(state string) string { return billing.CompleteGSTIN(state + "AABCT1332L1Z") }
	mk := func(label, space string, in map[string]any) {
		in["space_id"] = space
		x.ctc[label] = e.must(f.owner, "contact.create", in)["id"].(string)
	}
	mk("c29", f.space, map[string]any{"name": "Buyer 29", "kind": "company", "gstin": g("29")})
	mk("c27", f.space, map[string]any{"name": "Buyer 27", "kind": "company", "gstin": g("27")})
	mk("c33", f.space, map[string]any{"name": "Ünï & Sons <Ltd> \"Q\" 'x' 🚀", "kind": "company", "gstin": g("33")})
	mk("w29", f.space, map[string]any{"name": "Walk-in 29", "state_code": "29"})
	mk("w27", f.space, map[string]any{"name": "Walk-in 27", "state_code": "27"})
	mk("wnone", f.space, map[string]any{"name": "Walk-in no state"})
	mk("prvc", x.prv, map[string]any{"name": "Private buyer", "kind": "company", "gstin": g("29")})
	x.vars["task"] = e.must(f.owner, "note.create", map[string]any{"space_id": f.space, "title": "Task to bill", "types": []string{"task"}})["id"].(string)
	x.vars["ptask"] = e.must(f.owner, "note.create", map[string]any{"space_id": x.prv, "title": "Private task", "types": []string{"task"}})["id"].(string)
	x.vars["otherc"] = e.must(f.other, "contact.create", map[string]any{"space_id": e.space(f.other), "name": "Foreign", "state_code": "29"})["id"].(string)
}

func lines(l ...map[string]any) []any {
	out := make([]any, len(l))
	for i, v := range l {
		out[i] = v
	}
	return out
}

func ln(desc string, price int, extra ...any) map[string]any {
	m := map[string]any{"description": desc, "unit_price_minor": price}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i].(string)] = extra[i+1]
	}
	return m
}

func bday(off int) string { return day(off) }

func billingScript(x *bx) {
	c := x.ctc
	sp := x.f.space
	missing := "00000000-0000-4000-8000-000000000000"
	std := func() []any {
		return lines(ln("Consulting", 40_001, "hsn_sac", "998314", "quantity", 2.5), ln("Hosting", 10_005, "hsn_sac", "998315", "gst_rate_bp", 500))
	}

	// ---- invoice.create_draft: successes
	x.id("d1", "owner", "invoice.create_draft", map[string]any{"space_id": sp, "contact_id": c["c29"], "invoice_date": bday(0), "lines": std()})
	x.id("d2", "owner", "invoice.create_draft", map[string]any{"space_id": sp, "contact_id": c["c27"], "invoice_date": bday(0), "due_date": bday(45), "notes": "Net 45 ünï 🚀 <b>&",
		"lines": lines(ln("Laptop", 100_000, "quantity", 3, "gst_rate_bp", 2800), ln("Zero rated", 5_000, "gst_rate_bp", 0, "quantity", 1.0), ln("Odd qty", 101, "quantity", 0.335, "gst_rate_bp", 1200), ln("Odd paisa", 105, "gst_rate_bp", 1800))})
	x.id("d3", "member", "invoice.create_draft", map[string]any{"space_id": sp, "contact_id": c["w29"], "invoice_date": bday(0), "lines": std()})
	x.id("d4", "biller", "invoice.create_draft", map[string]any{"space_id": sp, "contact_id": c["w27"], "invoice_date": bday(0), "lines": lines(ln("Phone", 100_000*100, "hsn_sac", "851713"), ln("Case", 99, "quantity", 7))})
	x.id("d5", "owner", "invoice.create_draft", map[string]any{"space_id": sp, "contact_id": c["w27"], "invoice_date": bday(0), "lines": lines(ln("Phone small", 1_000*100, "hsn_sac", "851713"))})
	x.id("d6", "owner", "invoice.create_draft", map[string]any{"space_id": sp, "contact_id": c["c29"], "invoice_date": bday(0), "reverse_charge": true, "place_of_supply": "27", "lines": std()})
	x.id("d7", "owner", "invoice.create_draft", map[string]any{"space_id": sp, "contact_id": c["c33"], "invoice_date": bday(-75), "lines": lines(ln("Old work", 123_457, "item_note_id", x.v("task")))})
	x.id("d8", "owner", "invoice.create_draft", map[string]any{"space_id": sp, "contact_id": c["c29"], "invoice_date": bday(-5), "lines": lines(ln("Recent", 77_777, "quantity", 12.75))})
	x.id("d9", "apikey", "invoice.create_draft", map[string]any{"space_id": sp, "contact_id": c["c29"], "lines": lines(ln("Default date", 1_00))})
	x.id("dnull", "owner", "invoice.create_draft", map[string]any{"space_id": sp, "contact_id": c["c29"], "invoice_date": nil, "due_date": nil, "place_of_supply": nil, "reverse_charge": nil, "notes": nil,
		"lines": lines(map[string]any{"description": " trimmed ", "hsn_sac": nil, "quantity": nil, "unit_price_minor": 100, "gst_rate_bp": nil, "item_note_id": nil}, ln("blank hsn", 100, "hsn_sac", "   "))})
	x.id("dnostate", "owner", "invoice.create_draft", map[string]any{"space_id": sp, "contact_id": c["wnone"], "invoice_date": bday(0), "lines": std()})
	x.id("dpos", "owner", "invoice.create_draft", map[string]any{"space_id": sp, "contact_id": c["wnone"], "place_of_supply": "29", "invoice_date": bday(0), "lines": std()})
	x.id("djul", "owner", "invoice.create_draft", map[string]any{"space_id": sp, "contact_id": c["w27"], "invoice_date": "2024-07-31", "lines": lines(ln("Jul big", 150_000*100, "gst_rate_bp", 0))})
	x.id("daug", "owner", "invoice.create_draft", map[string]any{"space_id": sp, "contact_id": c["w27"], "invoice_date": "2024-08-01", "lines": lines(ln("Aug big", 150_000*100, "gst_rate_bp", 0))})
	x.id("djul2", "owner", "invoice.create_draft", map[string]any{"space_id": sp, "contact_id": c["w27"], "invoice_date": "2024-07-15", "lines": lines(ln("Jul huge", 260_000*100, "gst_rate_bp", 0), ln("Jul 5", 100_000, "gst_rate_bp", 500))})
	x.id("dprv", "owner", "invoice.create_draft", map[string]any{"space_id": x.prv, "contact_id": c["prvc"], "invoice_date": bday(0), "lines": lines(ln("Private job", 50_000, "item_note_id", x.v("ptask")))})
	x.id("dfar", "owner", "invoice.create_draft", map[string]any{"space_id": sp, "contact_id": c["c29"], "invoice_date": "2030-02-28", "lines": lines(ln("Future", 1_000))})

	// ---- invoice.create_draft: errors
	bad := func(caller string, in map[string]any) { x.call(caller, "invoice.create_draft", in) }
	base := func(extra ...any) map[string]any {
		m := map[string]any{"space_id": sp, "contact_id": c["c29"], "invoice_date": bday(0), "lines": lines(ln("x", 100))}
		for i := 0; i+1 < len(extra); i += 2 {
			m[extra[i].(string)] = extra[i+1]
		}
		return m
	}
	bad("owner", base("lines", []any{}))
	bad("owner", base("lines", lines(ln("   ", 100))))
	bad("owner", base("lines", lines(ln("x", -1))))
	bad("owner", base("lines", lines(ln("x", 100, "quantity", 0))))
	bad("owner", base("lines", lines(ln("x", 100, "quantity", -2))))
	bad("owner", base("lines", lines(ln("x", 100, "quantity", 0.0))))
	bad("owner", base("lines", lines(ln("x", 100, "gst_rate_bp", 2801))))
	bad("owner", base("lines", lines(ln("x", 100, "hsn_sac", "12"))))
	bad("owner", base("lines", lines(ln("x", 100, "hsn_sac", " 998314"))))
	bad("owner", base("lines", lines(ln("x", 100, "hsn_sac", "99831x"))))
	bad("owner", base("lines", lines(ln("x", 9223372036854775807, "quantity", 2))))
	bad("owner", base("lines", lines(ln("x", 9223372036854775807, "quantity", 1))))
	bad("owner", base("lines", lines(ln("x", 100, "quantity", 1000000000000))))
	bad("owner", base("lines", lines(ln("x", 100, "item_note_id", missing))))
	bad("owner", base("lines", lines(ln("x", 100, "item_note_id", x.v("ptask")))))
	bad("owner", base("lines", lines(ln("first ok", 100), ln("", 100), ln("later bad rate", 1, "gst_rate_bp", 9999))))
	bad("owner", base("place_of_supply", "00"))
	bad("owner", base("place_of_supply", "29 "))
	bad("owner", base("contact_id", missing))
	bad("owner", base("contact_id", x.v("task")))
	bad("owner", base("contact_id", c["prvc"]))
	bad("owner", base("space_id", missing))
	bad("owner", base("invoice_date", "2024-02-30"))
	bad("owner", base("lines", lines(ln("x", 100, "item_note_id", x.v("task")), ln("y", 100, "quantity", 5))))
	bad("member", base("contact_id", c["prvc"], "space_id", x.prv))
	bad("member", base("space_id", x.prv))
	bad("guest", base())
	bad("agent", base())
	bad("noauth", base())
	bad("badkey", base())
	bad("other", base())
	bad("owner", base("contact_id", x.v("otherc")))
	for _, raw := range []string{
		`{}`, `[]`, `null`, `{bad`, `{"space_id":"` + sp + `"}`, `{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `"}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":null}`, `{"space_id":"x","contact_id":"` + c["c29"] + `","lines":[]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"x"}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"x","unit_price_minor":1.5}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"x","unit_price_minor":-0}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"x","unit_price_minor":100,"quantity":"2"}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"x","unit_price_minor":100,"quantity":1e2}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"x","unit_price_minor":100,"quantity":0.00001}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"x","unit_price_minor":100,"quantity":2.50}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"x","unit_price_minor":100,"quantity":-0}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"q","unit_price_minor":333,"quantity":1.0}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"q","unit_price_minor":333,"quantity":10.00}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"q","unit_price_minor":333,"quantity":0.10}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"q","unit_price_minor":333,"quantity":123456.789}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"q","unit_price_minor":333,"quantity":0.001}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"q","unit_price_minor":333,"quantity":0.0001}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"q","unit_price_minor":333,"quantity":99999999999999.9}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"q","unit_price_minor":333,"quantity":0.0}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"q","unit_price_minor":333,"quantity":-0.0}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"q","unit_price_minor":333,"quantity":-1.5}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"q","unit_price_minor":333,"quantity":7}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"q","unit_price_minor":333,"quantity":0}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"q","unit_price_minor":333,"quantity":1E2}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"q","unit_price_minor":333,"quantity":2.5e0}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"q","unit_price_minor":333,"quantity":12345678901234567890}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"q","unit_price_minor":333,"quantity":0.333333333333333}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"q","unit_price_minor":333,"quantity":0.3333333333333333}]}`,

		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"x","unit_price_minor":100,"gst_rate_bp":-1}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"x","unit_price_minor":100,"gst_rate_bp":4294967296}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"x","unit_price_minor":100,"gst_rate_bp":1800.0}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"x","unit_price_minor":9223372036854775808}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"\ud800","unit_price_minor":1}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"x","unit_price_minor":1}],"invoice_date":"2026-1-5"}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"x","unit_price_minor":1}],"reverse_charge":"yes"}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"x","unit_price_minor":1}],"notes":5}`,
		`{"space_id":"` + strings.ReplaceAll(sp, "-", "") + `","contact_id":"` + c["c29"] + `","lines":[{"description":"x","unit_price_minor":1}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"nul\u0000","unit_price_minor":1}]}`,
		`{"space_id":"` + sp + `","contact_id":"` + c["c29"] + `","lines":[{"description":"` + strings.Repeat("z", 5000) + `","unit_price_minor":1}],"notes":"` + strings.Repeat("n", 5000) + `"}`,
	} {
		x.raw("owner", "invoice.create_draft", raw)
	}

	// ---- invoice.update_draft
	x.call("owner", "invoice.update_draft", map[string]any{"id": x.v("d1"), "contact_id": c["w27"]})
	x.call("owner", "invoice.update_draft", map[string]any{"id": x.v("d1"), "contact_id": c["c29"], "place_of_supply": "07", "reverse_charge": false, "invoice_date": bday(0), "due_date": bday(20)})
	x.call("owner", "invoice.update_draft", map[string]any{"id": x.v("d1"), "contact_id": c["wnone"]}) // state None, gstin None: place keeps
	x.call("owner", "invoice.update_draft", map[string]any{"id": x.v("d1"), "contact_id": c["c29"]})
	x.call("owner", "invoice.update_draft", map[string]any{"id": x.v("d1"), "lines": lines(ln("Replaced", 20_000, "quantity", 2.25, "item_note_id", x.v("task")))})
	x.call("owner", "invoice.update_draft", map[string]any{"id": x.v("d1"), "lines": lines(ln("Replaced again", 20_001))})
	x.call("owner", "invoice.update_draft", map[string]any{"id": x.v("d1")})
	x.call("owner", "invoice.update_draft", map[string]any{"id": x.v("d1"), "contact_id": nil, "lines": nil, "invoice_date": nil, "due_date": nil, "place_of_supply": nil, "reverse_charge": nil})
	x.call("owner", "invoice.update_draft", map[string]any{"id": x.v("d3"), "reverse_charge": true})
	x.call("member", "invoice.update_draft", map[string]any{"id": x.v("d3"), "due_date": bday(9)})
	x.call("owner", "invoice.update_draft", map[string]any{"id": x.v("d1"), "lines": []any{}})
	x.call("owner", "invoice.update_draft", map[string]any{"id": x.v("d1"), "lines": lines(ln("bad", -5))})
	x.call("owner", "invoice.update_draft", map[string]any{"id": x.v("d1"), "place_of_supply": "00"})
	x.call("owner", "invoice.update_draft", map[string]any{"id": x.v("d1"), "contact_id": missing})
	x.call("owner", "invoice.update_draft", map[string]any{"id": x.v("d1"), "contact_id": c["prvc"], "lines": []any{}})
	x.call("owner", "invoice.update_draft", map[string]any{"id": missing, "due_date": bday(1)})
	x.call("owner", "invoice.update_draft", map[string]any{"id": x.v("task"), "due_date": bday(1)})
	x.call("member", "invoice.update_draft", map[string]any{"id": x.v("dprv"), "due_date": bday(1)})
	x.call("guest", "invoice.update_draft", map[string]any{"id": x.v("d3"), "due_date": bday(1)})
	x.call("agent", "invoice.update_draft", map[string]any{"id": x.v("d3"), "due_date": bday(1)})
	x.call("biller", "invoice.update_draft", map[string]any{"id": x.v("d4"), "due_date": bday(1)})
	x.call("other", "invoice.update_draft", map[string]any{"id": x.v("d3"), "due_date": bday(1)})
	x.call("noauth", "invoice.update_draft", map[string]any{"id": x.v("d3"), "due_date": bday(1)})
	for _, raw := range []string{`{}`, `{"id":"nope"}`, `{"id":"` + x.v("d3") + `","lines":5}`, `{"id":"` + x.v("d3") + `","lines":[{"unit_price_minor":1}]}`,
		`{"id":"` + x.v("d3") + `","due_date":"2026-02-30"}`, `{"id":"` + x.v("d3") + `","reverse_charge":null}`, `{"id":"` + x.v("d3") + `","contact_id":"nope"}`} {
		x.raw("owner", "invoice.update_draft", raw)
	}

	// ---- invoice.preview (drafts; errors for non drafts below)
	for _, id := range []string{"d1", "d2", "d3", "d4", "d6", "d7", "dprv", "dnostate", "dpos", "djul"} {
		for _, who := range []string{"owner", "member", "guest", "biller", "other", "noauth"} {
			x.call(who, "invoice.preview", map[string]any{"id": x.v(id)})
		}
	}
	x.call("owner", "invoice.preview", map[string]any{"id": missing})
	x.call("owner", "invoice.preview", map[string]any{"id": x.v("task")})
	x.raw("owner", "invoice.preview", `{}`)
	x.raw("owner", "invoice.preview", `{"id":"x"}`)
	// no GSTIN: preview and issue are refused, and so are bad ones
	x.call("owner", "workspace.update", map[string]any{"settings": map[string]any{"gstin": nil, "state_code": nil}})
	x.call("owner", "invoice.preview", map[string]any{"id": x.v("d3")})
	x.call("owner", "invoice.issue", map[string]any{"id": x.v("d3")})
	x.call("owner", "workspace.update", map[string]any{"settings": map[string]any{"gstin": "bad"}})
	x.call("owner", "invoice.issue", map[string]any{"id": x.v("d3")})
	x.call("owner", "workspace.update", map[string]any{"settings": map[string]any{"gstin": "27AAPFU0939F1ZX"}})
	x.call("owner", "invoice.preview", map[string]any{"id": x.v("d3")})
	x.call("owner", "workspace.update", map[string]any{"settings": map[string]any{"gstin": "27AAPFU0939F1ZV", "state_code": "00"}})
	x.call("owner", "invoice.preview", map[string]any{"id": x.v("d3")})
	x.call("owner", "workspace.update", map[string]any{"settings": map[string]any{"gstin": billing.CompleteGSTIN("29ABCDE1234F1Z"), "state_code": nil}})
	x.call("owner", "invoice.preview", map[string]any{"id": x.v("d3")}) // state derived from the GSTIN
	x.call("owner", "workspace.update", map[string]any{"settings": map[string]any{"gstin": billing.CompleteGSTIN("29ABCDE1234F1Z"), "state_code": "29"}})
	x.call("owner", "invoice.issue", map[string]any{"id": x.v("dnostate")}) // place of supply unknown
	x.call("owner", "invoice.preview", map[string]any{"id": x.v("dnostate")})

	// ---- invoice.issue
	for _, id := range []string{"d1", "d2", "d6", "d7", "d8", "dnull", "dpos", "djul", "daug", "djul2", "dfar"} {
		x.call("owner", "invoice.issue", map[string]any{"id": x.v(id)})
	}
	x.call("member", "invoice.issue", map[string]any{"id": x.v("d3")})
	x.call("biller", "invoice.issue", map[string]any{"id": x.v("d4")}) // confirmation requested
	x.call("owner", "invoice.issue", map[string]any{"id": x.v("d1")})  // twice
	x.call("owner", "invoice.issue", map[string]any{"id": missing})
	x.call("owner", "invoice.issue", map[string]any{"id": x.v("task")})
	x.call("member", "invoice.issue", map[string]any{"id": x.v("dprv")})
	x.call("guest", "invoice.issue", map[string]any{"id": x.v("d5")})
	x.call("agent", "invoice.issue", map[string]any{"id": x.v("d5")})
	x.call("other", "invoice.issue", map[string]any{"id": x.v("d5")})
	x.call("noauth", "invoice.issue", map[string]any{"id": x.v("d5")})
	x.call("badkey", "invoice.issue", map[string]any{"id": x.v("d5")})
	x.raw("owner", "invoice.issue", `{}`)
	x.raw("owner", "invoice.issue", `{"id":null}`)
	x.call("owner", "invoice.preview", map[string]any{"id": x.v("d1")}) // not a draft
	x.call("owner", "invoice.update_draft", map[string]any{"id": x.v("d1"), "due_date": bday(3)})

	// agent confirmation flow: request, unconfirmed, agent cannot decide, human approves, single use
	r1 := x.call("biller", "invoice.issue", map[string]any{"id": x.v("d4")})
	cid, _ := r1["confirmation_id"].(string)
	hdr := map[string]string{"X-Confirmation-Id": cid}
	x.call("biller", "invoice.issue", map[string]any{"id": x.v("d4")}, hdr)
	x.call("biller", "confirmation.decide", map[string]any{"id": cid, "approve": true})
	x.call("owner", "confirmation.decide", map[string]any{"id": cid, "approve": true})
	x.call("biller", "invoice.issue", map[string]any{"id": x.v("d4")}, hdr)
	x.call("biller", "invoice.issue", map[string]any{"id": x.v("d4")}, hdr)
	// a confirmation for another command cannot be reused here
	r2 := x.call("biller", "payment.record", map[string]any{"invoice_id": x.v("d4"), "amount_minor": 500})
	cid2, _ := r2["confirmation_id"].(string)
	x.call("owner", "confirmation.decide", map[string]any{"id": cid2, "approve": true})
	x.call("biller", "invoice.issue", map[string]any{"id": x.v("d4")}, map[string]string{"X-Confirmation-Id": cid2})
	x.call("biller", "payment.record", map[string]any{"invoice_id": x.v("d4"), "amount_minor": 500}, map[string]string{"X-Confirmation-Id": cid2})
	// the approved input is authoritative, not the replayed one
	r3 := x.call("biller", "payment.record", map[string]any{"invoice_id": x.v("d4"), "amount_minor": 700})
	cid3, _ := r3["confirmation_id"].(string)
	x.call("owner", "confirmation.decide", map[string]any{"id": cid3, "approve": true})
	x.call("biller", "payment.record", map[string]any{"invoice_id": x.v("d4"), "amount_minor": 1}, map[string]string{"X-Confirmation-Id": cid3})
	r4 := x.call("biller", "invoice.write_off", map[string]any{"id": x.v("d4"), "reason": "agent"})
	x.call("owner", "confirmation.decide", map[string]any{"id": r4["confirmation_id"], "approve": false})
	x.call("biller", "invoice.write_off", map[string]any{"id": x.v("d4"), "reason": "agent"}, map[string]string{"X-Confirmation-Id": fmt.Sprint(r4["confirmation_id"])})
	x.call("biller", "invoice.cancel_irn", map[string]any{"id": x.v("d4"), "reason": "agent"})
	x.call("biller", "credit_note.create", map[string]any{"invoice_id": x.v("d4"), "reason": "agent", "lines": lines(ln("x", 100))})
	x.call("biller", "invoice.cancel", map[string]any{"id": x.v("d4"), "reason": "agent"})
	x.call("biller", "invoice.get", map[string]any{"id": x.v("d4")})
	x.call("owner", "confirmation.list", map[string]any{})
	x.call("owner", "invoice.issue", map[string]any{"id": x.v("d3")})
	x.call("owner", "invoice.issue", map[string]any{"id": x.v("d5")})
	x.call("owner", "invoice.issue", map[string]any{"id": x.v("dprv")})
	x.call("owner", "invoice.issue", map[string]any{"id": x.v("d9")})

	// ---- payment.record
	pay := func(caller, inv string, amt any, extra ...any) {
		in := map[string]any{"invoice_id": x.v(inv), "amount_minor": amt}
		for i := 0; i+1 < len(extra); i += 2 {
			in[extra[i].(string)] = extra[i+1]
		}
		x.call(caller, "payment.record", in)
	}
	pay("owner", "d1", 50_000, "reference", "UTR1 ünï")
	pay("owner", "d1", 0)
	pay("owner", "d1", -5)
	pay("owner", "d1", 9223372036854775807)
	pay("owner", "d1", 10, "method", "bank", "reference", nil)
	pay("owner", "d1", 10, "method", nil)
	pay("member", "d1", 11, "method", "cash")
	pay("apikey", "d1", 12, "method", "card")
	pay("guest", "d1", 1)
	pay("agent", "d1", 1)
	pay("other", "d1", 1)
	pay("noauth", "d1", 1)
	pay("owner", "d3", 1)     // drafted by member, issued above
	pay("owner", "d5", 1)     // issued
	pay("owner", "dprv", 100) // private
	pay("member", "dprv", 100)
	x.call("owner", "payment.record", map[string]any{"invoice_id": missing, "amount_minor": 1})
	x.call("owner", "payment.record", map[string]any{"invoice_id": x.v("task"), "amount_minor": 1})
	for _, raw := range []string{`{}`, `{"invoice_id":"` + x.v("d1") + `"}`, `{"invoice_id":"` + x.v("d1") + `","amount_minor":1.5}`, `{"invoice_id":"` + x.v("d1") + `","amount_minor":"5"}`,
		`{"invoice_id":"` + x.v("d1") + `","amount_minor":9223372036854775808}`, `{"invoice_id":"` + x.v("d1") + `","amount_minor":5,"method":5}`, `{"invoice_id":"nope","amount_minor":5}`} {
		x.raw("owner", "payment.record", raw)
	}
	// idempotency
	ih := map[string]string{"Idempotency-Key": "pay-key-1"}
	x.call("owner", "payment.record", map[string]any{"invoice_id": x.v("d2"), "amount_minor": 1_000}, ih)
	x.call("owner", "payment.record", map[string]any{"invoice_id": x.v("d2"), "amount_minor": 1_000}, ih)
	x.call("owner", "invoice.get", map[string]any{"id": x.v("d2")}, ih) // key reused for another command
	// settle d8 fully, d9 partly
	x.call("owner", "invoice.get", map[string]any{"id": x.v("d8")})
	tot := func(id string) int64 {
		return n(x.call("owner", "invoice.get", map[string]any{"id": x.v(id)})["outstanding_minor"])
	}
	pay("owner", "d8", 1_000, "method", "bank")
	pay("owner", "d8", tot("d8"), "method", "upi")
	pay("owner", "d8", 1)
	pay("owner", "d9", 50)
	pay("owner", "d6", tot("d6")) // reverse charge invoice, paid in full

	// ---- credit_note.create
	cn := func(caller, inv string, reason any, ls any) {
		x.call(caller, "credit_note.create", map[string]any{"invoice_id": x.v(inv), "reason": reason, "lines": ls})
	}
	cn("owner", "d2", "Discount", lines(ln("Discount", 10_000, "quantity", 10)))
	cn("owner", "d2", "Rate 12", lines(ln("Rate 12", 3_333, "quantity", 3, "gst_rate_bp", 1200), ln("Rate 0", 100, "gst_rate_bp", 0)))
	cn("owner", "d2", "x", lines(ln("Too much", 9_999_999_999)))
	cn("owner", "d2", "x", lines(ln("Zero", 0)))
	cn("owner", "d2", "x", []any{})
	cn("owner", "d2", "x", lines(ln("", 5)))
	cn("owner", "d2", "x", lines(ln("neg", -5)))
	cn("owner", "d2", "x", lines(ln("rate", 5, "gst_rate_bp", 3000)))
	cn("owner", "d2", "x", lines(ln("hsn", 5, "hsn_sac", "1")))
	cn("owner", "d2", "", lines(ln("empty reason", 500)))
	cn("owner", "d6", "Reverse charge", lines(ln("rc", 5_000)))
	cn("owner", "d7", "Old", lines(ln("old", 1_000, "item_note_id", x.v("task"))))
	cn("owner", "d3", "Draft?", lines(ln("x", 1)))
	cn("owner", "d5", "Small", lines(ln("x", 500)))
	cn("member", "d5", "Member", lines(ln("x", 500)))
	cn("owner", "d8", "paid", lines(ln("x", 1)))
	cn("guest", "d1", "x", lines(ln("x", 1)))
	cn("agent", "d1", "x", lines(ln("x", 1)))
	cn("other", "d1", "x", lines(ln("x", 1)))
	cn("noauth", "d1", "x", lines(ln("x", 1)))
	cn("owner", "dprv", "prv", lines(ln("x", 1)))
	cn("member", "dprv", "prv", lines(ln("x", 1)))
	x.call("owner", "credit_note.create", map[string]any{"invoice_id": missing, "reason": "x", "lines": lines(ln("x", 1))})
	x.call("owner", "credit_note.create", map[string]any{"invoice_id": x.v("task"), "reason": "x", "lines": lines(ln("x", 1))})
	for _, raw := range []string{`{}`, `{"invoice_id":"` + x.v("d2") + `","lines":[]}`, `{"invoice_id":"` + x.v("d2") + `","reason":"x"}`, `{"invoice_id":"` + x.v("d2") + `","reason":5,"lines":[]}`,
		`{"invoice_id":"` + x.v("d2") + `","reason":"x","lines":[{"description":"a","unit_price_minor":1.5}]}`} {
		x.raw("owner", "credit_note.create", raw)
	}

	// ---- invoice.cancel, IRN
	x.call("owner", "invoice.cancel", map[string]any{"id": x.v("d2"), "reason": "has credits"})
	x.call("owner", "invoice.cancel", map[string]any{"id": x.v("d1"), "reason": "has payments"})
	x.call("owner", "invoice.cancel", map[string]any{"id": x.v("d5"), "reason": "has credits"})
	x.call("owner", "invoice.cancel", map[string]any{"id": x.v("d9"), "reason": "partly paid"})
	x.call("owner", "invoice.cancel", map[string]any{"id": x.v("d3"), "reason": "draft"})
	x.call("member", "invoice.cancel", map[string]any{"id": x.v("dprv"), "reason": "no access"})
	x.call("guest", "invoice.cancel", map[string]any{"id": x.v("d7"), "reason": "guest"})
	x.call("other", "invoice.cancel", map[string]any{"id": x.v("d7"), "reason": "other"})
	x.call("owner", "invoice.cancel", map[string]any{"id": missing, "reason": "x"})
	x.raw("owner", "invoice.cancel", `{"id":"`+x.v("d7")+`"}`)
	x.call("owner", "invoice.cancel_irn", map[string]any{"id": x.v("d7"), "reason": "no irn"})
	x.call("owner", "invoice.generate_irn", map[string]any{"id": x.v("d3")}) // draft/cancel checks below
	x.call("owner", "invoice.generate_irn", map[string]any{"id": x.v("dnull")})
	x.call("owner", "invoice.generate_irn", map[string]any{"id": x.v("d7")}) // c33, day 75, aato off
	x.call("owner", "invoice.generate_irn", map[string]any{"id": x.v("d7")}) // already
	x.call("owner", "invoice.generate_irn", map[string]any{"id": x.v("d5")}) // b2c: no buyer gstin
	x.call("owner", "invoice.generate_irn", map[string]any{"id": x.v("d2")}) // with credits: allowed
	x.call("owner", "invoice.cancel", map[string]any{"id": x.v("d7"), "reason": "irn live"})
	x.call("member", "invoice.cancel_irn", map[string]any{"id": x.v("d7"), "reason": "member"})
	x.call("guest", "invoice.cancel_irn", map[string]any{"id": x.v("d7"), "reason": "guest"})
	x.call("agent", "invoice.generate_irn", map[string]any{"id": x.v("d6")})
	x.call("biller", "invoice.generate_irn", map[string]any{"id": x.v("d6")})
	x.call("other", "invoice.generate_irn", map[string]any{"id": x.v("d6")})
	x.call("noauth", "invoice.generate_irn", map[string]any{"id": x.v("d6")})
	x.call("owner", "invoice.cancel_irn", map[string]any{"id": x.v("d7"), "reason": "wrong buyer"})
	x.call("owner", "invoice.cancel_irn", map[string]any{"id": x.v("d7"), "reason": "twice"})
	x.call("owner", "invoice.generate_irn", map[string]any{"id": x.v("d7")}) // IRN present (cancelled): conflict
	x.call("owner", "invoice.cancel", map[string]any{"id": x.v("d7"), "reason": "Duplicate ünï"})
	x.call("owner", "invoice.generate_irn", map[string]any{"id": x.v("d7")}) // cancelled
	x.call("owner", "payment.record", map[string]any{"invoice_id": x.v("d7"), "amount_minor": 1})
	x.call("owner", "invoice.write_off", map[string]any{"id": x.v("d7"), "reason": "cancelled"})
	x.call("owner", "credit_note.create", map[string]any{"invoice_id": x.v("d7"), "reason": "cancelled", "lines": lines(ln("x", 1))})
	x.call("owner", "invoice.cancel_irn", map[string]any{"id": missing, "reason": "x"})
	x.call("owner", "invoice.generate_irn", map[string]any{"id": missing})
	x.raw("owner", "invoice.cancel_irn", `{"id":"`+x.v("d7")+`"}`)
	// credit note documents can take an IRN too
	for _, id := range x.creditNotes() {
		x.call("owner", "invoice.generate_irn", map[string]any{"id": id})
	}
	// 24h rule
	x.call("owner", "invoice.generate_irn", map[string]any{"id": x.v("djul")}) // consumer: error
	x.sql("update billing.invoices set ack_date = now() - interval '25 hours' where irn is not null and irn_cancelled_at is null")
	x.call("owner", "invoice.cancel_irn", map[string]any{"id": x.v("d2"), "reason": "late"})

	// 30-day window for AATO above 10 crore
	x.call("owner", "workspace.update", map[string]any{"settings": map[string]any{"aato_above_10cr": true}})
	for _, off := range []int{-19, -20, -28, -29, -45, 3} {
		x.id(fmt.Sprintf("w%d", off), "owner", "invoice.create_draft", map[string]any{"space_id": sp, "contact_id": c["c27"], "invoice_date": bday(off), "lines": lines(ln("Windowed", 10_000))})
		x.call("owner", "invoice.issue", map[string]any{"id": x.v(fmt.Sprintf("w%d", off))})
		x.call("owner", "invoice.generate_irn", map[string]any{"id": x.v(fmt.Sprintf("w%d", off))})
	}
	x.call("owner", "invoice.cancel_irn", map[string]any{"id": x.v("w-20"), "reason": "window"})
	x.call("owner", "invoice.cancel", map[string]any{"id": x.v("w-20"), "reason": "after irn cancel"})
	x.call("owner", "workspace.update", map[string]any{"settings": map[string]any{"aato_above_10cr": "yes"}})
	x.call("owner", "invoice.generate_irn", map[string]any{"id": x.v("w-45")})
	x.call("owner", "workspace.update", map[string]any{"settings": map[string]any{"aato_above_10cr": false}})
	x.call("owner", "invoice.generate_irn", map[string]any{"id": x.v("w-45")})

	// ---- invoice.write_off
	wo := func(caller, inv string, in map[string]any) {
		in["id"] = x.v(inv)
		x.call(caller, "invoice.write_off", in)
	}
	wo("owner", "d8", map[string]any{"reason": "paid"})
	wo("owner", "d9", map[string]any{"reason": "zero", "amount_minor": 0})
	wo("owner", "d9", map[string]any{"reason": "neg", "amount_minor": -1})
	wo("owner", "d9", map[string]any{"reason": "over", "amount_minor": 9223372036854775807})
	wo("owner", "d9", map[string]any{"reason": "part ünï <&>", "amount_minor": 10})
	wo("owner", "d9", map[string]any{"reason": "null amount", "amount_minor": nil})
	wo("owner", "d1", map[string]any{"reason": "Customer closed"})
	wo("member", "d6", map[string]any{"reason": "member"})
	wo("guest", "d6", map[string]any{"reason": "guest"})
	wo("agent", "d6", map[string]any{"reason": "agent"})
	wo("other", "d6", map[string]any{"reason": "other"})
	wo("noauth", "d6", map[string]any{"reason": "noauth"})
	wo("owner", "d3", map[string]any{"reason": "draft"})
	wo("owner", "dprv", map[string]any{"reason": "private", "amount_minor": 5})
	x.call("owner", "invoice.write_off", map[string]any{"id": missing, "reason": "x"})
	x.raw("owner", "invoice.write_off", `{"id":"`+x.v("d9")+`"}`)
	x.raw("owner", "invoice.write_off", `{"id":"`+x.v("d9")+`","reason":"x","amount_minor":1.5}`)

	// ---- reads: invoice.get / invoice.list for every caller
	for _, who := range []string{"owner", "member", "guest", "biller", "apikey", "other", "noauth", "badkey"} {
		for _, id := range []string{"d1", "d2", "d3", "d7", "dprv", "djul"} {
			x.call(who, "invoice.get", map[string]any{"id": x.v(id)})
		}
		for _, in := range []map[string]any{{}, {"status": "paid"}, {"status": "issued"}, {"status": "cancelled"}, {"status": "draft"}, {"kind": "credit_note"}, {"kind": "invoice", "limit": 2},
			{"contact_id": c["c29"]}, {"contact_id": c["prvc"]}, {"limit": 0}, {"limit": -3}, {"limit": 1000}, {"limit": nil, "status": nil, "kind": nil, "contact_id": nil}, {"status": "nope"}} {
			x.call(who, "invoice.list", in)
		}
	}
	x.call("owner", "invoice.get", map[string]any{"id": missing})
	x.call("owner", "invoice.get", map[string]any{"id": x.v("task")})
	x.raw("owner", "invoice.get", `{}`)
	x.raw("owner", "invoice.get", `{"id":"`+strings.ReplaceAll(x.v("d1"), "-", "")+`"}`)
	x.raw("owner", "invoice.list", `{"limit":1.5}`)
	x.raw("owner", "invoice.list", `{"contact_id":"x"}`)
	x.raw("owner", "invoice.list", `[]`)
	x.call("owner", "invoice.list", nil)

	// ---- reports
	for _, who := range []string{"owner", "member", "guest", "agent", "biller", "apikey", "other", "noauth", "badkey"} {
		for _, in := range []map[string]any{{}, {"as_of": bday(0)}, {"as_of": bday(40)}, {"as_of": bday(120)}, {"as_of": "2020-01-01"}, {"as_of": nil}} {
			x.call(who, "report.receivables_ageing", in)
			x.call(who, "ledger.trial_balance", in)
		}
		for _, in := range []map[string]any{{"from": bday(-100), "to": bday(1)}, {"from": bday(0), "to": bday(0)}, {"from": bday(5), "to": bday(-5)}, {"from": "2024-07-01", "to": "2024-08-31"},
			{"from": "2000-01-01", "to": "2099-12-31"}} {
			x.call(who, "report.gst_summary", in)
			x.call(who, "export.tally", in)
		}
		for _, p := range []string{day(0)[:7], day(-40)[:7], "2024-07", "2024-08", "2024-09", "2020-02", "2030-02"} {
			x.call(who, "report.gstr1", map[string]any{"period": p})
		}
	}
	for _, p := range []string{"2026-13", "2026-00", "x", "", "2026", "2026-", "-2026-05", "+2024-08", "2024-+8", "2024-08-01", "2024-8", "024-08", "0001-01", "9999-12", "10000-01", "++2024-08", "2024--8", "2024-08 ", " 2024-08", "٢٠٢٤-٠٨"} {
		x.call("owner", "report.gstr1", map[string]any{"period": p})
	}
	for _, raw := range []string{`{}`, `{"period":5}`, `{"period":null}`, `[]`} {
		x.raw("owner", "report.gstr1", raw)
	}
	for _, raw := range []string{`{}`, `{"from":"2026-01-01"}`, `{"from":"2026-01-01","to":"bad"}`, `{"from":"2026-1-1","to":"2026-02-01"}`, `{"from":"0000-01-01","to":"2026-02-01"}`, `{"as_of":"x"}`, `{"as_of":5}`} {
		x.raw("owner", "report.gst_summary", raw)
		x.raw("owner", "export.tally", raw)
		x.raw("owner", "report.receivables_ageing", raw)
		x.raw("owner", "ledger.trial_balance", raw)
	}
	x.call("owner", "ledger.trial_balance", nil)
	x.call("owner", "report.gstr1", map[string]any{"period": day(0)[:7]})
	// settings without a gstin / state: gstr1 echoes null gstin
	x.call("owner", "workspace.update", map[string]any{"settings": map[string]any{"gstin": nil, "state_code": nil}})
	x.call("owner", "report.gstr1", map[string]any{"period": "2024-08"})
	x.call("owner", "report.gstr1", map[string]any{"period": day(0)[:7]})
}

func (x *bx) creditNotes() []string {
	r := x.call("owner", "invoice.list", map[string]any{"kind": "credit_note", "limit": 3})
	_ = r
	tx, err := dbTenant(x.e, x.f.owner.ws)
	if err != nil {
		x.e.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(context.Background(), "select note_id::text from billing.invoices where kind = 'credit_note' and buyer_gstin is not null order by number limit 3")
	if err != nil {
		x.e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func (x *bx) dump() []string {
	x.e.t.Helper()
	tx, err := dbTenant(x.e, x.f.owner.ws)
	if err != nil {
		x.e.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var out []string
	for _, q := range []struct{ name, sql string }{
		{"invoices", "select to_jsonb(i)::text from billing.invoices i order by (select created_at from core.notes where id = i.note_id)"},
		{"lines", `select (to_jsonb(l) - 'id')::text from billing.invoice_lines l join billing.invoices i on i.note_id = l.invoice_id
		   order by (select created_at from core.notes where id = i.note_id), l.position`},
		{"payments", "select (to_jsonb(p) - 'id')::text from billing.payments p order by p.received_at, p.amount_minor, p.reference nulls last"},
		{"accounts", "select to_jsonb(a)::text from billing.accounts a order by a.code"},
		{"journal", `select (to_jsonb(e) - 'id' - 'source_id')::text || ' ' || coalesce((select string_agg(l.account_code || ':' || l.debit_minor || ':' || l.credit_minor, ',' order by l.account_code, l.debit_minor, l.credit_minor)
		   from billing.journal_lines l where l.entry_id = e.id), '') from billing.journal_entries e order by e.posted_at, e.memo, e.source_type`},
		{"sequences", "select to_jsonb(s)::text from billing.sequences s order by s.name"},
		{"notes", `select (to_jsonb(n) - 'search')::text from core.notes n where n.types && array['invoice','credit_note'] order by n.title, n.created_at, n.content_md`},
		{"relations", "select to_jsonb(r)::text from core.relations r where r.kind = 'bills' order by r.created_at, r.to_id::text"},
		{"events", "select (to_jsonb(e) - 'id')::text from core.events e where e.actor_type in ('user','agent') and e.kind not like 'doc.%' and (e.kind like 'invoice.%' or e.kind like 'payment.%' or e.kind like 'credit_note.%' or e.kind = 'note.created') order by e.id"},
		{"dispatch", "select count(*)::text from core.jobs j where j.kind = 'event.dispatch' and exists (select 1 from core.events e where e.id = (j.payload->>'event_id')::bigint and (e.kind like 'invoice.%' or e.kind like 'payment.%' or e.kind like 'credit_note.%'))"},
		{"confirmations", "select (to_jsonb(c) - 'id')::text from core.confirmations c where c.command like 'invoice.%' or c.command like 'payment.%' or c.command like 'credit_note.%' order by c.created_at"},
		{"idempotency", "select (to_jsonb(i) - 'created_at')::text from core.idempotency i order by i.command, i.key"},
		{"usage", "select count(*)::text from core.usage u where u.kind = 'mcp.call' and u.meta->>'command' like any (array['invoice.%','payment.%','credit_note.%'])"},
	} {
		rows, err := tx.Query(context.Background(), q.sql)
		if err != nil {
			x.e.t.Fatalf("%s: %v", q.name, err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				x.e.t.Fatal(err)
			}
			out = append(out, q.name+" "+x.norm(s))
		}
		rows.Close()
	}
	return out
}

func TestDifferentialBillingWrites(t *testing.T) {
	e := newEnv(t)
	run := func(base string) *bx {
		x := &bx{runner: &runner{e: e, base: base, f: e.fixture(), vars: map[string]string{}, toks: map[string]string{}}, ctc: map[string]string{}}
		x.setup()
		billingScript(x)
		e.settle(x.f.owner.ws)
		x.transcript = append(x.transcript, x.dump()...)
		return x
	}
	rust, gov := run(e.rust), run(e.gosv.URL)
	if len(rust.transcript) != len(gov.transcript) {
		seen := map[string]int{}
		for _, l := range rust.transcript {
			seen[l]++
		}
		for _, l := range gov.transcript {
			seen[l]--
		}
		for l, c := range seen {
			if c != 0 {
				t.Logf("%+d %.500s", c, l)
			}
		}
		t.Fatalf("transcript lengths differ: rust %d go %d", len(rust.transcript), len(gov.transcript))
	}
	bad, ok2xx, errs := 0, 0, 0
	for i := range rust.transcript {
		if rust.transcript[i] != gov.transcript[i] {
			bad++
			if bad <= 20 {
				a, b := rust.transcript[i], gov.transcript[i]
				k := 0
				for k < len(a) && k < len(b) && a[k] == b[k] {
					k++
				}
				from := max(k-150, 0)
				t.Errorf("step %d differs at byte %d\n rust ...%.400s\n go   ...%.400s", i, k, a[from:], b[min(from, len(b)):])
			}
		}
		if strings.Contains(rust.transcript[i], "-> 200 ") {
			ok2xx++
		} else if strings.Contains(rust.transcript[i], " -> 4") {
			errs++
		}
	}
	if ok2xx < 400 || errs < 250 {
		t.Fatalf("script not rich enough: %d ok, %d errors", ok2xx, errs)
	}
	t.Logf("compared %d steps and rows (%d ok responses, %d 4xx); %d differ", len(rust.transcript), ok2xx, errs, bad)

	// reads: raw bytes, Go vs Rust, on both fixtures (same database state, so ids need no normalization)
	for _, x := range []*bx{rust, gov} {
		reads := 0
		ids := []string{}
		for _, k := range []string{"d1", "d2", "d3", "d5", "d7", "d8", "d9", "dprv", "djul", "dfar", "w-20", "w-29", "dnostate"} {
			ids = append(ids, x.v(k))
		}
		for _, cn := range []string{"owner", "member", "guest", "biller", "apikey", "other", "noauth", "badkey"} {
			w := x.who(cn)
			type call struct {
				cmd string
				in  any
			}
			calls := []call{{"invoice.list", map[string]any{}}, {"invoice.list", map[string]any{"kind": "credit_note"}}, {"invoice.list", map[string]any{"status": "paid", "limit": 1}},
				{"report.receivables_ageing", map[string]any{}}, {"ledger.trial_balance", map[string]any{}}, {"export.tally", map[string]any{"from": day(-1000), "to": day(5)}},
				{"report.gst_summary", map[string]any{"from": "2000-01-01", "to": "2099-12-31"}}, {"report.gstr1", map[string]any{"period": day(0)[:7]}}, {"report.gstr1", map[string]any{"period": "2024-08"}}}
			for _, id := range ids {
				calls = append(calls, call{"invoice.get", map[string]any{"id": id}}, call{"invoice.preview", map[string]any{"id": id}})
			}
			for _, c := range calls {
				time.Sleep(55 * time.Millisecond)
				g := e.cmd(e.gosv.URL, w, x.f.owner.ws, c.cmd, c.in, nil)
				ru := e.cmd(e.rust, w, x.f.owner.ws, c.cmd, c.in, nil)
				reads++
				if g.status != ru.status || string(g.body) != string(ru.body) {
					t.Errorf("%s %s %v\n go   %d %.900s\n rust %d %.900s", cn, c.cmd, c.in, g.status, g.body, ru.status, ru.body)
				}
			}
		}
		t.Logf("raw-compared %d read pairs", reads)
	}
}

var tallyLine = regexp.MustCompile(`<ALLLEDGERENTRIES\.LIST>.*?</ALLLEDGERENTRIES\.LIST>`)

// export.tally orders the lines of one voucher by journal_lines.id (a random uuid) in Rust and Go alike, so
// twin fixtures sort them before comparing; the same-database raw comparison below keeps the real order.
func sortTallyLines(s string) string {
	parts := strings.Split(s, "</TALLYMESSAGE>")
	for i, p := range parts {
		ls := tallyLine.FindAllString(p, -1)
		if len(ls) < 2 {
			continue
		}
		sort.Strings(ls)
		parts[i] = tallyLine.ReplaceAllStringFunc(p, func(string) string { l := ls[0]; ls = ls[1:]; return l })
	}
	return strings.Join(parts, "</TALLYMESSAGE>")
}

// Invoices of one date have no defined order in the ageing report (order by invoice_date only), in Rust and Go alike.
func sortAgeing(s string) string {
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if dec.Decode(&m) != nil {
		return s
	}
	inv, _ := m["invoices"].([]any)
	sort.Slice(inv, func(i, j int) bool {
		a, b := inv[i].(map[string]any), inv[j].(map[string]any)
		return fmt.Sprint(a["invoice_date"], a["number"]) < fmt.Sprint(b["invoice_date"], b["number"])
	})
	out, _ := json.Marshal(m)
	return string(out)
}

func orderless(cmd string, status int, body string) string {
	switch {
	case cmd == "export.tally":
		return sortTallyLines(body)
	case cmd == "report.receivables_ageing" && status == 200:
		return sortAgeing(body)
	}
	return body
}
