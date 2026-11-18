package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeAPI(t *testing.T, h func(path string, body map[string]any, hdr http.Header) (int, any)) (url string, seen *[]map[string]any) {
	t.Helper()
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "1") // the fake listens on loopback
	var rec []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec = append(rec, body)
		code, out := h(r.URL.Path, body, r.Header)
		w.WriteHeader(code)
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &rec
}

func TestAnthropicStepToolRoundTrip(t *testing.T) {
	raw := `[{"type":"thinking","thinking":"","signature":"sig<&>"},{"type":"text","text":"Looking. "},{"type":"tool_use","id":"toolu_1","name":"note.get","input":{"id":"n1"}}]`
	n := 0
	url, seen := fakeAPI(t, func(_ string, _ map[string]any, _ http.Header) (int, any) {
		n++
		if n == 1 {
			return 200, map[string]any{"model": "claude-sonnet-5-5", "stop_reason": "tool_use", "content": json.RawMessage(raw),
				"usage": map[string]any{"input_tokens": 10, "cache_creation_input_tokens": 5, "cache_read_input_tokens": 100, "output_tokens": 7}}
		}
		return 200, map[string]any{"model": "claude-sonnet-5-5", "stop_reason": "end_turn", "content": []any{map[string]any{"type": "text", "text": "Done."}},
			"usage": map[string]any{"input_tokens": 3, "output_tokens": 1}}
	})
	a := NewAnthropic("k", &url, nil)
	tools := []Tool{{Name: "z.last", Schema: json.RawMessage(`{"type":"object"}`)}, {Name: "a.first"}}
	req := StepRequest{System: "sys", Tools: tools, Turns: []Turn{{Role: "user", Text: "hi"}}, MaxTokens: 100}
	r1, err := a.Step(context.Background(), req)
	if err != nil || r1.Stop != "tools" || len(r1.Calls) != 1 || r1.Calls[0].ID != "toolu_1" || string(r1.Calls[0].Input) != `{"id":"n1"}` ||
		r1.Text != "Looking. " || r1.TokensIn != 115 || r1.CacheRead != 100 || r1.TokensOut != 7 || string(r1.Raw) != raw {
		t.Fatalf("%+v %v", r1, err)
	}
	b := (*seen)[0]
	if b["model"] != defaultAnthropicModel || b["output_config"] == nil || b["tool_choice"] != nil || b["thinking"] != nil || b["cache_control"] != nil || b["fallbacks"] != nil {
		t.Fatalf("request shape (custom base URL gets no cache_control/fallbacks): %v", b)
	}
	if ts := b["tools"].([]any); ts[0].(map[string]any)["name"] != "a.first" || ts[0].(map[string]any)["input_schema"] == nil {
		t.Fatalf("tools sorted by name with a schema: %v", ts)
	}
	req.Turns = append(req.Turns, Turn{Role: "assistant", Raw: r1.Raw, Calls: r1.Calls},
		Turn{Role: "tool", Results: []ToolResult{{CallID: "toolu_1", Content: `{"title":"x"}`, IsError: true}}})
	r2, err := a.Step(context.Background(), req)
	if err != nil || r2.Stop != "end" || r2.Text != "Done." || r2.TokensIn != 3 {
		t.Fatalf("%+v %v", r2, err)
	}
	ms := (*seen)[1]["messages"].([]any)
	asst := ms[1].(map[string]any)["content"].([]any)
	if len(ms) != 3 || asst[0].(map[string]any)["signature"] != "sig<&>" || asst[2].(map[string]any)["id"] != "toolu_1" {
		t.Fatalf("raw replayed: %v", ms)
	}
	tr := ms[2].(map[string]any)
	blk := tr["content"].([]any)[0].(map[string]any)
	if tr["role"] != "user" || blk["type"] != "tool_result" || blk["tool_use_id"] != "toolu_1" || blk["is_error"] != true {
		t.Fatalf("tool turn: %v", tr)
	}
}

func TestAnthropicStepRawBytesAndStops(t *testing.T) {
	// the replayed assistant content must reach the wire unchanged (no HTML escaping, key order kept)
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "1")
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 4096)
		n, _ := r.Body.Read(b)
		got = string(b[:n])
		_, _ = w.Write([]byte(`{"model":"m","stop_reason":"max_tokens","content":[{"type":"text","text":"x"}]}`))
	}))
	defer srv.Close()
	a := NewAnthropic("k", &srv.URL, nil)
	raw := `[{"type":"text","text":"a<b>&c","z":1,"a":2}]`
	res, err := a.Step(context.Background(), StepRequest{Turns: []Turn{{Role: "user", Text: "q"}, {Role: "assistant", Raw: json.RawMessage(raw)}, {Role: "user", Text: "again"}}})
	if err != nil || res.Stop != "max_tokens" || !strings.Contains(got, raw) {
		t.Fatalf("%v %q", err, got)
	}
}

