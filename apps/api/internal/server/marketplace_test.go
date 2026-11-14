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
	// Published app reads work for the publisher.
	for _, cmd := range []string{"market.list", "market.get", "dev.apps"} {
		in := map[string]any{}
		if cmd == "market.get" {
			in["slug"] = app["slug"]
		}
		if cmd == "market.list" {
			in["q"] = app["slug"]
		}
		adminOK(t, e.cmd(e.gosv.URL, publisher, publisher.ws, cmd, in, nil))
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
