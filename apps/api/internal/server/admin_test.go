package server_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
)

var adminCommands = []string{"member.list", "member.add", "agent.create", "agent.list", "apikey.create", "apikey.list", "apikey.revoke", "workspace.get", "workspace.update", "confirmation.list", "confirmation.decide", "usage.summary"}

func adminOK(t *testing.T, r resp) map[string]any {
	t.Helper()
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	return r.json()
}
func sameAdmin(t *testing.T, g, r resp) {
	t.Helper()
	if g.status != r.status || string(g.body) != string(r.body) {
		t.Fatalf("go %d %s\nrust %d %s", g.status, g.body, r.status, r.body)
	}
}
func adminSQL(e *env, ws, sql string, args ...any) {
	e.t.Helper()
	tx, err := dbTenant(e, ws)
	if err != nil {
		e.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}
func adminScalar(e *env, ws, sql string, args ...any) string {
	e.t.Helper()
	tx, err := dbTenant(e, ws)
	if err != nil {
		e.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var out string
	if err = tx.QueryRow(context.Background(), sql, args...).Scan(&out); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func TestAdminDifferential(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Admin diff", true)
	member := e.addMember(owner, e.signup("Member", false), "member")
	guest := e.addMember(owner, e.signup("Guest", false), "guest")
	admin := e.addMember(owner, e.signup("Admin", false), "admin")
	other := e.signup("Other", true)
	key := e.apiKey(owner)
	agent := who{ws: owner.ws, bearer: e.must(owner, "agent.create", map[string]any{"name": "Diff agent"})["api_key"].(string)}
	missing := "00000000-0000-4000-8000-000000000000"
	inputs := map[string][]any{
		"member.list": {map[string]any{}}, "agent.list": {map[string]any{}}, "apikey.list": {map[string]any{}}, "workspace.get": {map[string]any{}}, "confirmation.list": {map[string]any{}}, "usage.summary": {map[string]any{}},
		"member.add":          {map[string]any{"email": "x", "role": "owner"}, map[string]any{"email": owner.email, "role": "guest"}, map[string]any{"email": admin.email, "role": "guest"}},
		"agent.create":        {map[string]any{"name": "bot", "kind": "invalid"}},
		"apikey.create":       {map[string]any{"name": nil}},
		"apikey.revoke":       {map[string]any{"id": missing}},
		"workspace.update":    {map[string]any{"settings": map[string]any{"agent_tool_groups": []any{"unknown"}}}, map[string]any{"settings": map[string]any{"agent_tool_groups": []any{nil}}}},
		"confirmation.decide": {map[string]any{"id": missing, "approve": true}, map[string]any{"id": missing, "approve": false}},
	}
	// Read-only and deterministic failing writes. Owner/peer member.add success is tested on twins below.
	delete(inputs, "member.add")
	inputs["member.add"] = []any{map[string]any{"email": "x", "role": "owner"}}
	pairs := 0
	for label, w := range map[string]who{"owner": owner, "admin": admin, "member": member, "guest": guest, "other": other, "api": key, "agent": agent, "none": {}} {
		for _, cmd := range adminCommands {
			for _, in := range inputs[cmd] {
				t.Run(label+"/"+cmd+fmt.Sprint(pairs), func(t *testing.T) {
					// usage.summary itself records machine usage; restore the pre-call meter before comparing.
					var before string
					if cmd == "usage.summary" {
						before = adminScalar(e, owner.ws, "select coalesce(max(id),0)::text from core.usage")
					}
					g := e.cmd(e.gosv.URL, w, owner.ws, cmd, in, nil)
					if cmd == "usage.summary" {
						adminSQL(e, owner.ws, "delete from core.usage where id > $1::bigint", before)
					}
					r := e.cmd(e.rust, w, owner.ws, cmd, in, nil)
					sameAdmin(t, g, r)
				})
				pairs++
			}
		}
	}
	for _, cmd := range adminCommands {
		for _, raw := range []string{`[]`, `null`, `{}`, `{"id":"bad","approve":true}`, `{"name":"x","scopes":[null]}`, `{"name":3}`, `{"id":null}`} {
			// Valid empty read inputs remain deterministic, apart from key last_used_at, which is stable here.
			sameAdmin(t, e.rawJSON(e.gosv.URL, owner, cmd, raw), e.rawJSON(e.rust, owner, cmd, raw))
			pairs++
		}
	}
	t.Logf("compared %d admin response pairs", pairs)
}

func TestAdminSecretsAndRoles(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Owner", true)
	admin := e.addMember(owner, e.signup("Admin", false), "admin")
	peer := e.addMember(owner, e.signup("Peer", false), "admin")
	member := e.addMember(owner, e.signup("Member", false), "member")
	guest := e.addMember(owner, e.signup("Guest", false), "guest")
	for _, w := range []who{admin, owner} {
		r := e.cmd(e.gosv.URL, w, owner.ws, "member.add", map[string]any{"email": owner.email, "role": "guest"}, nil)
		if r.status != 403 {
			t.Fatalf("owner demotion: %s", r.body)
		}
	}
	for _, email := range []string{admin.email, peer.email} {
		r := e.cmd(e.gosv.URL, admin, owner.ws, "member.add", map[string]any{"email": email, "role": "guest"}, nil)
		if r.status != 403 {
			t.Fatalf("peer demotion: %s", r.body)
		}
	}
	added := adminOK(t, e.cmd(e.gosv.URL, admin, owner.ws, "member.add", map[string]any{"email": "  " + rnd() + "@EXAMPLE.TEST  ", "role": "guest"}, nil))
	if added["role"] != "guest" || strings.Contains(added["email"].(string), " ") {
		t.Fatal(added)
	}
	for _, w := range []who{member, guest} {
		for _, cmd := range []string{"member.add", "agent.create", "apikey.list", "apikey.revoke", "workspace.update", "usage.summary"} {
			if r := e.cmd(e.gosv.URL, w, owner.ws, cmd, map[string]any{}, nil); r.status != 403 {
				t.Fatalf("%s: %d %s", cmd, r.status, r.body)
			}
		}
	}
	for _, cmd := range []string{"apikey.create", "agent.create"} {
		for _, base := range []string{e.gosv.URL, e.rust} {
			hdr := map[string]string{"Idempotency-Key": rnd()}
			in := map[string]any{"name": "  Bot ünïcode 🚀  "}
			a := adminOK(t, e.cmd(base, owner, owner.ws, cmd, in, hdr))
			b := adminOK(t, e.cmd(base, owner, owner.ws, cmd, in, hdr))
			if a["api_key"] == b["api_key"] {
				t.Fatal("secret replayed")
			}
			secret := a["api_key"].(string)
			if !regexp.MustCompile(`^cn_[0-9a-f]{8}_[A-Za-z0-9_-]{32}$`).MatchString(secret) {
				t.Fatal("wrong key format")
			}
			hash := sha256.Sum256([]byte(secret))
			kid := a["id"]
			if cmd == "agent.create" {
				kid = a["api_key_id"]
			}
			stored := adminScalar(e, owner.ws, "select key_hash from core.api_keys where id=$1::uuid", kid)
			if stored != hex.EncodeToString(hash[:]) {
				t.Fatal("wrong stored hash")
			}
			if n := adminScalar(e, owner.ws, "select count(*)::text from core.idempotency where key=$1", hdr["Idempotency-Key"]); n != "0" {
				t.Fatal("secret stored for replay")
			}
			w := who{ws: owner.ws, bearer: secret}
			for _, target := range []string{e.gosv.URL, e.rust} {
				adminOK(t, e.cmd(target, w, owner.ws, "workspace.get", map[string]any{}, nil))
			}
			if cmd == "agent.create" {
				if n := adminScalar(e, owner.ws, "select count(*)::text from core.agents where id=$1::uuid and owner_user_id=$2::uuid and scopes=array['read','write_notes','write_items','manage_crm']", a["agent_id"], owner.userID); n != "1" {
					t.Fatal("agent ownership/default scopes")
				}
			}
			adminOK(t, e.cmd(e.gosv.URL, owner, owner.ws, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "admin"}}}, nil))
			for _, forbidden := range []string{"agent.create", "apikey.create", "confirmation.decide"} {
				r := e.cmd(e.gosv.URL, w, owner.ws, forbidden, map[string]any{"name": "x", "id": command.NilUUID, "approve": true}, nil)
				if r.status != 403 {
					t.Fatalf("machine %s: %d %s", forbidden, r.status, r.body)
				}
			}
			r := adminOK(t, e.cmd(e.gosv.URL, owner, owner.ws, "apikey.revoke", map[string]any{"id": kid}, nil))
			if r["revoked"] != true {
				t.Fatal(r)
			}
			r = adminOK(t, e.cmd(e.gosv.URL, owner, owner.ws, "apikey.revoke", map[string]any{"id": kid}, nil))
			if r["revoked"] != false {
				t.Fatal(r)
			}
			for _, target := range []string{e.gosv.URL, e.rust} {
				if r := e.cmd(target, w, owner.ws, "workspace.get", map[string]any{}, nil); r.status != 401 {
					t.Fatalf("revoked key: %d %s", r.status, r.body)
				}
			}
		}
	}
}

