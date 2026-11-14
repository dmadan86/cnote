package server_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cnote/api/internal/modules"
)

var mcpAccept = map[string]string{"Accept": "application/json, text/event-stream"}

// mcpCall posts a tools/call to base/mcp and returns the HTTP response plus the parsed tool result.
func (b *bt) mcpCall(base, key, tool string, args any) (resp, map[string]any) {
	b.t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}}
	r := b.do(base, "POST", "/mcp", who{bearer: key}, body, mcpAccept)
	var out struct {
		Result map[string]any `json:"result"`
	}
	_ = json.Unmarshal(r.body, &out)
	return r, out.Result
}

func (b *bt) invoke(key, cmd string, args map[string]any, extra map[string]any) map[string]any {
	b.t.Helper()
	a := map[string]any{"command": cmd, "arguments": args}
	for k, v := range extra {
		a[k] = v
	}
	_, res := b.mcpCall(b.gosv.URL, key, "invoke", a)
	if res == nil {
		b.t.Fatalf("invoke %s: no result", cmd)
	}
	return res
}

func structured(res map[string]any) map[string]any {
	m, _ := res["structuredContent"].(map[string]any)
	return m
}

func TestMCPAgentRunsGoOnlyQuoteWithHumanConfirmation(t *testing.T) {
	b := newBT(t, false)
	b.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "manage_crm", "manage_invoices"}}})
	key := b.must(b.owner, "agent.create", map[string]any{"name": "Seller", "scopes": []string{"read", "manage_crm", "manage_invoices"}})["api_key"].(string)

	// discover: Go-only entry carries group, confirmation flag and schema; Rust's commands are still there
	_, d := b.mcpCall(b.gosv.URL, key, "discover", map[string]any{"query": "quote send"})
	cmds := structured(d)["commands"].([]any)
	top := cmds[0].(map[string]any)
	eq(t, top["name"], "quote.send")
	eq(t, top["group"], "manage_invoices")
	eq(t, top["confirmation_required"], true)
	eq(t, top["input_schema"].(map[string]any)["required"], []any{"id"})
	eq(t, d["isError"], false)
	_, d = b.mcpCall(b.gosv.URL, key, "discover", map[string]any{"query": "invoice", "limit": 50})
	names := ""
	for _, c := range structured(d)["commands"].([]any) {
		names += c.(map[string]any)["name"].(string) + " "
	}
	for _, want := range []string{"invoice.issue", "quote.convert_to_invoice", "recurring.create"} {
		if !strings.Contains(names, want) {
			t.Errorf("%s missing from discover: %s", want, names)
		}
	}

	// draft (no confirmation), by underscore name too
	q := b.invoke(key, "quote_create_draft", map[string]any{"space_id": b.space, "contact_id": b.contact,
		"lines": []any{map[string]any{"description": "Consulting", "unit_price_minor": 100000}}}, nil)
	eq(t, q["isError"], false)
	id := structured(q)["id"].(string)

	// send is gated: confirmation first, nothing sent
	r := b.invoke(key, "quote.send", map[string]any{"id": id}, nil)
	s := structured(r)
	eq(t, s["confirmation_required"], true)
	if !strings.Contains(s["message"].(string), s["confirmation_id"].(string)) {
		t.Errorf("no approval message: %v", s)
	}
	eq(t, b.ok("quote.get", map[string]any{"id": id})["status"], "draft")
	cid := s["confirmation_id"].(string)

	// unapproved id is refused (isError with a code), the agent cannot approve itself
	r = b.invoke(key, "quote.send", map[string]any{"id": id}, map[string]any{"confirmation_id": cid})
	eq(t, r["isError"], true)
	eq(t, structured(r)["error"].(map[string]any)["code"], "forbidden")

	b.ok("confirmation.decide", map[string]any{"id": cid, "approve": true})
	r = b.invoke(key, "quote.send", map[string]any{"id": id}, map[string]any{"confirmation_id": cid})
	eq(t, structured(r)["status"], "sent")
	r = b.invoke(key, "quote.send", map[string]any{"id": id}, map[string]any{"confirmation_id": cid}) // single use
	eq(t, r["isError"], true)

	// idempotent replay of a write
	args := map[string]any{"space_id": b.space, "contact_id": b.contact, "lines": []any{map[string]any{"description": "X", "unit_price_minor": 1}}}
	one := b.invoke(key, "quote.create_draft", args, map[string]any{"idempotency_key": "mcp-idem-1"})
	two := b.invoke(key, "quote.create_draft", args, map[string]any{"idempotency_key": "mcp-idem-1"})
	eq(t, structured(one)["id"], structured(two)["id"])

	// invalid input is a tool error, not an HTTP error
	r = b.invoke(key, "quote.create_draft", map[string]any{}, nil)
	eq(t, r["isError"], true)
	eq(t, structured(r)["error"].(map[string]any)["code"], "invalid")
}

