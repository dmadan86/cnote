package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cnote/api/internal/modules/ai"
)

// Twin-fixture differential test for the AI and hook commands. The same script runs through Rust (fixture A) and
// Go (fixture B). No real provider is ever called: both sides talk to an httptest fake that speaks the OpenAI,
// Anthropic and MCP wire formats and records every request. Responses, DB rows (ai.audit, provider_config, usage,
// events, hooks) and the requests the fake saw must be equal.

type fakeReq struct{ Path, Auth, Key, Version, Body, TS, Sig string }

type fake struct {
	srv   *httptest.Server
	mu    sync.Mutex
	reqs  []fakeReq
	hooks []fakeReq
	mcp   []string // tools/call params
}

var (
	fakeTok  = regexp.MustCompile(`127\.0\.0\.1:\d+`)
	uuidAny  = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	tsAny    = regexp.MustCompile(`\d{4}-\d\d-\d\d[T ]\d\d:\d\d:\d\d(\.\d+)?(Z|\+00:00)?`)
	normBody = func(s string) string { return tsAny.ReplaceAllString(uuidAny.ReplaceAllString(s, "U"), "TS") }
)

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

var secretTok = regexp.MustCompile(`whsec_[A-Za-z0-9_-]{32}|cnc_diff_[0-9a-f]+_\d+`)

var aiCallers = []string{"owner", "member", "bob", "guest", "agent", "apikey", "other", "noauth", "badkey"}

// hyb records search.hybrid without exact scores: rows with equal vector distance (every note that shares no words
// with the query) tie, and their order depends on physical row order, which differs between twin fixtures. Exact bytes
// are compared on one shared fixture afterwards.
func hyb(r *runner, caller string, in any) {
	r.e.t.Helper()
	time.Sleep(30 * time.Millisecond)
	res := r.e.cmd(r.base, r.f.who(caller), r.f.owner.ws, "search.hybrid", in, nil)
	line := fmt.Sprintf("%s search.hybrid -> %d %s", caller, res.status, r.norm(string(res.body)))
	var hits []map[string]any
	if res.status == 200 && json.Unmarshal(res.body, &hits) == nil {
		titles := []string{}
		for _, h := range hits {
			titles = append(titles, fmt.Sprint(h["title"]))
		}
		sort.Strings(titles)
		line = fmt.Sprintf("%s search.hybrid -> 200 n=%d", caller, len(hits))
		if m, ok := in.(map[string]any); ok && m["limit"] == nil && len(hits) < 10 {
			line += fmt.Sprint(titles)
		}
	}
	r.transcript = append(r.transcript, line)
}

