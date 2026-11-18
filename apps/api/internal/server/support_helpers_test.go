package server_test

import (
	"encoding/json"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/ai"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeReq struct{ Path, Auth, Key, Version, Body, TS, Sig string }

type fake struct {
	srv   *httptest.Server
	mu    sync.Mutex
	reqs  []fakeReq
	hooks []fakeReq
	mcp   []string // tools/call params
}

func newFake(t *testing.T, mcpToken string) *fake {
	f := &fake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec := fakeReq{Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Key: r.Header.Get("X-Api-Key"), Version: r.Header.Get("Anthropic-Version"), Body: string(b)}
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		w.Header().Set("Content-Type", "application/json")
		text := func() (system, last string) {
			for _, m := range body["messages"].([]any) {
				m := m.(map[string]any)
				if m["role"] == "system" {
					system += m["content"].(string)
				}
				last, _ = m["content"].(string)
			}
			if s, ok := body["system"].(string); ok {
				system = s
			}
			return
		}
		answer := func() (string, bool) { // false: a failure was injected and written
			system, last := text()
			switch {
			case strings.Contains(last, "FAIL500"):
				w.WriteHeader(500)
				return "", false
			case strings.Contains(last, "FAILJSON"):
				_, _ = w.Write([]byte("not json"))
				return "", false
			case strings.Contains(last, "FAILSHAPE"):
				_, _ = w.Write([]byte(`{"choices":[],"content":null}`))
				return "", false
			case strings.Contains(system, "Extract action items") && strings.Contains(last, "BULLETS"):
				return "Sure:\n- [ ] Email Ravi\n* Update the deck\nprose", true
			case strings.Contains(system, "Extract action items"):
				return `Here: [{"title":"Send quote","assignee":"Asha","due":"2026-12-01"},{"title":"Book room","assignee":null}]`, true
			}
			return "echo: " + last[:min(len(last), 300)], true
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/messages":
			f.reqs = append(f.reqs, rec)
			a, ok := answer()
			if !ok {
				return
			}
			if r.URL.Path == "/v1/messages" {
				_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake-claude", "content": []any{map[string]any{"type": "text", "text": a}},
					"usage": map[string]any{"input_tokens": len(b) / 4, "output_tokens": len(a) / 4}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake-gpt", "choices": []any{map[string]any{"message": map[string]any{"content": a}}},
				"usage": map[string]any{"prompt_tokens": len(b) / 4, "completion_tokens": len(a) / 4}})
		case r.URL.Path == "/v1/embeddings":
			if strings.Contains(string(b), "QRY") { // worker indexing calls are not part of the script
				f.reqs = append(f.reqs, rec)
			}
			ins, _ := body["input"].([]any)
			data := []any{}
			for i := len(ins) - 1; i >= 0; i-- { // reversed on purpose: clients must sort by index
				data = append(data, map[string]any{"index": i, "embedding": ai.HashEmbed(ins[i].(string))})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case r.URL.Path == "/hook" || r.URL.Path == "/hook500":
			rec.TS, rec.Sig = r.Header.Get("X-Cnote-Timestamp"), r.Header.Get("X-Cnote-Signature")
			f.hooks = append(f.hooks, rec)
			if r.URL.Path == "/hook500" {
				w.WriteHeader(500)
			}
			_, _ = w.Write([]byte("{}"))
		case r.URL.Path == "/mcp":
			if r.Header.Get("Authorization") != "Bearer "+mcpToken {
				w.WriteHeader(401)
				return
			}
			if r.Method == http.MethodDelete {
				return
			}
			id := body["id"]
			switch body["method"] {
			case "initialize":
				w.Header().Set("Mcp-Session-Id", "sess-1")
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"protocolVersion": "2025-03-26",
					"capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fake", "version": "1"}}})
			case "tools/call":
				p, _ := json.Marshal(body["params"])
				f.mcp = append(f.mcp, string(p))
				boom := body["params"].(map[string]any)["name"] == "boom"
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
					"content": []any{map[string]any{"type": "text", "text": "done"}}, "isError": boom}})
			default:
				w.WriteHeader(202)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// settle waits until the workspace has no queued or running jobs.
