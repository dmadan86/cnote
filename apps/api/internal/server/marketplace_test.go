package server_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func marketApp() map[string]any {
	return map[string]any{"slug": "app-" + rnd(), "name": "Sync Bot", "tagline": "Syncs things", "description": "Longer text", "category": "developer", "redirect_url": "http://127.0.0.1:9/callback?x=1", "scopes": []string{"read", "write_items"}}
}
func marketRun(e *env, w who, cmd string, in any) resp {
	return e.cmd(e.gosv.URL, w, w.ws, cmd, in, nil)
}
func marketOK(e *env, w who, cmd string, in any) map[string]any {
	return adminOK(e.t, marketRun(e, w, cmd, in))
}
func marketStatus(t *testing.T, r resp, status int) {
	t.Helper()
	if r.status != status {
		t.Fatalf("want %d: %d %s", status, r.status, r.body)
	}
}
func marketArray(t *testing.T, r resp) []map[string]any {
	t.Helper()
	marketStatus(t, r, 200)
	var a []map[string]any
	if err := json.Unmarshal(r.body, &a); err != nil {
		t.Fatal(err)
	}
	return a
}
func marketHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// Ports every assertion from cnote-core/tests/marketplace.rs, including exchange via the Rust endpoint.
func TestMarketplaceLifecycle(t *testing.T) {
	e := newEnv(t)
	publisher := e.signup("Publisher", true)
	t.Setenv("CNOTE_PLATFORM_ADMINS", " , @EXAMPLE.TEST , ")
	app := marketApp()
	saved := marketOK(e, publisher, "dev.app_save", app)
	id := saved["app_id"].(string)
	secret := saved["client_secret"].(string)
	other := e.signup("Installer", true)
	marketStatus(t, marketRun(e, other, "market.install", map[string]any{"app_id": id}), 404)
	marketOK(e, publisher, "dev.app_submit", map[string]any{"id": id})
	outsider := e.addMember(publisher, e.signup("Outsider", false), "admin")
	t.Setenv("CNOTE_PLATFORM_ADMINS", "")
	marketStatus(t, marketRun(e, publisher, "market.review_queue", map[string]any{}), 403)
	t.Setenv("CNOTE_PLATFORM_ADMINS", " , "+strings.ToUpper(publisher.email)+" , ")
	for _, cmd := range []string{"market.review", "market.review_queue"} {
		marketStatus(t, marketRun(e, outsider, cmd, map[string]any{"id": id, "approve": true}), 403)
	}
	queue := marketArray(t, marketRun(e, publisher, "market.review_queue", map[string]any{}))
	found := false
	for _, a := range queue {
		if a["id"] == id {
			found = true
		}
	}
	if !found {
		t.Fatal(queue)
	}
	marketOK(e, publisher, "market.review", map[string]any{"id": id, "approve": true})
	listed := marketArray(t, marketRun(e, other, "market.list", map[string]any{"q": app["slug"]}))
	if len(listed) != 1 || listed[0]["installed"] != false {
		t.Fatal(listed)
	}
	member := e.addMember(other, e.signup("Member", false), "member")
	marketStatus(t, marketRun(e, member, "market.install", map[string]any{"app_id": id}), 403)
	marketOK(e, other, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "admin"}}})
	a := marketOK(e, other, "agent.create", map[string]any{"name": "bot", "scopes": []string{"read", "admin"}})
	agent := who{ws: other.ws, bearer: a["api_key"].(string)}
	marketStatus(t, marketRun(e, agent, "market.install", map[string]any{"app_id": id}), 403)
	w := marketOK(e, other, "workspace.update", map[string]any{"settings": map[string]any{"modules": []string{"docs"}}})
	if got, _ := json.Marshal(w["settings"].(map[string]any)["agent_tool_groups"]); string(got) != `["read","admin"]` {
		t.Fatal(w)
	}
	got := marketOK(e, other, "market.get", map[string]any{"slug": app["slug"]})
	if b, _ := json.Marshal(got["blocked_scopes"]); string(b) != `["write_items"]` {
		t.Fatal(got)
	}
	// Published app reads are byte-identical on the same fixture.
	for _, cmd := range []string{"market.list", "market.get", "dev.apps"} {
		in := map[string]any{}
		if cmd == "market.get" {
			in["slug"] = app["slug"]
		}
		if cmd == "market.list" {
			in["q"] = app["slug"]
		}
		sameAdmin(t, e.cmd(e.gosv.URL, publisher, publisher.ws, cmd, in, nil), e.cmd(e.rust, publisher, publisher.ws, cmd, in, nil))
	}
	inst := marketOK(e, other, "market.install", map[string]any{"app_id": id})
	redirect := inst["redirect_to"].(string)
	if !strings.HasPrefix(redirect, "http://127.0.0.1:9/callback?x=1&code=cnic_") || !strings.HasSuffix(redirect, "&workspace_id="+other.ws) {
		t.Fatal(inst)
	}
	marketStatus(t, marketRun(e, other, "market.install", map[string]any{"app_id": id}), 409)
	marketStatus(t, marketRun(e, publisher, "dev.app_delete", map[string]any{"id": id}), 409)
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	code := u.Query().Get("code")
	if h := adminScalar(e, other.ws, "select code_hash from core.app_install_codes where install_id=$1::uuid", inst["install_id"]); h != marketHash(code) {
		t.Fatal("code hash mismatch")
	}
	exchange := func(client, sec string) resp {
		return e.do(e.gosv.URL, "POST", "/api/marketplace/token", who{}, map[string]any{"client_id": client, "client_secret": sec, "code": code}, nil)
	}
	wrong := rnd()
	marketStatus(t, exchange(id, wrong), 400)
	marketStatus(t, exchange("00000000-0000-4000-8000-000000000001", secret), 400)
	tok := adminOK(t, exchange(id, secret))
	if tok["token_type"] != "bearer" || tok["workspace_id"] != other.ws {
		t.Fatal(tok)
	}
	marketStatus(t, exchange(id, secret), 400)
	sameAdmin(t, e.cmd(e.gosv.URL, other, other.ws, "market.installed", map[string]any{}, nil), e.cmd(e.rust, other, other.ws, "market.installed", map[string]any{}, nil))
	key := tok["access_token"].(string)
	p := who{ws: other.ws, bearer: key}
	marketStatus(t, marketRun(e, p, "market.installed", map[string]any{}), 200)
	marketStatus(t, marketRun(e, p, "note.create", map[string]any{"title": "x"}), 403)
	marketStatus(t, marketRun(e, p, "market.uninstall", map[string]any{"app_id": id}), 403)
	if list := marketArray(t, marketRun(e, other, "market.installed", map[string]any{})); len(list) != 1 || list[0]["app_id"] != id {
		t.Fatal(list)
	}
	if marketOK(e, other, "market.uninstall", map[string]any{"app_id": id})["uninstalled"] != true {
		t.Fatal("not uninstalled")
	}
	marketStatus(t, marketRun(e, p, "market.installed", map[string]any{}), 401)
	if marketOK(e, other, "market.uninstall", map[string]any{"app_id": id})["uninstalled"] != false {
		t.Fatal("uninstall not idempotent")
	}
	app["id"] = id
	app["slug"] = "app-" + rnd()
	marketOK(e, publisher, "dev.app_save", app)
	mine := marketArray(t, marketRun(e, publisher, "dev.apps", map[string]any{}))
	if mine[0]["status"] != "review" {
		t.Fatal(mine)
	}
	if _, ok := mine[0]["client_secret_hash"]; ok {
		t.Fatal("leaked hash")
	}
	hdr := map[string]string{"Idempotency-Key": rnd()}
	r1 := adminOK(t, e.cmd(e.gosv.URL, publisher, publisher.ws, "dev.app_rotate_secret", map[string]any{"id": id}, hdr))
	r2 := adminOK(t, e.cmd(e.gosv.URL, publisher, publisher.ws, "dev.app_rotate_secret", map[string]any{"id": id}, hdr))
	if r1["client_secret"] == r2["client_secret"] {
		t.Fatal("secret replay")
	}
	if h := adminScalar(e, publisher.ws, "select client_secret_hash from core.market_apps where id=$1::uuid", id); h != marketHash(r2["client_secret"].(string)) {
		t.Fatal("secret hash")
	}
	if n := adminScalar(e, publisher.ws, "select count(*)::text from core.idempotency where command in ('dev.app_rotate_secret','dev.app_save')"); n != "0" {
		t.Fatal("stored secret")
	}
	if marketOK(e, publisher, "dev.app_delete", map[string]any{"id": id})["deleted"] != true {
		t.Fatal("not deleted")
	}
	// Worker-visible events and their dispatch jobs are persisted together (jobs may already be consumed).
	if n := adminScalar(e, other.ws, "select count(*)::text from core.events where kind in ('market.installed','market.uninstalled') and actor_type='user' and actor_id=$1 and payload->>'app_id'=$2", other.userID, id); n != "2" {
		t.Fatal("events", n)
	}
}