func aiScript(r *runner, fk *fake, key string) {
	e, ws, sp := r.e, r.f.owner.ws, r.f.space
	ph := "00000000-0000-4000-8000-000000000001"
	url := fk.srv.URL
	pii := "Weekly sync with asha.k@example.co.in, call 9876543210. GSTIN 29ABCDE1234F1Z5, PAN ABCDE1234F.\n- [ ] Email Ravi\n- Update the deck\n"
	page := func(name, caller string, space any, title, md string) {
		r.id(name, caller, "note.create", map[string]any{"space_id": space, "title": title, "content_md": md})
	}
	r.id("prv", "owner", "space.create", map[string]any{"key": "PRV", "name": "Private", "visibility": "private"})
	page("p1", "owner", sp, "Standing meeting", pii)
	page("p2", "owner", sp, "Deploy runbook", "restart the pods then wait for the rollout ünï 🚀")
	page("p3", "owner", sp, "BULLETS note", "bullets please")
	page("pp", "owner", r.v("prv"), "Secret roadmap", "secret rollout plan for pods")
	page("rest", "owner", sp, "Restricted page", "restricted pods body")
	r.call("owner", "note.restrict", map[string]any{"note_id": r.v("rest"), "restriction": "view"})
	page("long", "owner", sp, "Long page", strings.Repeat("pods and rollout ", 2500))
	page("pf", "owner", sp, "FAIL500 page", "FAIL500 content")
	page("pj", "owner", sp, "FAILJSON page", "FAILJSON content")
	page("ps", "owner", sp, "FAILSHAPE page", "FAILSHAPE content")
	e.settleDbg(ws)

	// ---- phase A: the platform (local) provider
	for _, c := range aiCallers {
		r.call(c, "ai.get_provider", map[string]any{})
		r.call(c, "ai.summarize", map[string]any{"note_id": r.v("p1")})
		r.call(c, "ai.extract_tasks", map[string]any{"note_id": r.v("p1")})
		r.call(c, "ai.draft", map[string]any{"prompt": "Write a status update"})
		hyb(r, c, map[string]any{"q": "runbook pods rollout"})
		r.call(c, "doc.ask", map[string]any{"question": "how do we restart the pods?"})
	}
	for _, c := range []string{"owner", "member", "other"} {
		for _, n := range []string{ph, r.v("pp"), r.v("rest"), r.v("long"), r.v("p3")} {
			r.call(c, "ai.summarize", map[string]any{"note_id": n})
			r.call(c, "ai.extract_tasks", map[string]any{"note_id": n})
			r.call(c, "ai.draft", map[string]any{"prompt": "  summarise this  ", "note_id": n})
		}
	}
	r.call("owner", "ai.draft", map[string]any{"prompt": "   "})
	r.call("owner", "ai.draft", map[string]any{"prompt": ""})
	r.call("owner", "ai.draft", map[string]any{"prompt": "ünï ✓ " + strings.Repeat("x", 5000), "note_id": nil})
	for _, body := range []string{`{}`, `[]`, `null`, `{"note_id":"x"}`, `{"note_id":null}`, `{"note_id":1}`, `{"prompt":1}`, `{"prompt":null}`, `{"prompt":"a","note_id":5}`} {
		for _, cmd := range []string{"ai.summarize", "ai.extract_tasks", "ai.draft", "search.hybrid", "doc.ask", "ai.set_provider", "ai.get_provider", "ai.clear_provider"} {
			r.raw("owner", cmd, body)
		}
	}
	for _, q := range []any{"", "   ", "ünï rollout", `"restart the" OR -pods`, "zzzzqqq nomatch", strings.Repeat("pods ", 400), "pods; drop table x", "restricted", "secret"} {
		for _, c := range []string{"owner", "member", "other"} {
			hyb(r, c, map[string]any{"q": q})
		}
	}
	for _, lim := range []any{0, -5, 1, 2, 50, 51, 100000, 9223372036854775807, nil, "10", 1.5, 9223372036854775808.0} {
		hyb(r, "owner", map[string]any{"q": "pods", "limit": lim})
	}
	r.raw("owner", "search.hybrid", `{"q":"pods","limit":9223372036854775808}`)
	r.raw("owner", "search.hybrid", `{"q":"pods","limit":1e2}`)
	for _, q := range []string{"what is the", "", "  ", "ab", "restart pods rollout", "restart pods rollout ünï", "nonexistentthing ünï", "deploy", "restricted body", "secret roadmap", "Standing meeting"} {
		for _, c := range []string{"owner", "member", "guest", "other"} {
			r.call(c, "doc.ask", map[string]any{"question": q})
		}
	}
	for _, src := range []string{"transcript", "chat", "pr", "email", ""} {
		r.call("owner", "doc.capture", map[string]any{"source": src, "text": "Decision: ship friday\nOwner: Asha", "space_id": sp})
	}
	for _, c := range aiCallers {
		r.call(c, "doc.capture", map[string]any{"source": "chat", "text": "first line\nsecond", "space_id": sp, "title": "  My capture  "})
	}
	r.call("owner", "doc.capture", map[string]any{"source": "pr", "text": "\n\n   " + strings.Repeat("ü", 120) + "\nrest", "space_id": sp})
	r.call("owner", "doc.capture", map[string]any{"source": "pr", "text": "   ", "space_id": sp})
	r.call("owner", "doc.capture", map[string]any{"source": "pr", "text": "x", "space_id": r.v("prv")})
	r.call("member", "doc.capture", map[string]any{"source": "pr", "text": "x", "space_id": r.v("prv")})
	r.call("owner", "doc.capture", map[string]any{"source": "pr", "text": "x", "space_id": ph})
	r.call("owner", "doc.capture", map[string]any{"source": "pr", "text": "x", "space_id": sp, "title": "   "})
	r.call("owner", "doc.capture", map[string]any{"source": "pr", "text": "x", "space_id": sp, "title": nil})
	e.settleDbg(ws)

	// ---- phase B: bring your own provider (the fake)
	for _, c := range aiCallers {
		r.call(c, "ai.set_provider", map[string]any{"provider": "openai", "base_url": url + "/v1", "api_key": key})
		r.call(c, "ai.clear_provider", map[string]any{})
	}
	for _, in := range []map[string]any{
		{"provider": "gemini"}, {"provider": ""}, {"provider": "openai", "base_url": "ftp://example.com"}, {"provider": "openai", "base_url": "nonsense"},
		{"provider": "openai", "base_url": "http://u:p@example.com/v1"}, {"provider": "openai", "base_url": "http://"}, {"provider": "openai", "base_url": "   "},
		{"provider": "openai", "base_url": "https://"}, {"provider": "anthropic", "base_url": "mailto:a@b.c"}, {"provider": "openai", "base_url": "http://:@host/v1"},
		{"provider": "openai", "base_url": url, "api_key": "   "}, {"provider": "openai", "base_url": nil, "model": nil},
		{"provider": "openai", "model": 7}, {"provider": "openai", "api_key": 7}, {"base_url": url},
	} {
		r.call("owner", "ai.set_provider", in)
	}
	r.call("owner", "ai.clear_provider", map[string]any{})
	r.call("owner", "ai.clear_provider", map[string]any{})
	r.call("owner", "ai.set_provider", map[string]any{"provider": "openai", "base_url": " " + url + "/v1/ ", "model": "m1", "embedding_model": "em", "api_key": " " + key + " "})
	r.call("owner", "ai.get_provider", map[string]any{})
	r.call("member", "ai.get_provider", map[string]any{})
	for _, c := range []string{"owner", "member", "guest", "other"} {
		r.call(c, "ai.summarize", map[string]any{"note_id": r.v("p1")})
	}
	r.call("owner", "ai.extract_tasks", map[string]any{"note_id": r.v("p1")})
	r.call("owner", "ai.extract_tasks", map[string]any{"note_id": r.v("p3")})
	r.call("owner", "ai.draft", map[string]any{"prompt": "Write a status update", "note_id": r.v("p2")})
	for _, n := range []string{"pf", "pj", "ps"} {
		r.call("owner", "ai.summarize", map[string]any{"note_id": r.v(n)})
	}
	hyb(r, "owner", map[string]any{"q": "QRY runbook pods"})
	hyb(r, "owner", map[string]any{"q": "QRY ünï"})
	r.call("member", "doc.ask", map[string]any{"question": "how do we restart the pods?"}) // process provider, not the workspace's
	// a stored key follows only the endpoint it was given for
	r.call("owner", "ai.set_provider", map[string]any{"provider": "openai", "base_url": url + "/v1/", "model": "m2"})
	r.call("owner", "ai.set_provider", map[string]any{"provider": "openai", "base_url": url + "/v1/", "model": "m2", "api_key": key})
	r.call("owner", "ai.set_provider", map[string]any{"provider": "openai", "base_url": url + "/v1/"})
	r.call("owner", "ai.set_provider", map[string]any{"provider": "openai", "base_url": url + "/v1"})
	r.call("owner", "ai.summarize", map[string]any{"note_id": r.v("p2")})
	r.call("owner", "ai.set_provider", map[string]any{"provider": "anthropic", "base_url": url, "api_key": key})
	r.call("owner", "ai.summarize", map[string]any{"note_id": r.v("p1")})
	r.call("owner", "ai.extract_tasks", map[string]any{"note_id": r.v("p1")})
	r.call("owner", "ai.draft", map[string]any{"prompt": "hello", "note_id": r.v("p2")})
	hyb(r, "owner", map[string]any{"q": "pods rollout"})
	r.call("owner", "ai.set_provider", map[string]any{"provider": "anthropic", "base_url": url, "api_key": key, "model": "claude-x"})
	r.call("owner", "ai.set_provider", map[string]any{"provider": "openai"}) // default endpoint is public https: only checked when set
	r.call("owner", "ai.set_provider", map[string]any{"provider": "openai", "base_url": url + "/v1", "api_key": key})
	// PII redaction
	adminSQL(e, ws, `update core.workspaces set settings = settings || '{"ai_redact_pii": true}'::jsonb where id = $1::uuid`, ws)
	r.call("owner", "ai.get_provider", map[string]any{})
	r.call("owner", "ai.summarize", map[string]any{"note_id": r.v("p1")})
	r.call("owner", "ai.draft", map[string]any{"prompt": "mail asha.k@example.co.in or +91 98765 43210 re PAN ABCDE1234F"})
	hyb(r, "owner", map[string]any{"q": "QRY asha.k@example.co.in 9876543210"})
	r.call("owner", "ai.clear_provider", map[string]any{})
	r.call("owner", "ai.get_provider", map[string]any{})
	r.call("owner", "ai.summarize", map[string]any{"note_id": r.v("p1")}) // local again: redaction is skipped
	adminSQL(e, ws, `update core.workspaces set settings = settings - 'ai_redact_pii' where id = $1::uuid`, ws)
	r.call("owner", "ai.get_provider", map[string]any{})
}

