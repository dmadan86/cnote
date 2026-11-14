package hooks

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/secretbox"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func rnd() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func rustURL() string {
	if u := strings.TrimRight(os.Getenv("RUST_API_URL"), "/"); u != "" {
		return u
	}
	return "http://localhost:4000"
}

type fx struct {
	t      *testing.T
	pool   *pgxpool.Pool
	ws     string
	cookie string
	ctx    *command.Ctx
}

// setup signs up through Rust (fixture) and opens ONE tenant tx that is always rolled back: the Rust worker
// claims any committed queued job, so nothing here may commit.
func setup(t *testing.T) *fx {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Fatal("DATABASE_URL is required (load the repo .env)")
	}
	if os.Getenv("CNOTE_MASTER_KEY") == "" {
		k := make([]byte, 32)
		_, _ = rand.Read(k)
		t.Setenv("CNOTE_MASTER_KEY", base64.StdEncoding.EncodeToString(k))
	}
	pool, err := db.Connect(context.Background(), url, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	b, _ := json.Marshal(map[string]any{"email": "go-" + rnd() + "@example.test", "name": "Hook", "password": rnd() + rnd(), "workspace_name": "Hooks WS"})
	res, err := http.Post(rustURL()+"/api/auth/signup", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		WorkspaceID string `json:"workspace_id"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	f := &fx{t: t, pool: pool, ws: out.WorkspaceID}
	for _, c := range res.Cookies() {
		if c.Name == "cn_session" {
			f.cookie = c.Value
		}
	}
	if res.StatusCode != 200 || f.ws == "" {
		t.Fatalf("signup failed: %d", res.StatusCode)
	}
	tx, err := db.BeginTenant(context.Background(), pool, f.ws)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	f.ctx = &command.Ctx{Context: context.Background(), Tx: tx, Principal: command.Principal{Kind: command.KindSystem, WorkspaceID: f.ws, Role: command.Owner}}
	return f
}

type hookSpec struct {
	target, url, tool, secret string
	args                      string
	events                    []string
	every                     *int
	next                      *time.Time
	disabled                  bool
}

func (f *fx) hook(s hookSpec) string {
	f.t.Helper()
	var enc []byte
	if s.secret != "" {
		key, err := secretbox.MasterKey()
		if err != nil {
			f.t.Fatal(err)
		}
		if enc, err = secretbox.Encrypt(key, f.ws, s.secret); err != nil {
			f.t.Fatal(err)
		}
	}
	if s.target == "" {
		s.target = "http"
	}
	if s.args == "" {
		s.args = "{}"
	}
	var tool *string
	if s.tool != "" {
		tool = &s.tool
	}
	if s.events == nil {
		s.events = []string{}
	}
	var id string
	err := f.ctx.Tx.QueryRow(f.ctx.Context, `insert into core.hooks (workspace_id,name,target,url,tool,arguments,secret_enc,events,every_minutes,next_run_at,enabled)
		values ($1::uuid,'h',$2,$3,$4,$5::jsonb,$6,$7,$8,$9,$10) returning id::text`,
		f.ws, s.target, s.url, tool, s.args, enc, s.events, s.every, s.next, !s.disabled).Scan(&id)
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

type job struct {
	Payload json.RawMessage
	RunAt   time.Time
}

func (f *fx) jobs() []job {
	f.t.Helper()
	rows, err := f.ctx.Tx.Query(f.ctx.Context, "select payload, run_at from core.jobs where kind = 'hook.deliver' order by id")
	if err != nil {
		f.t.Fatal(err)
	}
	js, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (j job, err error) { err = r.Scan(&j.Payload, &j.RunAt); return })
	if err != nil {
		f.t.Fatal(err)
	}
	return js
}

type status struct {
	Status, Err *string
	RanAt       *time.Time
}

func (f *fx) status(id string) status {
	var s status
	if err := f.ctx.Tx.QueryRow(f.ctx.Context, "select last_status, last_error, last_run_at from core.hooks where id=$1::uuid", id).Scan(&s.Status, &s.Err, &s.RanAt); err != nil {
		f.t.Fatal(err)
	}
	return s
}

func (f *fx) deliver(id string, attempt int, body string) {
	f.t.Helper()
	p, _ := json.Marshal(map[string]any{"hook_id": id, "body": json.RawMessage(body), "attempt": attempt})
	if err := DeliverJob(f.ctx, p); err != nil {
		f.t.Fatal(err)
	}
}

func str(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

type hit struct {
	h    http.Header
	body string
}

func receiver(t *testing.T, code int) (string, func() []hit) {
	t.Helper()
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "1") // httptest listens on 127.0.0.1
	var mu sync.Mutex
	var hits []hit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		hits = append(hits, hit{r.Header.Clone(), string(b)})
		mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/hook", func() []hit { mu.Lock(); defer mu.Unlock(); return append([]hit(nil), hits...) }
}

func TestSignature(t *testing.T) {
	// HMAC-SHA256 key "k", message "1.{}" (same assertion as the Rust unit test, computed independently).
	m := Signature("k", 1, "{}")
	if !strings.HasPrefix(m, "sha256=") || len(m) != 7+64 {
		t.Fatal(m)
	}
	if m == Signature("k", 2, "{}") || m == Signature("j", 1, "{}") {
		t.Fatal("signature must depend on ts and key")
	}
}

func TestEventDeliveryBytesHeadersAndHMAC(t *testing.T) {
	f := setup(t)
	url, hits := receiver(t, 200)
	secret := "whsec_" + rnd()
	id := f.hook(hookSpec{url: url, secret: secret, events: []string{"note."}})
	note := "11111111-2222-3333-4444-555555555555"
	e := jobs.Event{ID: 7, WorkspaceID: f.ws, NoteID: &note, Kind: "note.created", ActorType: "user",
		Payload: json.RawMessage(`{"b":"x","a":{"z":1,"y":[2.5]}}`), CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)}
	if err := OnEvent(f.ctx, e); err != nil {
		t.Fatal(err)
	}
	js := f.jobs()
	if len(js) != 1 {
		t.Fatalf("jobs: %d", len(js))
	}
	var p map[string]any
	_ = json.Unmarshal(js[0].Payload, &p)
	if p["hook_id"] != id || p["attempt"].(float64) != 1 {
		t.Fatalf("payload %s", js[0].Payload)
	}
	if err := DeliverJob(f.ctx, js[0].Payload); err != nil {
		t.Fatal(err)
	}
	h := hits()
	if len(h) != 1 {
		t.Fatalf("hits %d", len(h))
	}
	want := `{"event":{"actor_id":null,"actor_type":"user","created_at":"2026-01-02T03:04:05.123456Z","id":7,"kind":"note.created",` +
		`"note_id":"` + note + `","on_behalf_of":null,"payload":{"a":{"y":[2.5],"z":1},"b":"x"},"workspace_id":"` + f.ws + `"},` +
		`"hook_id":"` + id + `","type":"event","workspace_id":"` + f.ws + `"}`
	if h[0].body != want {
		t.Fatalf("body\n got %s\nwant %s", h[0].body, want)
	}
	if h[0].h.Get("content-type") != "application/json" {
		t.Error("content-type")
	}
	ts := h[0].h.Get("x-cnote-timestamp")
	if d := time.Now().Unix() - mustInt(t, ts); d < 0 || d > 5 {
		t.Errorf("timestamp %s", ts)
	}
	if got := h[0].h.Get("x-cnote-signature"); got != Signature(secret, mustInt(t, ts), want) {
		t.Errorf("signature %s", got)
	}
	if s := f.status(id); str(s.Status) != "ok" || s.Err != nil || s.RanAt == nil {
		t.Errorf("status %+v", s)
	}
	if len(f.jobs()) != 1 {
		t.Error("success must not enqueue a retry")
	}
}

func mustInt(t *testing.T, s string) int64 {
	t.Helper()
	var n int64
	if err := json.Unmarshal([]byte(s), &n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestFailureRetriesWithBackoffAndStopsAtFive(t *testing.T) {
	f := setup(t)
	url, hits := receiver(t, 500)
	id := f.hook(hookSpec{url: url, secret: "whsec_" + rnd(), events: []string{"*"}})
	body := `{"type":"test"}`
	for attempt, mins := range map[int]int{1: 2, 2: 4, 4: 16} {
		before := len(f.jobs())
		f.deliver(id, attempt, body)
		js := f.jobs()
		if len(js) != before+1 {
			t.Fatalf("attempt %d: no retry", attempt)
		}
		last := js[len(js)-1]
		var p struct {
			Attempt int `json:"attempt"`
		}
		_ = json.Unmarshal(last.Payload, &p)
		if p.Attempt != attempt+1 {
			t.Errorf("attempt %d -> %d", attempt, p.Attempt)
		}
		if d := time.Until(last.RunAt) - time.Duration(mins)*time.Minute; d > 5*time.Second || d < -5*time.Second {
			t.Errorf("attempt %d run_at off by %v", attempt, d)
		}
	}
	if s := f.status(id); str(s.Status) != "failed" || str(s.Err) != "HTTP 500" {
		t.Errorf("status %s %s", str(s.Status), str(s.Err))
	}
	n := len(f.jobs())
	f.deliver(id, 5, body)
	if len(f.jobs()) != n {
		t.Error("5th attempt must not re-enqueue")
	}
	if len(hits()) != 4 {
		t.Errorf("hits %d", len(hits()))
	}
}

func TestPausedAndMissingHooksAreNoOps(t *testing.T) {
	f := setup(t)
	url, hits := receiver(t, 200)
	id := f.hook(hookSpec{url: url, secret: "whsec_" + rnd(), events: []string{"*"}, disabled: true})
	f.deliver(id, 1, `{}`)
	f.deliver("00000000-0000-4000-8000-000000000000", 1, `{}`)
	if len(hits()) != 0 || len(f.jobs()) != 0 {
		t.Error("expected nothing")
	}
	if s := f.status(id); s.Status != nil || s.RanAt != nil {
		t.Errorf("paused hook status written: %+v", s)
	}
	if err := DeliverJob(f.ctx, json.RawMessage(`{"hook_id":"nope"}`)); err == nil {
		t.Error("bad hook_id must be an error")
	}
}

func TestSSRFRefusals(t *testing.T) {
	f := setup(t)
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "")
	for url, want := range map[string]string{
		"https://10.0.0.5/x":           "url must resolve to public addresses only",
		"https://127.0.0.1:9/x":        "url must resolve to public addresses only",
		"http://example.com/x":         "url must use https",
		"https://user:pw@example.com/": "url must not contain credentials",
	} {
		id := f.hook(hookSpec{url: url, secret: "whsec_" + rnd(), events: []string{"*"}})
		f.deliver(id, 5, `{}`)
		if s := f.status(id); str(s.Status) != "failed" || str(s.Err) != want {
			t.Errorf("%s: %s %s", url, str(s.Status), str(s.Err))
		}
	}
}

func TestSweepAdvancesPastNowAndEnqueues(t *testing.T) {
	f := setup(t)
	old := time.Now().Add(-23 * time.Minute).Truncate(time.Microsecond)
	future := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	five := 5
	due := f.hook(hookSpec{url: "https://example.com/x", every: &five, next: &old})
	notDue := f.hook(hookSpec{url: "https://example.com/x", every: &five, next: &future})
	paused := f.hook(hookSpec{url: "https://example.com/x", every: &five, next: &old, disabled: true})
	if err := sweepTx(f.ctx.Context, f.ctx.Tx, f.ws); err != nil {
		t.Fatal(err)
	}
	next := func(id string) time.Time {
		var n time.Time
		if err := f.ctx.Tx.QueryRow(f.ctx.Context, "select next_run_at from core.hooks where id=$1::uuid", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := next(due); !got.Equal(old.Add(25*time.Minute)) || !got.After(time.Now()) {
		t.Errorf("due next_run_at %v (old %v)", got, old)
	}
	if !next(notDue).Equal(future) || !next(paused).Equal(old) {
		t.Error("not-due and paused hooks must be untouched")
	}
	js := f.jobs()
	if len(js) != 1 {
		t.Fatalf("jobs %d", len(js))
	}
	want := `{"hook_id":"` + due + `","name":"h","scheduled_for":"` + string(mustJSON(command.Ts(old))[1:len(mustJSON(command.Ts(old)))-1]) + `","type":"schedule","workspace_id":"` + f.ws + `"}`
	var p struct {
		Body json.RawMessage `json:"body"`
	}
	_ = json.Unmarshal(js[0].Payload, &p)
	if got, _ := command.Norm(p.Body); string(got) != want {
		t.Errorf("body\n got %s\nwant %s", got, want)
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestOnEventPrefixMatching(t *testing.T) {
	f := setup(t)
	all := f.hook(hookSpec{url: "https://example.com/a", events: []string{"*"}})
	pre := f.hook(hookSpec{url: "https://example.com/b", events: []string{"note."}})
	exact := f.hook(hookSpec{url: "https://example.com/c", events: []string{"item.x", "note.created"}})
	f.hook(hookSpec{url: "https://example.com/d", events: []string{"item."}})
	f.hook(hookSpec{url: "https://example.com/e", events: []string{"*"}, disabled: true})
	f.hook(hookSpec{url: "https://example.com/f", events: []string{"note.created.more"}})
	e := jobs.Event{ID: 1, WorkspaceID: f.ws, Kind: "note.created", ActorType: "system", CreatedAt: time.Now()}
	if err := OnEvent(f.ctx, e); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, j := range f.jobs() {
		var p struct {
			HookID string `json:"hook_id"`
		}
		_ = json.Unmarshal(j.Payload, &p)
		got[p.HookID] = true
	}
	if len(got) != 3 || !got[all] || !got[pre] || !got[exact] {
		t.Errorf("matched %v", got)
	}
}

// fakeMCP is a streamable-HTTP MCP server: JSON reply for initialize (with a session id), SSE for tools/call.
type fakeMCP struct {
	mu      sync.Mutex
	calls   []string
	args    map[string]any
	deleted bool
	isError bool
	badAuth bool
	token   string
}

func (m *fakeMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+m.token {
		w.WriteHeader(401)
		return
	}
	if r.Method == "DELETE" {
		m.deleted = r.Header.Get("Mcp-Session-Id") == "sess1"
		w.WriteHeader(200)
		return
	}
	acc := r.Header.Get("Accept")
	if !strings.Contains(acc, "application/json") || !strings.Contains(acc, "text/event-stream") {
		w.WriteHeader(406)
		return
	}
	var req struct {
		ID     *int            `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	m.calls = append(m.calls, req.Method)
	switch req.Method {
	case "initialize":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "sess1")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + itoa(*req.ID) + `,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"f","version":"1"}}}`))
	case "notifications/initialized":
		if r.Header.Get("Mcp-Session-Id") != "sess1" {
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(202)
	case "tools/call":
		var p struct {
			Name string         `json:"name"`
			Args map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		m.args = p.Args
		m.args["__name"] = p.Name
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\n" +
			"event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":" + itoa(*req.ID) + ",\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"boom\"}],\"isError\":" + boolStr(m.isError) + "}}\n\n"))
	}
}

