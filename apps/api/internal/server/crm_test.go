package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Port of finance.rs::crm_pipeline_and_validation and CRM tenant assertions.
func TestCRMPipelineAndValidation(t *testing.T) {
	e := newEnv(t)
	o := e.signup("CRM", true)
	sp := e.space(o)
	call := func(cmd string, in any) map[string]any {
		t.Helper()
		res := e.cmd(e.gosv.URL, o, o.ws, cmd, in, nil)
		if res.status != 200 {
			t.Fatalf("%s: %d %s", cmd, res.status, res.body)
		}
		return res.json()
	}
	bad := e.cmd(e.gosv.URL, o, o.ws, "contact.create", map[string]any{"space_id": sp, "name": "X", "gstin": "29ABCDE1234F1ZZ"}, nil)
	if bad.status != 400 || bad.json()["error"].(map[string]any)["code"] != "invalid" {
		t.Fatalf("%s", bad.body)
	}
	c := call("contact.create", map[string]any{"space_id": sp, "name": "Buyer", "state_code": "29"})["id"]
	u := call("contact.update", map[string]any{"id": c, "phone": "+91 99999 00000", "name": "Renamed"})
	if u["phone"] != "+91 99999 00000" || u["name"] != "Renamed" || u["state_code"] != "29" {
		t.Fatal(u)
	}
	d := call("deal.create", map[string]any{"space_id": sp, "title": "Big project", "contact_id": c, "amount_minor": 5000000})
	if d["stage"] != "lead" {
		t.Fatal(d)
	}
	id := d["id"]
	q := call("deal.move_stage", map[string]any{"id": id, "stage": "proposal"})
	if q["stage"] != "proposal" {
		t.Fatal(q)
	}
	if r := e.cmd(e.gosv.URL, o, o.ws, "deal.move_stage", map[string]any{"id": id, "stage": "nope"}, nil); r.status == 200 {
		t.Fatal("accepted invalid stage")
	}
	won := call("deal.move_stage", map[string]any{"id": id, "stage": "won"})
	if won["probability"] != float64(100) {
		t.Fatal(won)
	}
	if _, ok := won["closed_at"].(string); !ok {
		t.Fatal(won)
	}
	call("deal.move_stage", map[string]any{"id": id, "stage": "won"})
	tx, err := dbTenant(e, o.ws)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var changes, wins int
	err = tx.QueryRow(context.Background(), `select count(*) filter(where kind='deal.stage_changed'),count(*) filter(where kind='deal.won') from core.events where note_id=$1::uuid`, id).Scan(&changes, &wins)
	if err != nil || changes != 2 || wins != 1 {
		t.Fatalf("events: %d %d %v", changes, wins, err)
	}
	for stage, want := range map[string]int{"won": 1, "lead": 0} {
		r := e.cmd(e.gosv.URL, o, o.ws, "deal.list", map[string]any{"stage": stage}, nil)
		var ds []any
		if json.Unmarshal(r.body, &ds) != nil || len(ds) != want {
			t.Fatalf("%s: %s", stage, r.body)
		}
	}
	other := e.signup("Other CRM", true)
	r := e.cmd(e.gosv.URL, other, other.ws, "contact.get", map[string]any{"id": c}, nil)
	if r.status != 404 {
		t.Fatalf("isolation %d %s", r.status, r.body)
	}
	e.must(other, "contact.create", map[string]any{"space_id": e.space(other), "name": "Buyer", "state_code": "29"})
	r = e.cmd(e.gosv.URL, other, other.ws, "contact.list", map[string]any{"q": "Buyer"}, nil)
	var cs []any
	if json.Unmarshal(r.body, &cs) != nil || len(cs) != 1 {
		t.Fatalf("isolation list %s", r.body)
	}
}

