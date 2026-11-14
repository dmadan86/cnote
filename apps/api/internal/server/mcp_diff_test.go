package server_test

// MCP differential tests: the same JSON-RPC request goes to Rust (rmcp) and to Go, and status, content type and body
// must be identical bytes. Requests Go answers natively run against a Go server whose upstream fails the test.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/server"
)

const mcpHost = "localhost:4000" // the Host both servers see, so issuer and resource URLs agree

type mcpEnv struct {
	*env
	native *httptest.Server // upstream = test failure: whatever it answers, it answered itself
	owner  who
	space  string
	key    string // agent key of the owner
	note   string
	view   string
}

func newMCPEnv(t *testing.T) *mcpEnv { return newMCPEnvWith(t) }

func newMCPEnvWith(t *testing.T, extra ...command.Command) *mcpEnv {
	e := newEnv(t, extra...)
	guard := httptest.NewServer(server.New(e.pool, command.NewRegistry(append(modules.Commands(), extra...)...), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("proxied to Rust: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusBadGateway)
	})))
	t.Cleanup(guard.Close)
	m := &mcpEnv{env: e, native: guard, owner: e.signup("Tess", true)}
	m.space = e.space(m.owner)
	m.key = e.must(m.owner, "agent.create", map[string]any{"name": "diff"})["api_key"].(string)
	m.note = e.must(m.owner, "note.create", map[string]any{"space_id": m.space, "title": "Launch <plan> &   é \"q\"", "content_md": "Ship it.\n"})["id"].(string)
	e.must(m.owner, "note.create", map[string]any{"space_id": m.space, "title": "Second"})
	m.view = e.must(m.owner, "view.create", map[string]any{"name": "Open", "layout": "table", "query": "project = GEN"})["id"].(string)
	return m
}

type mcpReq struct {
	name    string
	key     string
	body    string
	headers map[string]string
	method  string
	native  bool // must be served by Go itself
}

func (m *mcpEnv) send(base string, q mcpReq) (int, http.Header, []byte) {
	m.t.Helper()
	method := q.method
	if method == "" {
		method = "POST"
	}
	req, _ := http.NewRequest(method, base+"/mcp", strings.NewReader(q.body))
	req.Host = mcpHost
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	if q.key != "" {
		req.Header.Set("Authorization", "Bearer "+q.key)
	}
	for k, v := range q.headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		m.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, b
}

// compare runs q against Rust and Go and requires identical status, relevant headers and body.
func (m *mcpEnv) compare(q mcpReq) {
	m.t.Helper()
	rs, rh, rb := m.send(m.rust, q)
	goBase := m.gosv.URL
	if q.native {
		goBase = m.native.URL
	}
	gs, gh, gb := m.send(goBase, q)
	if rs != gs || !bytes.Equal(rb, gb) {
		m.t.Errorf("%s: differs\nrust %d %.600s\ngo   %d %.600s", q.name, rs, rb, gs, gb)
		return
	}
	for _, h := range []string{"Content-Type", "Www-Authenticate", "Allow"} {
		if rh.Get(h) != gh.Get(h) {
			m.t.Errorf("%s: header %s rust %q go %q", q.name, h, rh.Get(h), gh.Get(h))
		}
	}
}