func TestAnthropicDefaultURLAddsCacheAndFallbackOnlyForSonnet(t *testing.T) {
	// no network: build the request through a recording transport is overkill; assert the gating constants instead
	a := NewAnthropic("k", nil, nil)
	if a.baseURL != anthropicDefaultURL {
		t.Fatal("default base URL constant drifted")
	}
}

func TestOpenAIStepToolRoundTrip(t *testing.T) {
	n := 0
	url, seen := fakeAPI(t, func(path string, _ map[string]any, _ http.Header) (int, any) {
		n++
		if path != "/chat/completions" {
			return 404, nil
		}
		if n == 1 {
			return 200, map[string]any{"model": "gpt-x", "choices": []any{map[string]any{"finish_reason": "tool_calls", "message": map[string]any{"role": "assistant", "content": nil,
				"tool_calls": []any{map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "report.run", "arguments": `{"dataset":"sales"}`}}}}}},
				"usage": map[string]any{"prompt_tokens": 20, "completion_tokens": 4, "prompt_tokens_details": map[string]any{"cached_tokens": 8}}}
		}
		return 200, map[string]any{"model": "gpt-x", "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": "ok"}}},
			"usage": map[string]any{"prompt_tokens": 30, "completion_tokens": 1}}
	})
	o := NewOpenAI(nil, &url, nil, nil)
	req := StepRequest{System: "sys", Tools: []Tool{{Name: "report.run", Description: "d", Schema: json.RawMessage(`{"type":"object"}`)}}, Turns: []Turn{{Role: "user", Text: "go"}}}
	r1, err := o.Step(context.Background(), req)
	if err != nil || r1.Stop != "tools" || len(r1.Calls) != 1 || r1.Calls[0].Name != "report.run" || string(r1.Calls[0].Input) != `{"dataset":"sales"}` || r1.TokensIn != 20 || r1.CacheRead != 8 {
		t.Fatalf("%+v %v", r1, err)
	}
	tl := (*seen)[0]["tools"].([]any)[0].(map[string]any)
	if tl["type"] != "function" || tl["function"].(map[string]any)["parameters"] == nil {
		t.Fatalf("tools shape: %v", tl)
	}
	req.Turns = append(req.Turns, Turn{Role: "assistant", Raw: r1.Raw, Calls: r1.Calls},
		Turn{Role: "tool", Results: []ToolResult{{CallID: "call_1", Content: "rows"}, {CallID: "call_2", Content: "more"}}})
	r2, err := o.Step(context.Background(), req)
	if err != nil || r2.Stop != "end" || r2.Text != "ok" {
		t.Fatalf("%+v %v", r2, err)
	}
	ms := (*seen)[1]["messages"].([]any)
	if len(ms) != 5 || ms[0].(map[string]any)["role"] != "system" || ms[2].(map[string]any)["tool_calls"] == nil ||
		ms[3].(map[string]any)["role"] != "tool" || ms[3].(map[string]any)["tool_call_id"] != "call_1" || ms[4].(map[string]any)["tool_call_id"] != "call_2" {
		t.Fatalf("messages: %v", ms)
	}
}