func crmScript(r *runner) {
	sp := r.f.space
	missing := "00000000-0000-4000-8000-000000000000"
	r.id("contact", "owner", "contact.create", map[string]any{"space_id": sp, "name": " Buyer 日本語 🚀 ", "gstin": " 27aapfu0939f1zv ", "email": "buyer@example.test", "company": "Acme"})
	r.id("plain", "member", "contact.create", map[string]any{"space_id": sp, "name": "Plain", "kind": "company", "state_code": "29"})
	for _, in := range []map[string]any{{"name": ""}, {"name": "   "}, {"name": "X", "kind": "bad"}, {"name": "X", "gstin": "bad"}, {"name": "X", "gstin": "29ABCDE1234F1ZZ"}, {"name": "X", "state_code": "00"}, {"name": "X", "gstin": "27AAPFU0939F1ZV", "state_code": "29"}} {
		in["space_id"] = sp
		r.call("owner", "contact.create", in)
	}
	for _, in := range []map[string]any{{"name": " Renamed ", "phone": "+91 99999 00000"}, {"gstin": "", "state_code": ""}, {"gstin": "27AAPFU0939F1ZV"}, {"gstin": "27AAPFU0939F1ZV", "state_code": "29"}, {"state_code": " 27 "}, {"name": "  ", "email": "", "billing_address": "Unicode\u2028address"}, {"gstin": nil, "state_code": nil}} {
		in["id"] = r.v("contact")
		r.call("owner", "contact.update", in)
	}
	r.id("deal", "owner", "deal.create", map[string]any{"space_id": sp, "title": "Big project", "contact_id": r.v("contact"), "amount_minor": 5000000, "expected_close": "2030-02-28", "probability": 40})
	for _, in := range []map[string]any{{"title": " Updated ", "amount_minor": 9007199254740993, "probability": 60, "owner_id": r.f.bob.userID}, {"probability": 101}, {"amount_minor": -1}, {"contact_id": r.v("deal")}, {"contact_id": missing}, {"title": " ", "contact_id": nil, "expected_close": nil}} {
		in["id"] = r.v("deal")
		r.call("owner", "deal.update", in)
	}
	for _, s := range []string{"proposal", "nope", "won", "won", "lost", "qualified", "negotiation", "lead"} {
		r.call("owner", "deal.move_stage", map[string]any{"id": r.v("deal"), "stage": s})
	}
	for _, s := range []string{"won", "lost"} {
		r.id(s, "owner", "deal.create", map[string]any{"space_id": sp, "title": s, "stage": s, "probability": 5})
	}
	for _, in := range []map[string]any{{"stage": "closed"}, {"amount_minor": -1}, {"probability": -1}, {"probability": 101}, {"contact_id": r.v("deal")}, {"contact_id": missing}, {"title": ""}, {"title": strings.Repeat("界", 2000)}} {
		in["space_id"] = sp
		if _, ok := in["title"]; !ok {
			in["title"] = "Invalid"
		}
		r.call("owner", "deal.create", in)
	}
	for _, caller := range []string{"owner", "member", "guest", "bob", "agent", "apikey", "other", "noauth", "badkey"} {
		for _, id := range []string{r.v("contact"), r.v("deal"), missing} {
			r.call(caller, "contact.get", map[string]any{"id": id})
		}
		for _, lim := range []any{nil, 0, -2, 1, 500, 999999} {
			r.call(caller, "contact.list", map[string]any{"limit": lim, "q": "  "})
			r.call(caller, "deal.list", map[string]any{"limit": lim})
		}
		r.call(caller, "contact.create", map[string]any{"space_id": sp, "name": "by " + caller})
		r.call(caller, "deal.create", map[string]any{"space_id": sp, "title": "by " + caller})
		r.call(caller, "contact.update", map[string]any{"id": r.v("plain"), "company": caller})
		r.call(caller, "deal.update", map[string]any{"id": r.v("deal"), "title": caller})
		r.call(caller, "deal.move_stage", map[string]any{"id": r.v("deal"), "stage": "won"})
	}
	for _, q := range []string{"Buyer", "Acme", "%", "_", "日本語", "", "  ", strings.Repeat("q", 2000)} {
		r.call("owner", "contact.list", map[string]any{"q": q})
	}
	for _, s := range []string{"won", "lead", "bogus", ""} {
		r.call("owner", "deal.list", map[string]any{"stage": s, "contact_id": r.v("contact"), "space_id": sp})
	}
	for _, cmd := range []string{"contact.create", "contact.get", "contact.list", "contact.update", "deal.create", "deal.list", "deal.update", "deal.move_stage"} {
		for _, raw := range []string{`{}`, `[]`, `null`, `{"id":"bad","space_id":5}`, `{"limit":1.5}`, `{"space_id":"` + sp + `","title":7,"name":7}`, `{"id":"` + r.v("deal") + `","stage":null,"probability":2147483648}`} {
			r.raw("owner", cmd, raw)
		}
	}
}
func crmDump(r *runner) []string {
	r.e.t.Helper()
	tx, err := dbTenant(r.e, r.f.owner.ws)
	if err != nil {
		r.e.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var out []string
	for _, q := range []struct{ name, sql string }{
		{"contacts", `select to_jsonb(c)::text from crm.contacts c join core.notes n on n.id=c.note_id order by n.title`},
		{"deals", `select to_jsonb(d)::text from crm.deals d join core.notes n on n.id=d.note_id order by n.title,n.created_at`},
		{"notes", `select (to_jsonb(n)-'search')::text from core.notes n where types && array['contact','deal'] order by n.title,n.created_at`},
		{"events", `select (to_jsonb(e)-'id')::text from core.events e where actor_type in ('user','agent') and (kind like 'contact.%' or kind like 'deal.%' or kind='note.created') order by id`},
		{"jobs", `select jsonb_build_object('kind',j.kind,'event_kind',e.kind,'payload',e.payload,'note_id',e.note_id)::text from core.jobs j join core.events e on (j.payload->>'event_id')::bigint=e.id where j.kind='event.dispatch' and e.actor_type in ('user','agent') and (e.kind like 'contact.%' or e.kind like 'deal.%' or e.kind='note.created') order by e.id`},
	} {
		rows, err := tx.Query(context.Background(), q.sql)
		if err != nil {
			r.e.t.Fatal(err)
		}
		for rows.Next() {
			var s string
			if err = rows.Scan(&s); err != nil {
				r.e.t.Fatal(err)
			}
			out = append(out, q.name+" "+r.norm(s))
		}
		if err = rows.Err(); err != nil {
			r.e.t.Fatal(err)
		}
		rows.Close()
	}
	return out
}
func TestDifferentialCRMWrites(t *testing.T) {
	e := newEnv(t)
	run := func(base string) *runner {
		r := &runner{e: e, base: base, f: e.fixture(), vars: map[string]string{}, toks: map[string]string{}}
		crmScript(r)
		r.transcript = append(r.transcript, crmDump(r)...)
		return r
	}
	a, b := run(e.rust), run(e.gosv.URL)
	if !reflect.DeepEqual(a.transcript, b.transcript) {
		for i := 0; i < len(a.transcript) && i < len(b.transcript); i++ {
			if a.transcript[i] != b.transcript[i] {
				t.Errorf("step %d\nRust %s\nGo   %s", i, a.transcript[i], b.transcript[i])
			}
		}
		if len(a.transcript) != len(b.transcript) {
			t.Errorf("length %d != %d", len(a.transcript), len(b.transcript))
		}
	}
	t.Logf("compared %d twin responses and DB rows", len(a.transcript))
	reads := 0
	for _, r := range []*runner{a, b} {
		for _, cn := range []string{"owner", "member", "guest", "bob", "agent", "apikey", "other", "noauth", "badkey"} {
			for _, v := range []struct {
				cmd string
				in  any
			}{{"contact.get", map[string]any{"id": r.v("contact")}}, {"contact.list", map[string]any{}}, {"contact.list", map[string]any{"q": "日本語", "limit": 1}}, {"deal.list", map[string]any{}}, {"deal.list", map[string]any{"stage": "won", "space_id": r.f.space}}} {
				time.Sleep(55 * time.Millisecond)
				g := e.cmd(e.gosv.URL, r.f.who(cn), r.f.owner.ws, v.cmd, v.in, nil)
				ru := e.cmd(e.rust, r.f.who(cn), r.f.owner.ws, v.cmd, v.in, nil)
				if g.status != ru.status || string(g.body) != string(ru.body) {
					t.Errorf("raw %s %s: Go %d %s Rust %d %s", cn, v.cmd, g.status, g.body, ru.status, ru.body)
				}
				reads++
			}
		}
	}
	t.Log(fmt.Sprintf("raw compared %d read pairs", reads))
}

func TestCRMAccessScopesAndReplay(t *testing.T) {
	e := newEnv(t)
	f := e.fixture()
	o := f.owner
	call := func(w who, cmd string, in any, h map[string]string) resp {
		return e.cmd(e.gosv.URL, w, o.ws, cmd, in, h)
	}
	ok := func(r resp) map[string]any {
		t.Helper()
		if r.status != 200 {
			t.Fatalf("%d %s", r.status, r.body)
		}
		return r.json()
	}
	key := rnd()
	in := map[string]any{"space_id": f.space, "name": "Replay"}
	h := map[string]string{"Idempotency-Key": key}
	a := call(o, "contact.create", in, h)
	b := call(o, "contact.create", in, h)
	if a.status != 200 || a.status != b.status || string(a.body) != string(b.body) {
		t.Fatalf("replay %s / %s", a.body, b.body)
	}
	id := a.json()["id"]
	tx, err := dbTenant(e, o.ws)
	if err != nil {
		t.Fatal(err)
	}
	var notes, events, jobs int
	err = tx.QueryRow(context.Background(), `select (select count(*) from core.notes where id=$1::uuid),(select count(*) from core.events where note_id=$1::uuid),(select count(*) from core.jobs j join core.events e on e.id=(j.payload->>'event_id')::bigint where e.note_id=$1::uuid and j.kind='event.dispatch')`, id).Scan(&notes, &events, &jobs)
	if err != nil || notes != 1 || events != 2 || jobs != 2 {
		t.Fatalf("replay rows %d %d %d %v", notes, events, jobs, err)
	}
	_, err = tx.Exec(context.Background(), `insert into core.space_members(workspace_id,space_id,user_id,role) values($1::uuid,$2::uuid,$3::uuid,'viewer')`, o.ws, f.space, f.bob.userID)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	deal := ok(call(o, "deal.create", map[string]any{"space_id": f.space, "title": "Access"}, nil))["id"]
	for _, v := range []struct {
		cmd string
		in  any
	}{{"contact.create", in}, {"contact.update", map[string]any{"id": id, "name": "denied"}}, {"deal.create", map[string]any{"space_id": f.space, "title": "denied"}}, {"deal.update", map[string]any{"id": deal, "title": "denied"}}, {"deal.move_stage", map[string]any{"id": deal, "stage": "won"}}} {
		g := call(f.bob, v.cmd, v.in, nil)
		r := e.cmd(e.rust, f.bob, o.ws, v.cmd, v.in, nil)
		if g.status != 403 || g.status != r.status || string(g.body) != string(r.body) {
			t.Fatalf("viewer %s: %d %s / %d %s", v.cmd, g.status, g.body, r.status, r.body)
		}
	}
	ok(call(f.bob, "contact.get", map[string]any{"id": id}, nil))
	// Personal read-only and agent keys must pass the common scope/tool-group gates.
	k := e.must(o, "apikey.create", map[string]any{"name": "read only", "scopes": []string{"read"}})["api_key"].(string)
	ro := who{ws: o.ws, bearer: k}
	ok(call(ro, "contact.get", map[string]any{"id": id}, nil))
	e.must(o, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read"}}})
	for _, w := range []who{ro, f.agent} {
		for _, cmd := range []string{"contact.create", "deal.create", "contact.update", "deal.update", "deal.move_stage"} {
			v := map[string]any{"space_id": f.space, "name": "denied", "title": "denied", "id": id, "stage": "won"}
			g := call(w, cmd, v, nil)
			r := e.cmd(e.rust, w, o.ws, cmd, v, nil)
			if g.status != r.status || string(g.body) != string(r.body) || g.status == 200 {
				t.Fatalf("scope %s: %d %s / %d %s", cmd, g.status, g.body, r.status, r.body)
			}
		}
	}
	// Private spaces and page restrictions also apply to typed notes.
	private := ok(call(o, "space.create", map[string]any{"key": "PRV", "name": "Private", "visibility": "private"}, nil))["id"]
	cid := ok(call(o, "contact.create", map[string]any{"space_id": private, "name": "Hidden"}, nil))["id"]
	if r := call(f.member, "contact.get", map[string]any{"id": cid}, nil); r.status != 404 {
		t.Fatalf("private: %s", r.body)
	}
	ok(call(o, "note.restrict", map[string]any{"note_id": id, "restriction": "view"}, nil))
	if r := call(f.member, "contact.get", map[string]any{"id": id}, nil); r.status != 404 {
		t.Fatalf("restricted: %s", r.body)
	}
}