func rpc(id any, method string, params any) string {
	m := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		m["params"] = params
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func call(tool string, args any) any { return map[string]any{"name": tool, "arguments": args} }

func TestMCPDifferentialProtocol(t *testing.T) {
	m := newMCPEnv(t)
	init := func(v string) any {
		return map[string]any{"protocolVersion": v, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "1"}}
	}
	var qs []mcpReq
	add := func(name, body string, native bool, hdr ...string) {
		h := map[string]string{}
		for i := 0; i+1 < len(hdr); i += 2 {
			h[hdr[i]] = hdr[i+1]
		}
		qs = append(qs, mcpReq{name: name, key: m.key, body: body, headers: h, native: native})
	}
	for _, v := range []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25", "2026-07-28", "1999-01-01", ""} {
		add("initialize "+v, rpc(1, "initialize", init(v)), v != "2026-07-28" && v != "")
	}
	add("initialize with elicitation", rpc(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"elicitation": map[string]any{}, "roots": map[string]any{"listChanged": true}}, "clientInfo": map[string]any{"name": "t", "version": "1"}}), true)
	add("initialize header matches", rpc(1, "initialize", init("2025-06-18")), true, "MCP-Protocol-Version", "2025-06-18")
	add("initialize header mismatch", rpc(1, "initialize", init("2025-06-18")), false, "MCP-Protocol-Version", "2025-03-26")
	add("initialize no params", rpc(1, "initialize", nil), false)
	add("initialize no clientInfo", rpc(1, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}}), false)
	for _, id := range []any{1, "abc", "ü\"\\\n", 9007199254740993, -5, 0, nil, 1.5} {
		add(fmt.Sprintf("ping id %v", id), rpc(id, "ping", nil), id != nil && id != 1.5)
	}
	add("ping with params", rpc(1, "ping", map[string]any{}), true)
	add("ping header legacy", rpc(1, "ping", nil), true, "MCP-Protocol-Version", "2025-06-18")
	add("ping header unknown", rpc(1, "ping", nil), false, "MCP-Protocol-Version", "1999-01-01")
	add("ping header 2026", rpc(1, "ping", nil), false, "MCP-Protocol-Version", "2026-07-28")
	add("notification initialized", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, true)
	add("notification cancelled", `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`, false)
	add("response message", `{"jsonrpc":"2.0","id":1,"result":{}}`, false)
	add("not json", `not json`, false)
	add("batch", `[`+rpc(1, "ping", nil)+`]`, false)
	add("unknown method", rpc(4, "nope/x", nil), false)
	add("tools/list", rpc(2, "tools/list", nil), true)
	add("tools/list cursor", rpc(2, "tools/list", map[string]any{"cursor": "x"}), true)
	add("tools/list meta", rpc(2, "tools/list", map[string]any{"_meta": map[string]any{"a": 1}}), false)
	add("prompts/list", rpc(5, "prompts/list", nil), true)
	add("resources/templates/list", rpc(3, "resources/templates/list", nil), true)
	add("resources/list", rpc(6, "resources/list", nil), true)
	ws := m.owner.ws
	uri := func(kind, id string) string { return "cnote://ws/" + ws + "/" + kind + "/" + id }
	add("read note", rpc(7, "resources/read", map[string]any{"uri": uri("note", m.note)}), true)
	add("read note upper", rpc(7, "resources/read", map[string]any{"uri": uri("note", strings.ToUpper(m.note))}), true)
	add("read note simple uuid", rpc(7, "resources/read", map[string]any{"uri": uri("note", strings.ReplaceAll(m.note, "-", ""))}), true)
	add("read note braced", rpc(7, "resources/read", map[string]any{"uri": uri("note", "{"+m.note+"}")}), false)
	add("read view", rpc(7, "resources/read", map[string]any{"uri": uri("view", m.view)}), true)
	add("read missing note", rpc(7, "resources/read", map[string]any{"uri": uri("note", ws)}), true)
	add("read missing view", rpc(7, "resources/read", map[string]any{"uri": uri("view", ws)}), true)
	add("read other workspace", rpc(7, "resources/read", map[string]any{"uri": "cnote://ws/" + m.note + "/note/" + m.note}), true)
	for _, bad := range []string{"bad", "", "cnote://ws/x/note/y", uri("space", m.note), uri("note", m.note) + "/x", "https://example.com"} {
		add("read bad uri "+bad, rpc(7, "resources/read", map[string]any{"uri": bad}), true)
	}
	add("read no uri", rpc(7, "resources/read", map[string]any{}), false)

	// tools/call: unknown tools, missing/odd shapes, tool-level errors
	add("call unknown tool", rpc(8, "tools/call", call("nope", map[string]any{})), true)
	add("call non-core tool by name", rpc(8, "tools/call", call("note_delete", map[string]any{"id": m.note})), true)
	add("call dotted name", rpc(8, "tools/call", call("note.get", map[string]any{"id": m.note})), true)
	add("call no params", rpc(9, "tools/call", map[string]any{}), false)
	add("call args string", rpc(9, "tools/call", map[string]any{"name": "space_list", "arguments": "x"}), false)
	add("call args null", rpc(9, "tools/call", map[string]any{"name": "space_list", "arguments": nil}), true)
	add("call no args", rpc(9, "tools/call", map[string]any{"name": "space_list"}), true)
	add("call space_list", rpc(9, "tools/call", call("space_list", map[string]any{})), true)
	add("call note_get", rpc(9, "tools/call", call("note_get", map[string]any{"id": m.note})), true)
	add("call note_get unicode title", rpc(9, "tools/call", call("note_list", map[string]any{"space_id": m.space})), true)
	add("call note_get missing", rpc(9, "tools/call", call("note_get", map[string]any{"id": ws})), true)
	add("call note_get bad id", rpc(9, "tools/call", call("note_get", map[string]any{"id": "x"})), false)
	add("call note_create invalid", rpc(9, "tools/call", call("note_create", map[string]any{"title": 42})), false)
	add("call search_notes", rpc(9, "tools/call", call("search_notes", map[string]any{"q": "Second"})), true)
	add("call item_list", rpc(9, "tools/call", call("item_list", map[string]any{})), true)
	add("call with _meta", rpc(9, "tools/call", map[string]any{"name": "space_list", "arguments": map[string]any{}, "_meta": map[string]any{"x": 1}}), false)
	add("call requestState", rpc(9, "tools/call", map[string]any{"name": "space_list", "requestState": "x"}), false)

	// discover
	for _, q := range []string{"note", "note delete", "NOTE  Delete", "zzzz nothing", "ünï cödé", "deal stage", "delete note", "relation", "doc verify"} {
		add("discover "+q, rpc(10, "tools/call", call("discover", map[string]any{"query": q})), true)
	}
	for _, l := range []string{`0`, `1`, `3`, `50`, `100`, `-1`, `"3"`, `3.0`, `1e1`, `null`, `true`} {
		add("discover limit "+l, rpc(10, "tools/call", map[string]any{"name": "discover", "arguments": json.RawMessage(`{"query":"delete note","limit":` + l + `}`)}), true)
	}

	// invoke
	add("invoke no command", rpc(11, "tools/call", call("invoke", map[string]any{})), true)
	add("invoke command number", rpc(11, "tools/call", call("invoke", map[string]any{"command": 5})), true)
	add("invoke unknown", rpc(11, "tools/call", call("invoke", map[string]any{"command": "nope.x"})), true)
	add("invoke space.list", rpc(11, "tools/call", call("invoke", map[string]any{"command": "space.list"})), true)
	add("invoke space_list underscore", rpc(11, "tools/call", call("invoke", map[string]any{"command": "space_list", "arguments": map[string]any{}})), true)
	add("invoke note.get", rpc(11, "tools/call", call("invoke", map[string]any{"command": "note.get", "arguments": map[string]any{"id": m.note}})), true)
	add("invoke note.delete forbidden", rpc(11, "tools/call", call("invoke", map[string]any{"command": "note.delete", "arguments": map[string]any{"id": m.note}})), true)
	add("invoke agent.create forbidden", rpc(11, "tools/call", call("invoke", map[string]any{"command": "agent.create", "arguments": map[string]any{"name": "x"}})), true)
	add("invoke invoice.issue bad id", rpc(11, "tools/call", call("invoke", map[string]any{"command": "invoice.issue", "arguments": map[string]any{"id": "x"}})), false)
	add("invoke with args number", rpc(11, "tools/call", call("invoke", map[string]any{"command": "space.list", "arguments": 5})), false)
	add("invoke with args null", rpc(11, "tools/call", call("invoke", map[string]any{"command": "space.list", "arguments": nil})), false)

	// transport and authentication
	qs = append(qs,
		mcpReq{name: "no auth", body: rpc(1, "ping", nil), native: true},
		mcpReq{name: "bad key", key: "cn_nope_nope", body: rpc(1, "ping", nil), native: true},
		mcpReq{name: "oauth-shaped bad token", key: "cno_nope", body: rpc(1, "ping", nil), native: true},
		mcpReq{name: "Basic auth", body: rpc(1, "ping", nil), headers: map[string]string{"Authorization": "Basic abc"}, native: true},
		mcpReq{name: "empty bearer", body: rpc(1, "ping", nil), headers: map[string]string{"Authorization": "Bearer "}, native: true},
		mcpReq{name: "no accept", key: m.key, body: rpc(1, "ping", nil), headers: map[string]string{"Accept": ""}, native: true},
		mcpReq{name: "accept json only", key: m.key, body: rpc(1, "ping", nil), headers: map[string]string{"Accept": "application/json"}, native: true},
		mcpReq{name: "accept q=0", key: m.key, body: rpc(1, "ping", nil), headers: map[string]string{"Accept": "application/json, text/event-stream;q=0"}, native: true},
		mcpReq{name: "accept q=0.5", key: m.key, body: rpc(1, "ping", nil), headers: map[string]string{"Accept": "application/json;q=0.5, text/event-stream;q=0.001"}, native: true},
		mcpReq{name: "accept wildcard", key: m.key, body: rpc(1, "ping", nil), headers: map[string]string{"Accept": "*/*"}, native: true},
		mcpReq{name: "accept upper", key: m.key, body: rpc(1, "ping", nil), headers: map[string]string{"Accept": "APPLICATION/JSON, Text/Event-Stream"}, native: true},
		mcpReq{name: "content type text", key: m.key, body: rpc(1, "ping", nil), headers: map[string]string{"Content-Type": "text/plain"}, native: true},
		mcpReq{name: "content type charset", key: m.key, body: rpc(1, "ping", nil), headers: map[string]string{"Content-Type": "application/json; charset=utf-8"}, native: true},
		mcpReq{name: "content type missing", key: m.key, body: rpc(1, "ping", nil), headers: map[string]string{"Content-Type": ""}, native: true},
		mcpReq{name: "GET", key: m.key, method: "GET"},
		mcpReq{name: "DELETE", key: m.key, method: "DELETE"},
		mcpReq{name: "GET no auth", method: "GET"},
	)
	for _, q := range qs {
		m.compare(q)
	}
}