func itoa(i int) string { return string(mustJSON(i)) }
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestMCPTarget(t *testing.T) {
	f := setup(t)
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "1")
	m := &fakeMCP{token: "tok_" + rnd()}
	srv := httptest.NewServer(m)
	defer srv.Close()
	id := f.hook(hookSpec{target: "mcp", url: srv.URL + "/mcp", tool: "discover", secret: m.token, args: `{"query":"note"}`, events: []string{"*"}})
	f.deliver(id, 1, `{"type":"test"}`)
	if s := f.status(id); str(s.Status) != "ok" {
		t.Fatalf("status %s %s", str(s.Status), str(s.Err))
	}
	if strings.Join(m.calls, ",") != "initialize,notifications/initialized,tools/call" || !m.deleted {
		t.Errorf("calls %v deleted %v", m.calls, m.deleted)
	}
	if m.args["__name"] != "discover" || m.args["query"] != "note" || m.args["cnote"].(map[string]any)["type"] != "test" {
		t.Errorf("args %v", m.args)
	}
	// Tool error.
	m.isError = true
	f.deliver(id, 5, `{"type":"test"}`)
	if s := f.status(id); str(s.Status) != "failed" || str(s.Err) != `MCP tool error: [{"text":"boom","type":"text"}]` {
		t.Errorf("status %s %s", str(s.Status), str(s.Err))
	}
	// Wrong token: connect fails.
	bad := f.hook(hookSpec{target: "mcp", url: srv.URL + "/mcp", tool: "discover", secret: "cn_nope", events: []string{"*"}})
	f.deliver(bad, 5, `{}`)
	if s := f.status(bad); str(s.Status) != "failed" || !strings.HasPrefix(str(s.Err), "MCP connect failed:") {
		t.Errorf("status %s %s", str(s.Status), str(s.Err))
	}
}