func TestMarketplaceDifferential(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Market diff", true)
	member := e.addMember(owner, e.signup("Member", false), "member")
	guest := e.addMember(owner, e.signup("Guest", false), "guest")
	other := e.signup("Other", true)
	key := e.apiKey(owner)
	agent := who{ws: owner.ws, bearer: e.must(owner, "agent.create", map[string]any{"name": "market-diff"})["api_key"].(string)}
	// Keep review authorization equal to the independently configured reference server.
	t.Setenv("CNOTE_PLATFORM_ADMINS", "@example.test")
	missing := "00000000-0000-4000-8000-000000000000"
	inputs := map[string][]any{"market.list": {map[string]any{}, map[string]any{"q": " 🦉 \u2028 "}, map[string]any{"q": ""}, map[string]any{"q": strings.Repeat("x", 5000)}}, "market.get": {map[string]any{"slug": "missing"}}, "market.installed": {map[string]any{}}, "dev.apps": {map[string]any{}}, "market.install": {map[string]any{"app_id": missing}}, "market.uninstall": {map[string]any{"app_id": missing}}, "dev.app_submit": {map[string]any{"id": missing}}, "dev.app_rotate_secret": {map[string]any{"id": missing}}, "dev.app_delete": {map[string]any{"id": missing}}, "market.review_queue": {map[string]any{}}, "market.review": {map[string]any{"id": missing, "approve": false}}, "dev.app_save": {}}
	for _, change := range []map[string]any{{"scopes": []string{"read", "admin"}}, {"scopes": []string{"read", "destructive"}}, {"scopes": []string{"write_items"}}, {"redirect_url": "http://example.com/cb"}, {"icon_url": "http://example.com/i.png"}, {"slug": "A!"}, {"category": "nope"}, {"name": " "}, {"tagline": strings.Repeat("😀", 121)}, {"description": strings.Repeat("字", 4001)}} {
		a := marketApp()
		for k, v := range change {
			a[k] = v
		}
		inputs["dev.app_save"] = append(inputs["dev.app_save"], a)
		marketStatus(t, marketRun(e, owner, "dev.app_save", a), 400)
	}
	pairs := 0
	for label, w := range map[string]who{"owner": owner, "member": member, "guest": guest, "other": other, "api": key, "agent": agent, "none": {}} {
		for cmd, ins := range inputs {
			for _, in := range ins {
				t.Run(label+"/"+cmd, func(t *testing.T) {
					sameAdmin(t, e.cmd(e.gosv.URL, w, owner.ws, cmd, in, nil), e.cmd(e.rust, w, owner.ws, cmd, in, nil))
				})
				pairs++
			}
		}
	}
	for cmd := range inputs {
		for _, raw := range []string{`[]`, `null`, `{}`, `{"id":"bad","app_id":"bad"}`, `{"slug":null}`, `{"scopes":[null]}`} {
			sameAdmin(t, e.rawJSON(e.gosv.URL, owner, cmd, raw), e.rawJSON(e.rust, owner, cmd, raw))
			pairs++
		}
	}
	t.Logf("compared %d marketplace response pairs", pairs)
}