func (e *env) settle(ws string) {
	e.t.Helper()
	for i := 0; i < 100; i++ {
		if e.dbStrings(ws, "select count(*)::text from core.jobs where status in ('queued','running')")[0] == "0" {
			time.Sleep(200 * time.Millisecond)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.t.Fatal("worker did not drain")
}

func (r resp) jsonList() []map[string]any {
	var l []map[string]any
	_ = json.Unmarshal(r.body, &l)
	return l
}

const mcpHost = "localhost:4000"

type mcpEnv struct {
	*env
	owner who
	space string
	key   string // agent key of the owner
	note  string
	view  string
}

func newMCPEnv(t *testing.T) *mcpEnv { return newMCPEnvWith(t) }

func newMCPEnvWith(t *testing.T, extra ...command.Command) *mcpEnv {
	e := newEnv(t, extra...)
	m := &mcpEnv{env: e, owner: e.signup("Tess", true)}
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

func rpc(id any, method string, params any) string {
	m := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		m["params"] = params
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func call(tool string, args any) any { return map[string]any{"name": tool, "arguments": args} }

type fixture struct {
	owner, member, guest, bob, carol, agent, apikey, other, noauth, badkey who
	space                                                                  string
}

func (e *env) fixture() fixture {
	o := e.signup("Owner", true)
	f := fixture{owner: o, other: e.signup("Other", true), noauth: who{}, badkey: who{bearer: "cn_x_y"}}
	f.member = e.addMember(o, e.signup("Member", false), "member")
	f.guest = e.addMember(o, e.signup("Guest", false), "guest")
	f.bob = e.addMember(o, e.signup("Bob", false), "member")
	f.carol = e.addMember(o, e.signup("Carol", false), "member")
	f.agent = who{ws: o.ws, bearer: e.must(o, "agent.create", map[string]any{"name": "diff-bot"})["api_key"].(string)}
	f.apikey = e.apiKey(o)
	f.space = e.space(o)
	return f
}

func (f fixture) who(n string) who {
	return map[string]who{"owner": f.owner, "member": f.member, "guest": f.guest, "bob": f.bob, "carol": f.carol,
		"agent": f.agent, "apikey": f.apikey, "other": f.other, "noauth": f.noauth, "badkey": f.badkey}[n]
}

func raw(t *testing.T, method, url, ct, body string, hdr map[string]string) (int, string, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header.Get("Content-Type"), string(b)
}

const missingID = "00000000-0000-4000-8000-000000000000"

// modelReq is what an agent sent to the scripted model: the tool names it was offered, its system prompt, the first
// user turn, how many assistant turns came before, and the tool results of the last user turn.
type modelReq struct {
	Tools         []string
	System, First string
	Turn          int
	Results       []string
}

// scriptedModel is an Anthropic-shaped fake whose answers a test scripts per request (blocks from mtext and muse).
type scriptedModel struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []modelReq
}

func mtext(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func muse(id, name string, in any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": in}
}

func newScriptedModel(t *testing.T, script func(modelReq) []map[string]any) *scriptedModel {
	m := &scriptedModel{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			System   string `json:"system"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		b, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/messages" || json.Unmarshal(b, &body) != nil || len(body.Messages) == 0 {
			w.WriteHeader(404)
			return
		}
		req := modelReq{System: body.System}
		for _, x := range body.Tools {
			req.Tools = append(req.Tools, x.Name)
		}
		for i, msg := range body.Messages {
			if msg.Role == "assistant" {
				req.Turn++
			}
			var s string
			if i == 0 && json.Unmarshal(msg.Content, &s) == nil {
				req.First = s
			}
		}
		var blocks []struct{ Type, Content string }
		if last := body.Messages[len(body.Messages)-1]; json.Unmarshal(last.Content, &blocks) == nil {
			for _, x := range blocks {
				if x.Type == "tool_result" {
					req.Results = append(req.Results, x.Content)
				}
			}
		}
		m.mu.Lock()
		m.reqs = append(m.reqs, req)
		m.mu.Unlock()
		out := script(req)
		stop := "end_turn"
		for _, x := range out {
			if x["type"] == "tool_use" {
				stop = "tool_use"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake-claude", "content": out, "stop_reason": stop,
			"usage": map[string]any{"input_tokens": 20, "output_tokens": 10}})
	}))
	t.Cleanup(m.srv.Close)
	return m
}

// useModel points the workspace at the scripted model as its own (BYO) Anthropic endpoint.
func (e *env) useModel(o who, m *scriptedModel) {
	e.t.Helper()
	e.t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "1")
	e.must(o, "ai.set_provider", map[string]any{"provider": "anthropic", "base_url": m.srv.URL, "api_key": "sk-" + rnd()})
}

// waitRun waits for a finished run matching where ("$1" is the first arg) and returns its id.
func (e *env) waitRun(ws, where string, args ...any) string {
	e.t.Helper()
	// 90 s: the shared worker's agent lane (2 at a time) may be busy with other tests' runs on a slow CI machine
	for i := 0; i < 900; i++ {
		if l := e.dbStrings(ws, "select id::text from core.agent_runs where status in ('completed','held','failed','skipped','cancelled') and "+where+" order by created_at limit 1", args...); len(l) > 0 {
			return l[0]
		}
		time.Sleep(100 * time.Millisecond)
	}
	runs := e.dbStrings(ws, "select status || ' ' || trigger || ' ' || coalesce(error,'') from core.agent_runs order by created_at")
	jobs := e.dbStrings(ws, "select kind || ' ' || status || ' attempts=' || attempts || ' ' || coalesce(last_error,'') from core.jobs where kind like 'agent.%'")
	e.t.Fatalf("no finished run where %s; runs %v; jobs %v", where, runs, jobs)
	return ""
}