// Restricted pages are invisible to a member-level agent: listing, reading and tool results agree with Rust.
func TestMCPDifferentialRestrictedPagesAndOtherWorkspace(t *testing.T) {
	m := newMCPEnv(t)
	memberUser := m.addMember(m.owner, m.signup("Mo", false), "admin")
	k := m.must(memberUser, "agent.create", map[string]any{"name": "mo-bot"})["api_key"].(string)
	m.must(m.owner, "member.add", map[string]any{"email": memberUser.email, "role": "member"}) // demote: the agent follows
	secret := m.must(m.owner, "note.create", map[string]any{"space_id": m.space, "title": "Secret", "content_md": "pangolin"})["id"].(string)
	m.must(m.owner, "note.restrict", map[string]any{"note_id": secret, "restriction": "view"})
	other := m.signup("Zed", true)
	otherKey := m.must(other, "agent.create", map[string]any{"name": "z"})["api_key"].(string)
	ws := m.owner.ws
	for _, key := range []string{k, otherKey, m.key} {
		for _, body := range []string{
			rpc(1, "resources/list", nil),
			rpc(2, "resources/read", map[string]any{"uri": "cnote://ws/" + ws + "/note/" + secret}),
			rpc(3, "tools/call", call("note_get", map[string]any{"id": secret})),
			rpc(4, "tools/call", call("search_notes", map[string]any{"q": "pangolin"})),
			rpc(5, "tools/call", call("space_list", map[string]any{})),
		} {
			m.compare(mcpReq{name: "restricted " + body[:40], key: key, body: body, native: true})
		}
	}
}