func hooksScript(r *runner, fk *fake, token string) (sigOK []bool) {
	ws := r.f.owner.ws
	r.e.settleDbg(ws) // pending event dispatches must not reach hooks created below
	url := fk.srv.URL
	ph := "00000000-0000-4000-8000-000000000001"
	far := "2099-01-01T00:00:00Z"
	mk := func(extra map[string]any) map[string]any {
		in := map[string]any{"name": "hook", "target": "http", "url": url + "/hook", "events": []string{"zz."}}
		for k, v := range extra {
			if v == nil {
				delete(in, k)
			} else {
				in[k] = v
			}
		}
		return in
	}
	for _, c := range aiCallers {
		r.call(c, "hook.list", map[string]any{})
		r.call(c, "hook.create", mk(map[string]any{"name": "by " + c}))
		r.call(c, "hook.delete", map[string]any{"id": ph})
	}
	for _, in := range []map[string]any{
		mk(map[string]any{"name": ""}), mk(map[string]any{"name": "   "}), mk(map[string]any{"name": strings.Repeat("n", 81)}), mk(map[string]any{"name": strings.Repeat("ü", 41)}),
		mk(map[string]any{"events": []string{"UP"}}), mk(map[string]any{"events": []string{""}}), mk(map[string]any{"events": []string{strings.Repeat("a", 61)}}),
		mk(map[string]any{"events": make([]string, 21)}), mk(map[string]any{"events": []string{}}), mk(map[string]any{"events": []string{}, "every_minutes": 4}),
		mk(map[string]any{"events": []string{}, "every_minutes": 5, "start_at": far}), mk(map[string]any{"every_minutes": 525601}), mk(map[string]any{"every_minutes": 525600, "start_at": far}),
		mk(map[string]any{"every_minutes": 5, "start_at": "2099-01-01T00:00:00.123456+05:30"}), mk(map[string]any{"every_minutes": 5, "start_at": "tomorrow"}),
		mk(map[string]any{"every_minutes": 60}), mk(map[string]any{"target": "ftp"}), mk(map[string]any{"target": "http", "tool": "x"}), mk(map[string]any{"token": "t"}),
		mk(map[string]any{"arguments": map[string]any{"a": 1}}), mk(map[string]any{"target": "mcp"}), mk(map[string]any{"target": "mcp", "tool": "  "}),
		mk(map[string]any{"target": "mcp", "tool": "ok", "arguments": []any{1}}), mk(map[string]any{"target": "mcp", "tool": "ok", "arguments": "x"}),
		mk(map[string]any{"url": "ftp://x"}), mk(map[string]any{"url": "nonsense"}), mk(map[string]any{"url": "http://u:p@h/"}), mk(map[string]any{"url": "http://"}),
		mk(map[string]any{"events": nil}), mk(map[string]any{"name": nil}), mk(map[string]any{"url": 5}), mk(map[string]any{"every_minutes": 5.5}),
		mk(map[string]any{"events": []string{"note.", "item.created", "a_b.*"}, "name": "  padded  "}),
	} {
		r.call("owner", "hook.create", in)
	}
	// every later event would otherwise be delivered to a "*" hook by the Rust worker, at a time that differs per side
	r.id("all", "owner", "hook.create", mk(map[string]any{"events": []string{"*"}, "name": "all events"}))
	r.call("owner", "hook.delete", map[string]any{"id": r.v("all")})
	r.raw("owner", "hook.create", `{"name":"x","target":"http","url":"http://127.0.0.1:1","events":null}`)
	r.raw("owner", "hook.create", `{"name":"x","target":"mcp","url":"http://127.0.0.1:1","tool":"t","arguments":1.5}`)
	r.raw("owner", "hook.create", `{}`)
	r.raw("owner", "hook.create", `[]`)

	var secret string
	mkHook := func(name string, in map[string]any) string {
		out := r.call("owner", "hook.create", in)
		r.vars[name], _ = out["id"].(string)
		if s, _ := out["signing_secret"].(string); s != "" {
			secret = s
		}
		return secret
	}
	httpSecret := mkHook("h1", mk(map[string]any{"name": "http ok", "every_minutes": 60, "start_at": far}))
	mkHook("h500", mk(map[string]any{"name": "http 500", "url": url + "/hook500"}))
	mkHook("hdead", mk(map[string]any{"name": "http dead", "url": "http://127.0.0.1:1/x"}))
	mkHook("hm", mk(map[string]any{"name": "mcp ok", "target": "mcp", "url": url + "/mcp", "tool": "ok", "arguments": map[string]any{"q": "note", "n": 3}, "token": token, "events": nil, "every_minutes": 60, "start_at": far}))
	mkHook("hmbad", mk(map[string]any{"name": "mcp bad token", "target": "mcp", "url": url + "/mcp", "tool": "ok", "token": "wrong", "events": nil, "every_minutes": 60, "start_at": far}))
	mkHook("hmboom", mk(map[string]any{"name": "mcp tool error", "target": "mcp", "url": url + "/mcp", "tool": "boom", "token": token}))
	mkHook("hmnotok", mk(map[string]any{"name": "mcp no token", "target": "mcp", "url": url + "/mcp", "tool": "ok"}))
	for _, c := range aiCallers {
		r.call(c, "hook.test", map[string]any{"id": r.v("h1")})
	}
	r.call("owner", "hook.test", map[string]any{"id": r.v("h1")})
	for _, n := range []string{"h500", "hdead", "hm", "hmbad", "hmboom", "hmnotok"} {
		r.call("owner", "hook.test", map[string]any{"id": r.v(n)})
	}
	r.call("owner", "hook.test", map[string]any{"id": ph})
	r.call("other", "hook.test", map[string]any{"id": r.v("h1")})
	r.call("owner", "hook.set_enabled", map[string]any{"id": r.v("h1"), "enabled": false})
	r.call("owner", "hook.test", map[string]any{"id": r.v("h1")}) // paused: no delivery
	r.call("owner", "hook.set_enabled", map[string]any{"id": r.v("h1"), "enabled": true})
	r.call("owner", "hook.set_enabled", map[string]any{"id": ph, "enabled": true})
	r.call("member", "hook.set_enabled", map[string]any{"id": r.v("h1"), "enabled": true})
	r.call("other", "hook.set_enabled", map[string]any{"id": r.v("h1"), "enabled": false})
	r.raw("owner", "hook.set_enabled", `{"id":"x","enabled":true}`)
	r.raw("owner", "hook.set_enabled", `{"id":"`+r.v("h1")+`"}`)
	r.raw("owner", "hook.set_enabled", `{"id":"`+r.v("h1")+`","enabled":"yes"}`)
	r.raw("owner", "hook.test", `{}`)
	r.raw("owner", "hook.delete", `{"id":5}`)
	r.call("owner", "hook.list", map[string]any{})
	r.call("owner", "hook.delete", map[string]any{"id": r.v("hmnotok")})
	r.call("owner", "hook.delete", map[string]any{"id": r.v("hmnotok")})
	r.call("other", "hook.delete", map[string]any{"id": r.v("h1")})
	r.call("owner", "hook.list", map[string]any{})
	fk.mu.Lock()
	defer fk.mu.Unlock()
	for _, h := range fk.hooks {
		if h.Path != "/hook" { // the other hooks have their own secrets
			continue
		}
		var ts int64
		fmt.Sscan(h.TS, &ts)
		sigOK = append(sigOK, ai.Signature(httpSecret, ts, h.Body) == h.Sig)
	}
	return
}