func TestRustEncryptedSecretDecryptsInGo(t *testing.T) {
	f := setup(t)
	b, _ := json.Marshal(map[string]any{"name": "compat", "target": "http", "url": "https://example.com/hook", "events": []string{"note."}})
	req, _ := http.NewRequest("POST", rustURL()+"/api/w/"+f.ws+"/commands/hook.create", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "cn_session="+f.cookie)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		ID            string `json:"id"`
		SigningSecret string `json:"signing_secret"`
	}
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || json.Unmarshal(raw, &out) != nil {
		t.Fatalf("hook.create: %d %s", res.StatusCode, raw)
	}
	// Rust committed this hook in its own tx; clean it up through a tenant tx of ours (not the shared one).
	t.Cleanup(func() {
		tx, err := db.BeginTenant(context.Background(), f.pool, f.ws)
		if err == nil {
			_, _ = tx.Exec(context.Background(), "delete from core.hooks where id=$1::uuid", out.ID)
			_ = tx.Commit(context.Background())
		}
	})
	tx, err := db.BeginTenant(context.Background(), f.pool, f.ws)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	var enc []byte
	if err := tx.QueryRow(context.Background(), "select secret_enc from core.hooks where id=$1::uuid", out.ID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	key, err := secretbox.MasterKey()
	if err != nil {
		t.Fatal(err)
	}
	got, err := secretbox.Decrypt(key, f.ws, enc)
	if err != nil || got != out.SigningSecret || !strings.HasPrefix(got, "whsec_") {
		t.Fatalf("decrypt: %q %v (want %q)", got, err, out.SigningSecret)
	}
}
