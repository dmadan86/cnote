package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// doc.ask goes through ai.Complete: a workspace with its own key uses it, the call is audited and not metered.
func TestDocsAskUsesWorkspaceProviderAndAudits(t *testing.T) {
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "1")
	var hits atomic.Int32
	var auth atomic.Value
	var broken atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if broken.Load() {
			w.WriteHeader(502)
			return
		}
		hits.Add(1)
		auth.Store(r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "byo-m", "choices": []any{map[string]any{"message": map[string]any{"content": "From the workspace model."}}},
			"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 3}})
	}))
	defer srv.Close()
	d, owner := newDG(t)
	space := d.space(owner, "ENG", "open")
	d.page(owner, space, "Pod runbook", nil, "Restart kubernetes pods with the rollout command")
	d.ok(owner, "ai.set_provider", map[string]any{"provider": "openai", "base_url": srv.URL, "api_key": "byo-key-123"})
	a := d.ok(owner, "doc.ask", map[string]any{"question": "How do I restart kubernetes pods?"})
	if a["answered"] != true || a["answer"] != "From the workspace model." {
		t.Fatalf("ask: %v", a)
	}
	if hits.Load() != 1 || auth.Load() != "Bearer byo-key-123" {
		t.Fatalf("workspace key not used: %d %v", hits.Load(), auth.Load())
	}
	if n := d.scalar("select count(*)::text from ai.audit where purpose = 'answer' and tokens_in = 5"); n != "1" {
		t.Fatalf("audit rows: %s", n)
	}
	if n := d.scalar("select count(*)::text from core.usage where kind like 'ai.%'"); n != "0" {
		t.Fatalf("BYO is never metered: %s", n)
	}
	// a broken workspace endpoint falls back to the extractive answer; the call is still safe
	broken.Store(true)
	b := d.ok(owner, "doc.ask", map[string]any{"question": "How do I restart kubernetes pods?"})
	if b["answered"] != true || !strings.Contains(b["answer"].(string), "kubernetes") {
		t.Fatalf("extractive fallback: %v", b)
	}
}