func (e *env) dumpRows(r *runner, qs map[string]string) []string {
	e.t.Helper()
	tx, err := dbTenant(e, r.f.owner.ws)
	if err != nil {
		e.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var names []string
	for n := range qs {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []string
	for _, n := range names {
		rows, err := tx.Query(context.Background(), qs[n])
		if err != nil {
			e.t.Fatalf("%s: %v", n, err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				e.t.Fatal(err)
			}
			out = append(out, n+" "+r.norm(s))
		}
		rows.Close()
	}
	return out
}

var aiDump = map[string]string{
	"audit": `select (to_jsonb(a) - 'id' - 'created_at')::text from ai.audit a order by a.purpose, a.prompt::text, a.response::text, exists (select 1 from core.agents g where g.id::text = a.actor_id),
		(select m.role from core.memberships m where m.user_id::text = a.actor_id), a.created_at`,
	"config": `select (to_jsonb(c) - 'key_enc' - 'updated_at')::text || ' key=' || (c.key_enc is not null)::text from ai.provider_config c`,
	"usage":  `select kind || ' ' || quantity || ' ' || meta::text from core.usage where kind like 'ai.%' order by 1`,
	"events": `select (to_jsonb(x) - 'id')::text from core.events x where x.actor_type in ('user','agent') and x.kind <> 'command.denied' order by x.id`,
	"caps":   `select to_jsonb(c)::text from docs.capture_requests c order by c.question, c.created_at`,
	"notes":  `select (to_jsonb(n) - 'search' - 'content_ydoc')::text from core.notes n order by n.title, n.created_at`,
	"hooks":  `select (to_jsonb(h) - 'secret_enc' - 'last_run_at')::text || ' secret=' || (h.secret_enc is not null)::text from core.hooks h order by h.name, h.created_at`,
}

func compareTranscripts(t *testing.T, rust, gov []string, minOK, minErr int) {
	t.Helper()
	fix := func(l string) string {
		return secretTok.ReplaceAllString(fakeTok.ReplaceAllString(l, "FAKE"), "SECRET")
	}
	if len(rust) != len(gov) {
		seen := map[string]int{}
		for _, l := range rust {
			seen[fix(l)]++
		}
		for _, l := range gov {
			seen[fix(l)]--
		}
		for l, n := range seen {
			if n != 0 {
				t.Logf("%+d %.600s", n, l)
			}
		}
		t.Fatalf("transcript lengths differ: rust %d go %d", len(rust), len(gov))
	}
	bad, ok2xx, errs := 0, 0, 0
	for i := range rust {
		a, b := fix(rust[i]), fix(gov[i])
		if a != b {
			if bad++; bad <= 15 {
				t.Errorf("step %d differs\n rust %.900s\n go   %.900s", i, a, b)
			}
		}
		if strings.Contains(a, "-> 200 ") {
			ok2xx++
		} else if strings.Contains(a, " -> 4") {
			errs++
		}
	}
	if ok2xx < minOK || errs < minErr {
		t.Fatalf("script not rich enough: %d ok, %d errors", ok2xx, errs)
	}
	t.Logf("compared %d steps and rows (%d ok responses, %d 4xx); %d differ", len(rust), ok2xx, errs, bad)
}

// sameHits compares two search.hybrid bodies ignoring order and score: chunks with equal vector distance tie, and
// Postgres may rank ties differently from one execution to the next (in Rust alone as well), which moves scores.
func sameHits(a, b []byte) bool {
	norm := func(x []byte) string {
		var hits []map[string]any
		if json.Unmarshal(x, &hits) != nil {
			return string(x)
		}
		var out []string
		for _, h := range hits {
			if sc, _ := h["score"].(float64); sc <= 0 || sc > 1 {
				return "bad score"
			}
			delete(h, "score")
			j, _ := json.Marshal(h)
			out = append(out, string(j))
		}
		sort.Strings(out)
		return strings.Join(out, "\n")
	}
	return norm(a) == norm(b)
}

func fakeLog(f *fake) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, q := range f.reqs {
		var v any
		_ = json.Unmarshal([]byte(q.Body), &v)
		b, _ := json.Marshal(v)
		out = append(out, normBody(fmt.Sprintf("%s auth=%s key=%s ver=%s %s", q.Path, q.Auth, q.Key, q.Version, b)))
	}
	for _, m := range f.mcp {
		out = append(out, normBody("mcp "+m))
	}
	for _, h := range f.hooks {
		var v any
		_ = json.Unmarshal([]byte(h.Body), &v)
		b, _ := json.Marshal(v)
		out = append(out, normBody("hook "+h.Path+" "+string(b)+" ts="+fmt.Sprint(h.TS != "")))
	}
	return out
}

func TestDifferentialAI(t *testing.T) {
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "1") // the fake provider is on 127.0.0.1; the shared Rust server has the same setting
	e := newEnv(t)
	key, token := "k-"+rnd()+rnd(), "t-"+rnd()
	var sigs [][]bool
	var logs [][]string
	run := func(base string) *runner {
		fk := newFake(t, token)
		r := &runner{e: e, base: base, f: e.fixture(), vars: map[string]string{}, toks: map[string]string{}}
		aiScript(r, fk, key)
		sigs = append(sigs, hooksScript(r, fk, token))
		e.settle(r.f.owner.ws)
		r.transcript = append(r.transcript, e.dumpRows(r, aiDump)...)
		logs = append(logs, fakeLog(fk))
		return r
	}
	rust, gov := run(e.rust), run(e.gosv.URL)
	compareTranscripts(t, rust.transcript, gov.transcript, 150, 150)
	if len(logs[0]) != len(logs[1]) {
		t.Fatalf("fake saw %d requests from Rust and %d from Go", len(logs[0]), len(logs[1]))
	}
	for i := range logs[0] {
		a, b := fakeTok.ReplaceAllString(logs[0][i], "FAKE"), fakeTok.ReplaceAllString(logs[1][i], "FAKE")
		if a != b {
			t.Errorf("fake request %d differs\n rust %.600s\n go   %.600s", i, a, b)
		}
	}
	for side, s := range sigs {
		if len(s) == 0 {
			t.Fatalf("side %d: no hook was delivered", side)
		}
		for _, ok := range s {
			if !ok {
				t.Errorf("side %d: a hook signature did not verify", side)
			}
		}
	}
	for _, r := range []*runner{rust, gov} {
		n := 0
		for _, cn := range aiCallers {
			for _, q := range []any{map[string]any{"q": "runbook pods rollout", "limit": 50}, map[string]any{"q": "ünï rollout", "limit": 50}, map[string]any{"q": "pods", "limit": 50},
				map[string]any{"q": "secret", "limit": 50}, map[string]any{"q": "zzzz qqq", "limit": 50}, map[string]any{"q": "restricted pods", "limit": 50}, map[string]any{"q": " "}, map[string]any{}} {
				time.Sleep(55 * time.Millisecond)
				g := e.cmd(e.gosv.URL, r.f.who(cn), r.f.owner.ws, "search.hybrid", q, nil)
				ru := e.cmd(e.rust, r.f.who(cn), r.f.owner.ws, "search.hybrid", q, nil)
				n++
				if g.status != ru.status || (string(g.body) != string(ru.body) && !sameHits(g.body, ru.body)) {
					t.Errorf("%s search.hybrid %v\n go   %d %.700s\n rust %d %.700s", cn, q, g.status, g.body, ru.status, ru.body)
				}
			}
		}
		t.Logf("raw-compared %d search.hybrid pairs", n)
	}
	paths := map[string]int{}
	for _, l := range logs[1] {
		paths[strings.SplitN(l, " ", 2)[0]]++
	}
	if paths["/v1/chat/completions"] < 8 || paths["/v1/messages"] < 3 || paths["/v1/embeddings"] < 3 || paths["mcp"] < 2 || paths["hook"] < 2 {
		t.Fatalf("the fake saw too little: %v", paths)
	}
	t.Logf("fake saw %d requests per side: %v", len(logs[0]), paths)
}

// settleDbg is settle with a report of the stuck jobs when the worker does not drain (and a longer wait: the shared
// Rust worker also serves other agents' tests).
func (e *env) settleDbg(ws string) {
	e.t.Helper()
	for i := 0; i < 1500; i++ {
		if e.dbStrings(ws, "select count(*)::text from core.jobs where status in ('queued','running')")[0] == "0" {
			time.Sleep(200 * time.Millisecond)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.t.Fatalf("worker did not drain: %v", e.dbStrings(ws, "select kind || ' ' || status || ' ' || run_at || ' ' || coalesce(last_error, '') from core.jobs where status in ('queued','running') limit 10"))
}