func TestAdminConfirmationSemantics(t *testing.T) {
	confirm := command.NewCommand("test.admin.confirm", command.GroupWriteNotes, func(c *command.Ctx, b json.RawMessage) (any, error) { return json.RawMessage(b), nil })
	confirm.Confirm = true
	e := newEnv(t, confirm)
	owner := e.signup("Confirm", true)
	member := e.addMember(owner, e.signup("Member", false), "member")
	agent := who{ws: owner.ws, bearer: e.must(owner, "agent.create", map[string]any{"name": "Requester", "scopes": []string{"*"}})["api_key"].(string)}
	for _, approve := range []bool{true, false} {
		requested := adminOK(t, e.cmd(e.gosv.URL, agent, owner.ws, "test.admin.confirm", map[string]any{"approved_input": true}, nil))
		cid := requested["confirmation_id"].(string)
		hdr := map[string]string{"X-Confirmation-Id": cid}
		if r := e.cmd(e.gosv.URL, agent, owner.ws, "test.admin.confirm", map[string]any{}, hdr); r.status != 403 {
			t.Fatal("pending confirmation accepted")
		}
		adminOK(t, e.cmd(e.gosv.URL, owner, owner.ws, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "write_notes", "admin"}}}, nil))
		if r := e.cmd(e.gosv.URL, agent, owner.ws, "confirmation.decide", map[string]any{"id": cid, "approve": approve}, nil); r.status != 403 {
			t.Fatal("agent confirmed itself")
		}
		out := adminOK(t, e.cmd(e.gosv.URL, member, owner.ws, "confirmation.decide", map[string]any{"id": cid, "approve": approve}, nil))
		status := "rejected"
		if approve {
			status = "confirmed"
		}
		if out["status"] != status {
			t.Fatal(out)
		}
		if n := adminScalar(e, owner.ws, "select count(*)::text from core.confirmations where id=$1::uuid and status=$2 and confirmed_by=$3::uuid and decided_at is not null", cid, status, member.userID); n != "1" {
			t.Fatal("decision not persisted")
		}
		if r := e.cmd(e.gosv.URL, owner, owner.ws, "confirmation.decide", map[string]any{"id": cid, "approve": !approve}, nil); r.status != 404 {
			t.Fatal("decision changed")
		}
		r := e.cmd(e.gosv.URL, agent, owner.ws, "test.admin.confirm", map[string]any{"tampered": true}, hdr)
		if approve {
			if r.status != 200 || string(r.body) != `{"approved_input":true}` {
				t.Fatalf("confirmed input: %d %s", r.status, r.body)
			}
		} else if r.status != 403 {
			t.Fatal("rejected confirmation accepted")
		}
		if r := e.cmd(e.gosv.URL, agent, owner.ws, "test.admin.confirm", map[string]any{}, hdr); r.status != 403 {
			t.Fatal("confirmation replayed")
		}
		if n := adminScalar(e, owner.ws, "select count(*)::text from core.events where kind='confirmation.decided' and payload->>'confirmation_id'=$1", cid); n != "1" {
			t.Fatal("decision event missing")
		}
	}
}

