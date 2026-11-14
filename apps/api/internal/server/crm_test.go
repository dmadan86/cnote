package server_test

import (
	"context"
	"encoding/json"
	"testing"
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
		if g.status != 403 {
			t.Fatalf("viewer %s: %d %s", v.cmd, g.status, g.body)
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
			r := e.cmd(e.api, w, o.ws, cmd, v, nil)
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
