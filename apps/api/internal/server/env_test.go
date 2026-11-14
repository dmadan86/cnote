package server_test

import (
	"bytes"
	"context"
	"crypto/rand"
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
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/proxy"
	"github.com/cnote/api/internal/server"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Integration tests use the real Postgres (DATABASE_URL, role cnote_app) and the real Rust server
// (RUST_API_URL, default http://localhost:4000) both to create fixtures through its public endpoints
// and as the reference for the differential tests. Nothing is mocked and nothing is skipped: a missing
// dependency fails the test run.
type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	rust string
	gosv *httptest.Server
	// proxied records every request path the Go server handed to Rust.
	proxied *proxiedPaths
}

type proxiedPaths struct {
	mu    sync.Mutex
	paths []string
}

func (p *proxiedPaths) has(sub string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, x := range p.paths {
		if strings.Contains(x, sub) {
			return true
		}
	}
	return false
}

func rnd() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newEnv(t *testing.T, extra ...command.Command) *env {
	t.Helper()
	t.Setenv("PUBLIC_TRUST_PROXY", "1") // tests pick a client address per request through X-Forwarded-For
	// Every test client is 127.0.0.1: the per-IP sign-in throttle (WP18) would otherwise lock the whole suite. The
	// throttle tests set their own values before calling newEnv.
	for _, k := range []string{"AUTH_IP_FREE", "AUTH_SIGNUP_FREE"} {
		if _, ok := os.LookupEnv(k); !ok {
			t.Setenv(k, "1000000")
		}
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Fatal("DATABASE_URL is required (load the repo .env)")
	}
	rust := strings.TrimRight(os.Getenv("RUST_API_URL"), "/")
	if rust == "" {
		rust = "http://localhost:4000"
	}
	if resp, err := http.Get(rust + "/healthz"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("Rust server not reachable at %s: %v", rust, err)
	}
	pool, err := db.Connect(context.Background(), url, 5)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	up, err := proxy.New(rust)
	if err != nil {
		t.Fatal(err)
	}
	reg := command.NewRegistry(append(modules.Commands(), extra...)...)
	seen := &proxiedPaths{}
	counted := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.mu.Lock()
		seen.paths = append(seen.paths, r.URL.Path)
		seen.mu.Unlock()
		up.ServeHTTP(w, r)
	})
	gosv := httptest.NewServer(server.New(pool, reg, counted))
	t.Cleanup(gosv.Close)
	return &env{t: t, pool: pool, rust: rust, gosv: gosv, proxied: seen}
}

// who is a caller: a session cookie, or a bearer key.
type who struct {
	userID, ws string
	cookie     string
	bearer     string
	email      string
}

func (w who) with(h http.Header) {
	if w.cookie != "" {
		h.Set("Cookie", "cn_session="+w.cookie)
	}
	if w.bearer != "" {
		h.Set("Authorization", "Bearer "+w.bearer)
	}
}

type resp struct {
	status int
	body   []byte
}

func (r resp) json() map[string]any {
	var m map[string]any
	_ = json.Unmarshal(r.body, &m)
	return m
}

func (e *env) do(base, method, path string, w who, body any, hdr map[string]string) resp {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w.with(req.Header)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, b}
}

// signup creates a user (and optionally a workspace) through the Rust server. Passwords are random per run.
func (e *env) signup(name string, workspace bool) who {
	e.t.Helper()
	w := who{email: "go-" + rnd() + "@example.test"}
	body := map[string]any{"email": w.email, "name": name, "password": rnd() + rnd()}
	if workspace {
		body["workspace_name"] = name + " WS"
	}
	b, _ := json.Marshal(body)
	res, err := http.Post(e.rust+"/api/auth/signup", "application/json", bytes.NewReader(b))
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		UserID      string  `json:"user_id"`
		WorkspaceID *string `json:"workspace_id"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	for _, c := range res.Cookies() {
		if c.Name == "cn_session" {
			w.cookie = c.Value
		}
	}
	if res.StatusCode != 200 || w.cookie == "" {
		e.t.Fatalf("signup failed: %d", res.StatusCode)
	}
	w.userID = out.UserID
	if out.WorkspaceID != nil {
		w.ws = *out.WorkspaceID
	}
	return w
}

// rust runs a command on the Rust server (fixture creation) and requires success.
func (e *env) must(w who, cmd string, in any) map[string]any {
	e.t.Helper()
	r := e.do(e.rust, "POST", "/api/w/"+w.ws+"/commands/"+cmd, w, in, nil)
	if r.status != 200 {
		e.t.Fatalf("%s: %d %s", cmd, r.status, r.body)
	}
	var m map[string]any
	_ = json.Unmarshal(r.body, &m)
	return m
}

func (e *env) space(w who) string {
	e.t.Helper()
	r := e.do(e.rust, "POST", "/api/w/"+w.ws+"/commands/space.list", w, map[string]any{}, nil)
	var spaces []map[string]any
	_ = json.Unmarshal(r.body, &spaces)
	return spaces[0]["id"].(string)
}

func (e *env) note(w who, title string) string {
	e.t.Helper()
	return e.must(w, "note.create", map[string]any{"space_id": e.space(w), "title": title})["id"].(string)
}

// addMember adds a signed-up user to the workspace of owner with the given role.
func (e *env) addMember(owner, u who, role string) who {
	e.t.Helper()
	e.must(owner, "member.add", map[string]any{"email": u.email, "role": role})
	u.ws = owner.ws
	return u
}

func (e *env) apiKey(w who) who {
	e.t.Helper()
	k := e.must(w, "apikey.create", map[string]any{"name": "go-test"})["api_key"].(string)
	return who{ws: w.ws, bearer: k}
}

// cmd calls a command on base (e.gosv.URL or e.rust).
func (e *env) cmd(base string, w who, ws, name string, in any, hdr map[string]string) resp {
	e.t.Helper()
	return e.do(base, "POST", "/api/w/"+ws+"/commands/"+name, w, in, hdr)
}

func (e *env) rawJSON(base string, w who, cmd, raw string) resp {
	e.t.Helper()
	req, _ := http.NewRequest("POST", base+"/api/w/"+w.ws+"/commands/"+cmd, strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w.with(req.Header)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, b}
}

func dbTenant(e *env, ws string) (pgx.Tx, error) {
	return db.BeginTenant(context.Background(), e.pool, ws)
}

func timeNowMicro() time.Time { return time.Now().Truncate(time.Microsecond) }