// Twin workspaces share their invited user. Generated workspace/owner/key/agent/confirmation
// identifiers are mapped explicitly; secret randomness is checked by its hash, never compared literally.
func TestAdminTwinWrites(t *testing.T) {
	e := newEnv(t)
	g := e.signup("Twin", true)
	r := e.signup("Twin", true)
	invite := e.signup("Invited", false)
	slugs := []string{adminScalar(e, g.ws, "select slug from core.workspaces where id=$1::uuid", g.ws), adminScalar(e, r.ws, "select slug from core.workspaces where id=$1::uuid", r.ws)}
	normalize := func(b []byte) string {
		s := string(b)
		for _, slug := range slugs {
			s = strings.ReplaceAll(s, `"slug":"`+slug+`"`, `"slug":"TWIN"`)
		}
		for _, w := range []who{g, r} {
			s = strings.ReplaceAll(s, w.ws, "WORKSPACE")
			s = strings.ReplaceAll(s, w.userID, "OWNER")
		}
		return s
	}
	compare := func(cmd string, a, b any) {
		t.Helper()
		ga := e.cmd(e.gosv.URL, g, g.ws, cmd, a, nil)
		rb := e.cmd(e.rust, r, r.ws, cmd, b, nil)
		if ga.status != rb.status || normalize(ga.body) != normalize(rb.body) {
			t.Fatalf("%s go %d %s rust %d %s", cmd, ga.status, ga.body, rb.status, rb.body)
		}
	}
	snapshot := func(w who, sql string) string { return normalize([]byte(adminScalar(e, w.ws, sql))) }
	checkRows := func(sql string) {
		t.Helper()
		a, b := snapshot(g, sql), snapshot(r, sql)
		if a != b {
			t.Fatalf("rows differ: %s\ngo %s\nrust %s", sql, a, b)
		}
	}
	for _, role := range []string{"guest", "member", "admin"} {
		in := map[string]any{"email": "  " + strings.ToUpper(invite.email) + "  ", "name": "Δ 🚀 <>&\u2028", "role": role}
		compare("member.add", in, in)
		checkRows("select jsonb_agg(jsonb_build_object('user',user_id,'role',role) order by role)::text from core.memberships")
	}
	hdr := map[string]string{"Idempotency-Key": rnd()}
	in := map[string]any{"email": invite.email, "role": "guest"}
	first := e.cmd(e.gosv.URL, g, g.ws, "member.add", in, hdr)
	sameAdmin(t, first, e.cmd(e.gosv.URL, g, g.ws, "member.add", map[string]any{"email": invite.email, "role": "member"}, hdr))
	// Mirror the one executed idempotent write on the reference twin.
	adminOK(t, e.cmd(e.rust, r, r.ws, "member.add", in, nil))
	for _, settings := range []any{map[string]any{"brand_kit": map[string]any{"unicode": "日本語 🚀\u2028", "n": json.Number("9007199254740993")}}, map[string]any{"agent_tool_groups": []string{"read", "admin", "write_notes"}}, map[string]any{"large": strings.Repeat("界🚀", 20000), "empty": ""}, []any{"array"}, nil} {
		in := map[string]any{"name": "Unicode Δ 🚀", "settings": settings}
		compare("workspace.update", in, in)
		checkRows("select jsonb_build_object('name',name,'settings',settings)::text from core.workspaces where id=current_setting('app.workspace_id')::uuid")
	}
	// Restore settings before creating machine credentials.
	adminSQL(e, g.ws, "update core.workspaces set settings='{}'::jsonb where id=$1::uuid", g.ws)
	adminSQL(e, r.ws, "update core.workspaces set settings='{}'::jsonb where id=$1::uuid", r.ws)
	for _, cmd := range []string{"agent.create", "apikey.create"} {
		in := map[string]any{"name": "  Unicode İ Δ Bot  ", "scopes": []string{"read", "write_notes"}}
		ga := adminOK(t, e.cmd(e.gosv.URL, g, g.ws, cmd, in, nil))
		rb := adminOK(t, e.cmd(e.rust, r, r.ws, cmd, in, nil))
		// Secret bytes and generated IDs differ by design; compare every response field after mapping them.
		gshape, rshape := map[string]any{}, map[string]any{}
		for k, v := range ga {
			gshape[k] = v
		}
		for k, v := range rb {
			rshape[k] = v
		}
		for k := range gshape {
			switch k {
			case "id", "agent_id", "api_key_id", "api_key":
				if _, ok := rb[k].(string); !ok {
					t.Fatalf("missing reference field %s", k)
				}
				gshape[k] = k
				rshape[k] = k
			}
		}
		gb, _ := command.Marshal(gshape)
		rbb, _ := command.Marshal(rshape)
		if string(gb) != string(rbb) {
			t.Fatalf("secret response shape differs: %s / %s", gb, rbb)
		}
		gkid, rkid := ga["id"], rb["id"]
		if cmd == "agent.create" {
			gkid, rkid = ga["api_key_id"], rb["api_key_id"]
			checkRows("select jsonb_agg(jsonb_build_object('name',name,'kind',kind,'owner',owner_user_id,'scopes',scopes) order by name)::text from core.agents")
		}
		for _, w := range []who{g, r} {
			out := ga
			kid := gkid
			if w.ws == r.ws {
				out = rb
				kid = rkid
			}
			secret := out["api_key"].(string)
			hash := sha256.Sum256([]byte(secret))
			if adminScalar(e, w.ws, "select key_hash from core.api_keys where id=$1::uuid", kid) != hex.EncodeToString(hash[:]) {
				t.Fatal("twin key hash mismatch")
			}
		}
		compare("apikey.revoke", map[string]any{"id": gkid}, map[string]any{"id": rkid})
		compare("apikey.revoke", map[string]any{"id": gkid}, map[string]any{"id": rkid})
	}
	checkRows("select jsonb_agg(jsonb_build_object('name',name,'user',user_id,'scopes',scopes,'revoked',revoked_at is not null,'used',last_used_at is not null) order by name)::text from core.api_keys")
	for _, approve := range []bool{true, false} {
		ids := []string{}
		for _, w := range []who{g, r} {
			tx, err := dbTenant(e, w.ws)
			if err != nil {
				t.Fatal(err)
			}
			var cid string
			err = tx.QueryRow(context.Background(), "insert into core.confirmations (workspace_id,command,input,requested_by) values ($1::uuid,'invoice.issue',$2,$3) returning id::text", w.ws, json.RawMessage(`{"amount":42}`), w.userID).Scan(&cid)
			if err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, cid)
		}
		compare("confirmation.decide", map[string]any{"id": ids[0], "approve": approve}, map[string]any{"id": ids[1], "approve": approve})
		compare("confirmation.decide", map[string]any{"id": ids[0], "approve": approve}, map[string]any{"id": ids[1], "approve": approve})
		checkRows("select jsonb_agg(jsonb_build_object('command',command,'input',input,'status',status,'confirmed_by',confirmed_by,'decided',decided_at is not null) order by status)::text from core.confirmations")
	}
	// Events are compared as semantic payloads after removing generated IDs. Each event must have
	// its own dispatch job (the worker may already have processed it).
	checkRows("select jsonb_agg(jsonb_build_object('kind',kind,'actor_type',actor_type,'actor_id',actor_id,'on_behalf_of',on_behalf_of,'payload',payload - 'agent_id' - 'confirmation_id') order by id)::text from core.events where kind in ('member.added','agent.created','workspace.settings_updated','confirmation.decided')")
	for _, w := range []who{g, r} {
		if n := adminScalar(e, w.ws, "select count(*)::text from core.events e where kind in ('member.added','agent.created','workspace.settings_updated','confirmation.decided') and not exists (select 1 from core.jobs j where j.kind='event.dispatch' and (j.payload->>'event_id')::bigint=e.id)"); n != "0" {
			t.Fatal("dispatch job missing")
		}
	}
}

func TestAdminUsageWindow(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Usage", true)
	other := e.signup("Other", true)
	adminSQL(e, owner.ws, "insert into core.usage (workspace_id,kind,quantity,created_at) values ($1::uuid,'alpha',2,now()),($1::uuid,'alpha',3,now()-interval '29 days'),($1::uuid,'alpha',100,now()-interval '31 days'),($1::uuid,'beta',7,now())", owner.ws)
	adminSQL(e, other.ws, "insert into core.usage (workspace_id,kind,quantity) values ($1::uuid,'alpha',999)", other.ws)
	g := e.cmd(e.gosv.URL, owner, owner.ws, "usage.summary", map[string]any{}, nil)
	sameAdmin(t, g, e.cmd(e.rust, owner, owner.ws, "usage.summary", map[string]any{}, nil))
	if string(g.body) != `[{"kind":"alpha","quantity":5},{"kind":"beta","quantity":7}]` {
		t.Fatalf("usage window/isolation: %s", g.body)
	}
}