func TestMCPGoOnlyHonoursToolGroupsScopesAndWorkspaces(t *testing.T) {
	b := newBT(t, false)
	// default agent groups exclude manage_invoices
	key := b.must(b.owner, "agent.create", map[string]any{"name": "Reader"})["api_key"].(string)
	r := b.invoke(key, "quote.list", map[string]any{}, nil)
	eq(t, r["isError"], false) // read group is on
	r = b.invoke(key, "quote.create_draft", map[string]any{"space_id": b.space, "contact_id": b.contact,
		"lines": []any{map[string]any{"description": "X", "unit_price_minor": 1}}}, nil)
	eq(t, r["isError"], true)
	eq(t, structured(r)["error"].(map[string]any)["code"], "forbidden")

	// a scoped key cannot use a group its scopes lack even when the workspace enables it
	b.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "manage_invoices"}}})
	wide := b.must(b.owner, "agent.create", map[string]any{"name": "Wide", "scopes": []string{"read", "manage_invoices"}})["api_key"].(string)
	r = b.invoke(wide, "quote.create_draft", map[string]any{"space_id": b.space, "contact_id": b.contact,
		"lines": []any{map[string]any{"description": "X", "unit_price_minor": 1}}}, nil)
	eq(t, r["isError"], false)
	scoped := b.must(b.owner, "agent.create", map[string]any{"name": "Narrow", "scopes": []string{"read"}})["api_key"].(string)
	r = b.invoke(scoped, "quote.create_draft", map[string]any{"space_id": b.space, "contact_id": b.contact,
		"lines": []any{map[string]any{"description": "X", "unit_price_minor": 1}}}, nil)
	eq(t, structured(r)["error"].(map[string]any)["code"], "forbidden")

	// another workspace's agent cannot see this workspace's quotes
	other := newBT(t, false)
	other.must(other.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "manage_invoices"}}})
	okey := other.must(other.owner, "agent.create", map[string]any{"name": "Other"})["api_key"].(string)
	id := b.ok("quote.create_draft", map[string]any{"space_id": b.space, "contact_id": b.contact,
		"lines": []any{map[string]any{"description": "X", "unit_price_minor": 1}}})["id"].(string)
	r = other.invoke(okey, "quote.get", map[string]any{"id": id}, nil)
	eq(t, r["isError"], true)

	// bad credentials: Rust's own 401 challenge, unchanged
	hr, _ := b.mcpCall(b.gosv.URL, "cn_nope_nope", "discover", map[string]any{"query": "quote"})
	rr, _ := b.mcpCall(b.rust, "cn_nope_nope", "discover", map[string]any{"query": "quote"})
	if hr.status != 401 || string(hr.body) != string(rr.body) {
		t.Fatalf("401 differs: %d %s vs %d %s", hr.status, hr.body, rr.status, rr.body)
	}
}

// Rust still answers everything it knows; Go must not change a byte of it.
func TestMCPRustAnswersAreByteIdentical(t *testing.T) {
	b := newBT(t, false)
	key := b.must(b.owner, "agent.create", map[string]any{"name": "Same"})["api_key"].(string)
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"discover", map[string]any{"query": "note delete"}},
		{"discover", map[string]any{"query": "zzzz nothing"}},
		{"invoke", map[string]any{"command": "note.list", "arguments": map[string]any{}}},
		{"invoke", map[string]any{"command": "nope.nope"}},
	} {
		g, _ := b.mcpCall(b.gosv.URL, key, c.tool, c.args)
		r, _ := b.mcpCall(b.rust, key, c.tool, c.args)
		if g.status != r.status || string(g.body) != string(r.body) {
			t.Errorf("%s %v differs:\ngo:   %s\nrust: %s", c.tool, c.args, g.body, r.body)
		}
	}
}

// Shape of Go's own answers equals Rust's for the same kind of reply (compared on a command both can run).
func TestMCPGoResultShapeMatchesRust(t *testing.T) {
	b := newBT(t, false)
	key := b.must(b.owner, "agent.create", map[string]any{"name": "Shape"})["api_key"].(string)
	_, rust := b.mcpCall(b.rust, key, "discover", map[string]any{"query": "note delete"})
	_, goOnly := b.mcpCall(b.gosv.URL, key, "discover", map[string]any{"query": "quote send"})
	keys := func(m map[string]any) (k []string) {
		for x := range m {
			k = append(k, x)
		}
		return
	}
	eq(t, len(keys(rust)), len(keys(goOnly)))
	for k := range rust {
		if _, ok := goOnly[k]; !ok {
			t.Errorf("result key %s missing in Go's reply", k)
		}
	}
}