// Mutations run on twin fixtures; compare the semantic rows as well as normalized responses.
func TestMarketplaceDifferentialWrites(t *testing.T) {
	e := newEnv(t)
	t.Setenv("CNOTE_PLATFORM_ADMINS", "@example.test")
	owners := []who{e.signup("Twin publisher", true), e.signup("Twin publisher", true)}
	bases := []string{e.gosv.URL, e.rust}
	apps := []map[string]any{marketApp(), marketApp()}
	ids := make([]string, 2)
	secrets := make([]string, 2)
	call := func(n int, cmd string, in any) resp { return e.cmd(bases[n], owners[n], owners[n].ws, cmd, in, nil) }
	compare := func(cmd string, inputs []any) {
		t.Helper()
		sameAdmin(t, call(0, cmd, inputs[0]), call(1, cmd, inputs[1]))
	}
	for n := range owners {
		saved := adminOK(t, call(n, "dev.app_save", apps[n]))
		ids[n] = saved["app_id"].(string)
		secrets[n] = saved["client_secret"].(string)
		if !strings.HasPrefix(secrets[n], "cnas_") || len(secrets[n]) != 48 {
			t.Fatal(saved)
		}
		if h := adminScalar(e, owners[n].ws, "select client_secret_hash from core.market_apps where id=$1::uuid", ids[n]); h != marketHash(secrets[n]) {
			t.Fatal("hash")
		}
	}
	// Normalize only fixture identities and generated timestamps, retaining every editable/status column.
	snapshot := func(n int) string {
		return adminScalar(e, owners[n].ws, `select (to_jsonb(a)-'id'-'slug'-'publisher_workspace_id'-'client_secret_hash'-'created_at'-'updated_at')::text from core.market_apps a where id=$1::uuid`, ids[n])
	}
	check := func() {
		t.Helper()
		if a, b := snapshot(0), snapshot(1); a != b {
			t.Fatalf("rows: %s / %s", a, b)
		}
	}
	check()
	byID := []any{map[string]any{"id": ids[0]}, map[string]any{"id": ids[1]}}
	compare("dev.app_submit", byID)
	check()
	compare("dev.app_submit", byID)
	reviews := []any{map[string]any{"id": ids[0], "approve": false, "note": "修正 🦉"}, map[string]any{"id": ids[1], "approve": false, "note": "修正 🦉"}}
	compare("market.review", reviews)
	check()
	compare("dev.app_submit", byID)
	for n := range reviews {
		reviews[n] = map[string]any{"id": ids[n], "approve": true}
	}
	compare("market.review", reviews)
	check()
	installs := make([]map[string]any, 2)
	for n := range owners {
		installs[n] = adminOK(t, call(n, "market.install", map[string]any{"app_id": ids[n]}))
		u, err := url.Parse(installs[n]["redirect_to"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(u.Query().Get("code"), "cnic_") || u.Query().Get("workspace_id") != owners[n].ws {
			t.Fatal(installs[n])
		}
	}
	// Compare agent/install fields, TTL, code hashes, event payloads/actors and dispatch rows.
	state := func(n int) string {
		return adminScalar(e, owners[n].ws, `select jsonb_build_object('agent',jsonb_build_object('name',a.name,'kind',a.kind,'scopes',a.scopes,'owner_matches',a.owner_user_id=i.installed_by),'scopes',i.scopes,'ttl',extract(epoch from(c.expires_at-i.created_at)),'unused',c.used_at is null,'hash_length',length(c.code_hash),'events',(select jsonb_agg(jsonb_build_object('kind',v.kind,'actor_type',v.actor_type,'actor_matches',v.actor_id=i.installed_by::text,'app_matches',v.payload->>'app_id'=i.app_id::text,'install_matches',v.payload->>'install_id'=i.id::text,'dispatch',exists(select 1 from core.jobs j where j.kind='event.dispatch' and j.payload->>'event_id'=v.id::text))) from core.events v where v.kind='market.installed'))::text from core.app_installs i join core.agents a on a.id=i.agent_id join core.app_install_codes c on c.install_id=i.id where i.app_id=$1::uuid`, ids[n])
	}
	if a, b := state(0), state(1); a != b {
		t.Fatalf("install/worker rows: %s / %s", a, b)
	}
	appIDs := []any{map[string]any{"app_id": ids[0]}, map[string]any{"app_id": ids[1]}}
	compare("market.uninstall", appIDs)
	compare("market.uninstall", appIDs)
	worker := func(n int) string {
		return adminScalar(e, owners[n].ws, `select jsonb_agg(jsonb_build_object('kind',v.kind,'actor_type',v.actor_type,'actor_matches',v.actor_id=$1,'app_matches',v.payload->>'app_id'=$2,'has_install',v.payload ? 'install_id','dispatch',exists(select 1 from core.jobs j where j.kind='event.dispatch' and j.payload->>'event_id'=v.id::text)) order by v.kind)::text from core.events v where v.kind in ('market.installed','market.uninstalled')`, owners[n].userID, ids[n])
	}
	if a, b := worker(0), worker(1); a != b {
		t.Fatalf("worker rows: %s / %s", a, b)
	}
	for n := range owners {
		rot := adminOK(t, call(n, "dev.app_rotate_secret", byID[n]))
		if rot["client_secret"] == secrets[n] {
			t.Fatal("did not rotate")
		}
		apps[n]["id"] = ids[n]
		apps[n]["name"] = " Updated 字 "
		saved := adminOK(t, call(n, "dev.app_save", apps[n]))
		if len(saved) != 1 || saved["app_id"] != ids[n] {
			t.Fatal(saved)
		}
	}
	check()
	compare("dev.app_delete", byID)
	for n := range owners {
		if count := adminScalar(e, owners[n].ws, "select count(*)::text from core.app_installs"); count != "0" {
			t.Fatal(count)
		}
	}
}
