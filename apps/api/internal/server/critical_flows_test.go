package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/modules/billing"
	"github.com/cnote/api/internal/proxy"
	"github.com/cnote/api/internal/server"
)

// newCritical is the production shape: the Go server with the TypeScript collab service behind /collab, fixtures made
// through that same server, and the background worker running the jobs.
func newCritical(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	port := startCollab(t)
	front := server.New(e.pool, command.NewRegistry(modules.Commands()...))
	var err error
	if front.Collab, err = proxy.New("http://127.0.0.1:" + itoa(port)); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(front)
	t.Cleanup(srv.Close)
	e.api, e.gosv = srv.URL, srv
	return e
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestCriticalFlows(t *testing.T) {
	n := newCritical(t)
	t.Setenv("TRUST_PROXY", "1")
	unsetEnv(t, "PUBLIC_BASE_URL")
	call := func(w who, cmd string, in any) resp { return n.cmd(n.gosv.URL, w, w.ws, cmd, in, nil) }
	ok := func(w who, cmd string, in any) map[string]any {
		t.Helper()
		r := call(w, cmd, in)
		if r.status != 200 {
			t.Fatalf("%s: %d %s", cmd, r.status, r.body)
		}
		return r.json()
	}

	// signup, login, session
	pw := rnd() + rnd()
	owner := n.signup("Tess", true)
	body, _ := json.Marshal(map[string]any{"email": owner.email, "password": pw})
	if r := n.do(n.gosv.URL, "POST", "/api/auth/login", who{}, json.RawMessage(body), nil); r.status != 401 {
		t.Fatalf("login with the wrong password must fail: %d", r.status) // the signup password is random and not kept
	}
	if r := n.do(n.gosv.URL, "GET", "/api/auth/me", owner, nil, nil); r.status != 200 || !strings.Contains(string(r.body), owner.email) {
		t.Fatalf("me: %d %s", r.status, r.body)
	}

	// notes, search, versions and restore (the live document is rendered by the collab service)
	space := ok(owner, "space.create", map[string]any{"key": "DOC", "name": "Docs"})["id"].(string)
	note := ok(owner, "note.create", map[string]any{"space_id": space, "title": "Plan", "content_md": "v0 ünï"})["id"].(string)
	ok(owner, "note.update", map[string]any{"id": note, "content_md": "v1"})
	ok(owner, "note.update", map[string]any{"id": note, "content_md": "v2"})
	if r := call(owner, "search.notes", map[string]any{"q": "v2"}); r.status != 200 {
		t.Fatalf("search: %d %s", r.status, r.body)
	}
	revs := call(owner, "note.revisions", map[string]any{"note_id": note})
	var list []map[string]any
	_ = json.Unmarshal(revs.body, &list)
	if len(list) < 1 {
		t.Fatalf("revisions: %s", revs.body)
	}
	got := ok(owner, "note.restore", map[string]any{"note_id": note, "revision_id": list[len(list)-1]["id"]})
	if got["version"] == nil {
		t.Fatalf("restore: %v", got)
	}

	// invoices
	ok(owner, "workspace.update", map[string]any{"settings": map[string]any{"gstin": billing.CompleteGSTIN("29ABCDE1234F1Z"), "state_code": "29"}})
	fin := ok(owner, "space.create", map[string]any{"key": "FIN", "name": "Finance"})["id"].(string)
	contact := ok(owner, "contact.create", map[string]any{"space_id": fin, "name": "Buyer", "kind": "company", "gstin": billing.CompleteGSTIN("29AABCT1332L1Z")})["id"].(string)
	inv := ok(owner, "invoice.create_draft", map[string]any{"space_id": fin, "contact_id": contact, "invoice_date": day(0),
		"lines": []any{map[string]any{"description": "Consulting", "hsn_sac": "998314", "quantity": 2, "unit_price_minor": 40_000}}})["id"].(string)
	ok(owner, "invoice.issue", map[string]any{"id": inv})
	ok(owner, "ledger.trial_balance", map[string]any{})

	// an import runs on the Go worker
	imp := ok(owner, "import.start", map[string]any{"source": "csv", "space_id": space, "content": "Name\nA\nB\n", "mapping": map[string]any{"title": "Name"}})["id"].(string)
	for i := 0; ; i++ {
		if st := ok(owner, "import.status", map[string]any{"id": imp}); st["status"] == "done" {
			break
		}
		if i > 200 {
			t.Fatal("the import never finished")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// MCP with an API key, including the protocol's corner cases
	key := n.apiKey(owner)
	mcp := func(b string) resp {
		req, _ := http.NewRequest("POST", n.gosv.URL+"/mcp", strings.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+key.bearer)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			k, err := res.Body.Read(buf)
			sb.Write(buf[:k])
			if err != nil {
				break
			}
		}
		return resp{res.StatusCode, []byte(sb.String())}
	}
	for _, b := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, `{"jsonrpc":"2.0","id":3,"method":"resources/list"}`, `{"jsonrpc":"2.0","id":4,"method":"ping","params":{"_meta":{"progressToken":1}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"nope"}`, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`, `{bad`, `[]`, `{"jsonrpc":"2.0","id":1,"result":{}}`,
	} {
		if r := mcp(b); r.status >= 500 {
			t.Fatalf("mcp %s: %d %s", b, r.status, r.body)
		}
	}
	if r := mcp(`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"note_get","arguments":{"id":"` + note + `"}}}`); r.status != 200 || strings.Contains(string(r.body), `"isError":true`) {
		t.Fatalf("mcp tools/call: %d %s", r.status, r.body)
	}

	// OAuth: register, consent, token, use the token on MCP and REST, refresh, revoke
	o := newOCFor(t, n)
	client, tok := o.connect(owner)
	access := tok["access_token"].(string)
	if s := o.mcpStatus(access); s != 200 {
		t.Fatalf("mcp with an OAuth token: %d", s)
	}
	if s := o.command(access, owner.ws, "space.list"); s != 200 {
		t.Fatalf("command with an OAuth token: %d", s)
	}
	r := o.postForm("/oauth/token", "grant_type", "refresh_token", "refresh_token", tok["refresh_token"].(string), "client_id", client)
	if r.status != 200 {
		t.Fatalf("refresh: %d %s", r.status, r.body)
	}
	if r := o.postForm("/oauth/revoke", "token", r.json()["refresh_token"].(string), "client_id", client); r.status != 200 {
		t.Fatalf("revoke: %d %s", r.status, r.body)
	}

	// bodies serde rejects, unknown routes and methods
	for _, c := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/api/w/" + owner.ws + "/commands/note.get", `{bad`, 400},
		{"POST", "/api/w/" + owner.ws + "/commands/note.get", `{}`, 400},
		{"POST", "/api/w/" + owner.ws + "/commands/nosuch.cmd", `{}`, 404},
		{"POST", "/api/w/not-a-uuid/commands/note.get", `{}`, 400},
		{"GET", "/api/w/" + owner.ws + "/commands/note.get", ``, 405},
		{"GET", "/no/such/route", ``, 404},
		{"POST", "/api/auth/login", `{"email":5}`, 422},
		{"POST", "/api/auth/signup", `{}`, 422},
	} {
		req, _ := http.NewRequest(c.method, n.gosv.URL+c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", "cn_session="+owner.cookie)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != c.status {
			t.Errorf("%s %s %s: %d, want %d", c.method, c.path, c.body, res.StatusCode, c.status)
		}
	}
}