// Every Go-only command must be described for agents. Go-only = not registered by Rust (read from the Rust source).
func TestGoOnlyCommandsHaveMCPMetadata(t *testing.T) {
	re := regexp.MustCompile(`Command::new\(\s*"([a-z_]+\.[a-z_.]+)"`)
	rust := map[string]bool{}
	_ = filepath.Walk("../../../../crates", func(p string, i os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, ".rs") {
			src, _ := os.ReadFile(p)
			for _, m := range re.FindAllSubmatch(src, -1) {
				rust[string(m[1])] = true
			}
		}
		return nil
	})
	if len(rust) < 100 {
		t.Skip("Rust sources not found")
	}
	for _, c := range modules.Registry().List() {
		hasMeta := c.Description != "" && json.Valid(c.Schema)
		if !rust[c.Name] && !hasMeta {
			t.Errorf("Go-only command %s has no MCP description/schema: add it to modules/mcpmeta.go", c.Name)
		}
		if rust[c.Name] && c.Description != "" {
			t.Errorf("%s exists in Rust: Rust describes it, remove it from mcpmeta.go", c.Name)
		}
		if hasMeta {
			var s map[string]any
			_ = json.Unmarshal(c.Schema, &s)
			if s["type"] != "object" {
				t.Errorf("%s schema is not an object schema", c.Name)
			}
		}
	}
}

// cno_ OAuth tokens: accepted natively only for the resource they were issued for, on the workspace the user approved.
func TestMCPGoOnlyAcceptsOAuthTokensForTheirResourceOnly(t *testing.T) {
	b := newBT(t, false)
	host := strings.TrimPrefix(b.gosv.URL, "http://")
	adminSQL(b.env, b.owner.ws, `insert into core.oauth_clients (client_id, kind, name, redirect_uris) values ('cnc_mcp_test', 'registered', 'T', '{}') on conflict do nothing`)
	grant := func(resource string) string {
		tok := "cno_" + rnd() + rnd()
		adminSQL(b.env, b.owner.ws, `with g as (insert into core.oauth_grants (client_id, user_id, workspace_id, resource) values ('cnc_mcp_test', $1::uuid, $2::uuid, $3) returning id)
			insert into core.oauth_tokens (token_hash, kind, grant_id, expires_at) select encode(sha256($4::bytea), 'hex'), 'access', id, now() + interval '1 hour' from g`,
			b.owner.userID, b.owner.ws, resource, tok)
		return tok
	}
	good, wrong := grant("http://"+host+"/mcp"), grant("http://elsewhere.example/mcp")
	r := b.invoke(good, "quote.list", map[string]any{}, nil)
	eq(t, r["isError"], false)
	hr, _ := b.mcpCall(b.gosv.URL, wrong, "invoke", map[string]any{"command": "quote.list"})
	rr, _ := b.mcpCall(b.rust, wrong, "invoke", map[string]any{"command": "quote.list"})
	if hr.status != 401 || string(hr.body) != string(rr.body) {
		t.Fatalf("wrong audience must get Rust's 401: %d %s", hr.status, hr.body)
	}
	adminSQL(b.env, b.owner.ws, `update core.oauth_grants set revoked_at = now() where user_id = $1::uuid and resource = $2`, b.owner.userID, "http://"+host+"/mcp")
	if hr, _ = b.mcpCall(b.gosv.URL, good, "invoke", map[string]any{"command": "quote.list"}); hr.status != 401 {
		t.Fatalf("revoked grant accepted: %d", hr.status)
	}
}

// Go renders merged results exactly like serde_json::to_string_pretty over the structured value.
func TestMCPMergedDiscoverTextEqualsStructured(t *testing.T) {
	b := newBT(t, false)
	key := b.must(b.owner, "agent.create", map[string]any{"name": "Fmt"})["api_key"].(string)
	_, res := b.mcpCall(b.gosv.URL, key, "discover", map[string]any{"query": "invoice"})
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	var back any
	if err := json.Unmarshal([]byte(text), &back); err != nil {
		t.Fatal(err)
	}
	eq(t, back, res["structuredContent"])
	if !strings.HasPrefix(text, "{\n  \"commands\": [\n    {\n      \"") {
		t.Errorf("not serde pretty format: %.60q", text)
	}
}