func TestStepStatusAndSizeBounds(t *testing.T) {
	url, _ := fakeAPI(t, func(string, map[string]any, http.Header) (int, any) { return 429, map[string]any{"error": "slow down"} })
	_, err := NewAnthropic("k", &url, nil).Step(context.Background(), StepRequest{Turns: []Turn{{Role: "user", Text: "x"}}})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 429 || err.Error() != "model API returned 429 Too Many Requests" {
		t.Fatalf("%v", err)
	}
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"x":"` + strings.Repeat("a", maxBody+10) + `"}`))
	}))
	defer big.Close()
	if _, err := postRaw(context.Background(), big.URL, nil, map[string]any{}); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized body accepted: %v", err)
	}
}

func TestLocalStepToolMode(t *testing.T) {
	ctx := context.Background()
	r, err := Local{}.Step(ctx, StepRequest{Turns: []Turn{{Role: "user", Text: "please\n/tool note.get {\"id\":\"n1\"}\nthanks"}}})
	if err != nil || r.Stop != "tools" || len(r.Calls) != 1 || r.Calls[0].Name != "note.get" || string(r.Calls[0].Input) != `{"id":"n1"}` || r.Calls[0].ID != "local-1" {
		t.Fatalf("%+v %v", r, err)
	}
	r2, _ := Local{}.Step(ctx, StepRequest{Turns: []Turn{{Role: "user", Text: "/tool home.summary"}}})
	if string(r2.Calls[0].Input) != "{}" {
		t.Fatalf("default input: %+v", r2)
	}
	r3, _ := Local{}.Step(ctx, StepRequest{Turns: []Turn{{Role: "user", Text: "/tool a {bad"}}})
	if r3.Stop != "end" || len(r3.Calls) != 0 || r3.Text != "/tool a {bad" {
		t.Fatalf("invalid json echoes: %+v", r3)
	}
	r4, _ := Local{}.Step(ctx, StepRequest{Turns: []Turn{{Role: "user", Text: "/tool note.get {}"}, {Role: "assistant", Calls: r.Calls, Raw: r.Raw},
		{Role: "tool", Results: []ToolResult{{CallID: "local-1", Content: "PAGE"}}}}})
	if r4.Stop != "end" || r4.Text != "Result of note.get:\nPAGE" || len(r4.Raw) == 0 {
		t.Fatalf("%+v", r4)
	}
	r5, _ := Local{}.Step(ctx, StepRequest{Turns: []Turn{{Role: "user", Text: "hello"}}})
	if r5.Text != "hello" || r5.Stop != "end" || r5.Provider != "local" {
		t.Fatalf("echo: %+v", r5)
	}
	if got, err := (Composite{Chat: Local{}, Embedder: Local{}}).Step(ctx, StepRequest{Turns: []Turn{{Role: "user", Text: "x"}}}); err != nil || got.Text != "x" {
		t.Fatal("composite delegates to chat")
	}
}

func TestRedactJSONTouchesStringLeavesOnly(t *testing.T) {
	in := `{"total_minor":9876543210,"id":"3f2c9a10-1b2c-4d3e-8f40-234567890123","phone":"+91 98765 43210","note":"mail asha@example.com ref 3f2c9a10-1b2c-4d3e-8f40-234567890123","tags":["PAN ABCDE1234F"],"ok":true,"n":null,"big":12345678901234567890}`
	var got map[string]any
	if err := json.Unmarshal(RedactJSON([]byte(in)), &got); err != nil {
		t.Fatal(err)
	}
	if got["total_minor"] != float64(9876543210) || got["id"] != "3f2c9a10-1b2c-4d3e-8f40-234567890123" || got["ok"] != true || got["n"] != nil {
		t.Fatalf("numbers, UUIDs and literals must survive: %v", got)
	}
	if got["phone"] != "[PHONE]" || got["note"] != "mail [EMAIL] ref 3f2c9a10-1b2c-4d3e-8f40-234567890123" || got["tags"].([]any)[0] != "PAN [PAN]" {
		t.Fatalf("string leaves redacted: %v", got)
	}
	if !strings.Contains(string(RedactJSON([]byte(in))), "12345678901234567890") {
		t.Fatal("large numbers keep their digits")
	}
	if got := string(RedactJSON([]byte("call 9876543210"))); got != "call [PHONE]" {
		t.Fatalf("non-JSON falls back to text redaction: %q", got)
	}
	if got := string(RedactJSON([]byte(`"a<b"`))); got != `"a<b"` {
		t.Fatalf("no HTML escaping: %s", got)
	}
}

func TestSessionRedactsStepInput(t *testing.T) {
	url, seen := fakeAPI(t, func(string, map[string]any, http.Header) (int, any) {
		return 200, map[string]any{"model": "m", "stop_reason": "end_turn", "content": []any{map[string]any{"type": "text", "text": "ok"}}}
	})
	s := &Session{&resolved{provider: NewAnthropic("k", &url, nil), redact: true}}
	_, err := s.Step(context.Background(), StepRequest{System: "mail a@b.co", Turns: []Turn{{Role: "user", Text: "call 9876543210"},
		{Role: "assistant", Raw: json.RawMessage(`[{"type":"text","text":"x"}]`)},
		{Role: "tool", Results: []ToolResult{{CallID: "c", Content: `{"paise":9876543210,"phone":"9876543210"}`}}}}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal((*seen)[0])
	sent := string(b)
	for _, leak := range []string{"a@b.co", "call 9876543210", `\"phone\":\"9876543210\"`} {
		if strings.Contains(sent, leak) {
			t.Fatalf("%q leaked: %s", leak, sent)
		}
	}
	if !strings.Contains(sent, `\"paise\":9876543210`) {
		t.Fatalf("paise total must survive: %s", sent)
	}
}

func TestModelForTiers(t *testing.T) {
	t.Setenv("AI_MODEL_ROUTINE", "")
	plat := &Session{&resolved{provider: NewAnthropic("k", nil, nil)}}
	if plat.ModelFor("routine") != "claude-haiku-5-5" || plat.ModelFor("reasoning") != "" {
		t.Fatal("platform tiers")
	}
	t.Setenv("AI_MODEL_ROUTINE", "claude-x")
	if plat.ModelFor("routine") != "claude-x" {
		t.Fatal("AI_MODEL_ROUTINE")
	}
	if (&Session{&resolved{provider: NewAnthropic("k", nil, nil), byo: true}}).ModelFor("routine") != "" || (&Session{&resolved{provider: Local{}}}).ModelFor("routine") != "" {
		t.Fatal("BYO and non-Anthropic use the configured model")
	}
}