// discover: Go adds the Go-only commands Rust does not know. Everything else, order included, must be Rust's answer.
func TestMCPDiscoverEqualsRustPlusGoOnlyCommands(t *testing.T) {
	m := newMCPEnv(t)
	rustNames := map[string]bool{}
	var cat []struct{ Name string }
	r := m.do(m.rust, "GET", "/api/commands", who{}, nil, nil)
	_ = json.Unmarshal(r.body, &cat)
	for _, c := range cat {
		rustNames[c.Name] = true
	}
	for _, q := range []string{"", "invoice", "quote send", "note", "contact", "expense", "report", "member", "recurring", "tds"} {
		body := rpc(1, "tools/call", call("discover", map[string]any{"query": q, "limit": 50}))
		_, _, rb := m.send(m.rust, mcpReq{key: m.key, body: body})
		_, _, gb := m.send(m.native.URL, mcpReq{key: m.key, body: body})
		var rr, gg struct {
			Result struct {
				Structured struct {
					Commands []map[string]any `json:"commands"`
				} `json:"structuredContent"`
			} `json:"result"`
		}
		_ = json.Unmarshal(rb, &rr)
		_ = json.Unmarshal(gb, &gg)
		var kept []map[string]any
		for _, c := range gg.Result.Structured.Commands {
			if rustNames[c["name"].(string)] {
				kept = append(kept, c)
			}
		}
		// With the limit hit, Go-only entries can push Rust ones out, so compare the shared prefix.
		want := rr.Result.Structured.Commands
		if len(gg.Result.Structured.Commands) < 50 && !slices.EqualFunc(kept, want, func(a, b map[string]any) bool { return fmt.Sprint(a) == fmt.Sprint(b) }) {
			t.Errorf("discover %q: Go minus Go-only differs from Rust:\n go %d entries, rust %d entries", q, len(kept), len(want))
		}
		for i := range min(len(kept), len(want)) {
			if fmt.Sprint(kept[i]) != fmt.Sprint(want[i]) {
				t.Errorf("discover %q entry %d differs: %v vs %v", q, i, kept[i]["name"], want[i]["name"])
				break
			}
		}
	}
}

// The embedded catalogue is a snapshot of Rust's registry; fail loudly when Rust moved on.
func TestMCPCatalogMatchesRust(t *testing.T) {
	e := newEnv(t)
	live := e.do(e.rust, "GET", "/api/commands", who{}, nil, nil)
	b, err := os.ReadFile("../mcp/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var a, c []map[string]any
	_ = json.Unmarshal(b, &a)
	_ = json.Unmarshal(live.body, &c)
	if fmt.Sprint(a) != fmt.Sprint(c) {
		t.Fatalf("internal/mcp/catalog.json is stale: regenerate with `curl -s %s/api/commands > internal/mcp/catalog.json`", e.rust)
	}
}
