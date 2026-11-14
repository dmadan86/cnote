//go:build recovery

package server_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/proxy"
	"github.com/cnote/api/internal/server"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CN-QA-001 recovery tests (section 4): run with `go test -tags recovery -run TestRecovery ./internal/server`.
// Needs the same Postgres and Rust server as the other integration tests (Rust is only used for fixtures and is
// never stopped). Everything that is stopped here is a stand-in owned by the test, on ports 4672-4675.
// Collab restart and "database unreachable" for live editing are covered by apps/collab/test/resilience.test.ts
// (DU-001, DU-003, CO-006), which the nightly workflow runs.

const (
	frontAddr    = "127.0.0.1:4672" // the Go front under test
	pgProxyAddr  = "127.0.0.1:4673" // TCP forwarder in front of Postgres
	upstreamAddr = "127.0.0.1:4674" // stand-in for the Rust server
	deadAddr     = "127.0.0.1:4675" // nothing listens here
)

func serveOn(t *testing.T, addr string, h http.Handler) *httptest.Server {
	t.Helper()
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewUnstartedServer(h)
	s.Listener.Close()
	s.Listener = l
	s.Start()
	return s
}

// forwarder is a TCP proxy to target whose stop() severs every connection and refuses new ones, like a network drop.
type forwarder struct {
	addr, target string
	l            net.Listener
	mu           sync.Mutex
	conns        []net.Conn
}

func (f *forwarder) start(t *testing.T) {
	l, err := net.Listen("tcp", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	f.l = l
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			u, err := net.Dial("tcp", f.target)
			if err != nil {
				c.Close()
				continue
			}
			f.mu.Lock()
			f.conns = append(f.conns, c, u)
			f.mu.Unlock()
			go func() { _, _ = io.Copy(u, c); u.Close() }()
			go func() { _, _ = io.Copy(c, u); c.Close() }()
		}
	}()
}

func (f *forwarder) stop() {
	f.l.Close()
	f.mu.Lock()
	for _, c := range f.conns {
		c.Close()
	}
	f.conns = nil
	f.mu.Unlock()
}

func recoveryEnv(t *testing.T) *env {
	t.Helper()
	rust := "http://localhost:4000"
	if v := os.Getenv("RUST_API_URL"); v != "" {
		rust = v
	}
	if r, err := http.Get(rust + "/healthz"); err != nil || r.StatusCode != 200 {
		t.Fatalf("Rust server not reachable at %s: %v", rust, err)
	}
	if os.Getenv("DATABASE_URL") == "" {
		t.Fatal("DATABASE_URL is required")
	}
	return &env{t: t, rust: rust} // fixtures only need the Rust URL
}

func (e *env) openPool(host, port string) *pgxpool.Pool {
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		e.t.Fatal(err)
	}
	if host != "" {
		cfg.ConnConfig.Host, cfg.ConnConfig.Port = host, mustPort(port)
		cfg.ConnConfig.ConnectTimeout = 2 * time.Second
		cfg.ConnConfig.TLSConfig, cfg.ConnConfig.Fallbacks = nil, nil
	}
	cfg.MaxConns = 4
	cfg.MaxConnLifetime = time.Hour
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(p.Close)
	return p
}

func mustPort(p string) uint16 {
	var n uint16
	for _, c := range p {
		n = n*10 + uint16(c-'0')
	}
	return n
}

func (e *env) front(pool *pgxpool.Pool, upstream string) string {
	up, err := proxy.New(upstream)
	if err != nil {
		e.t.Fatal(err)
	}
	s := serveOn(e.t, frontAddr, server.New(pool, command.NewRegistry(modules.Commands()...), up))
	e.t.Cleanup(s.Close)
	return s.URL
}

// until polls f until it returns true or d passes.
func until(d time.Duration, f func() bool) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if f() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func TestRecovery_PostgresDropAndReconnect(t *testing.T) {
	e := recoveryEnv(t)
	owner := e.signup("Rec", true)
	fw := &forwarder{addr: pgProxyAddr, target: "localhost:5432"}
	pg := e.openPool("127.0.0.1", "4673")
	fw.start(t)
	base := e.front(pg, e.rust)
	call := func() resp { return e.cmd(base, owner, owner.ws, "space.list", map[string]any{}, nil) }

	if r := call(); r.status != 200 {
		t.Fatalf("baseline: %d %s", r.status, r.body)
	}
	fw.stop() // network drop: live connections reset, new ones refused
	start := time.Now()
	r := call()
	if r.status != 500 || time.Since(start) > 10*time.Second {
		t.Fatalf("database down must answer 500 promptly, got %d after %s: %s", r.status, time.Since(start), r.body)
	}
	if r.json()["error"].(map[string]any)["message"] != "database error" {
		t.Fatalf("error must not leak the cause: %s", r.body)
	}
	if e.do(base, "GET", "/healthz", who{}, nil, nil).status != 200 {
		t.Fatal("process health must not depend on the database")
	}
	fw.start(t)
	t.Cleanup(fw.stop)
	if !until(15*time.Second, func() bool { return call().status == 200 }) {
		t.Fatal("pool did not reconnect after the database came back")
	}
	if r := call(); r.status != 200 { // stays healthy, no stale connection left in the pool
		t.Fatalf("after recovery: %d %s", r.status, r.body)
	}
}

func TestRecovery_PostgresBackendTerminated(t *testing.T) {
	e := recoveryEnv(t)
	owner := e.signup("Rec", true)
	pool := e.openPool("", "")
	base := e.front(pool, e.rust)
	call := func() resp { return e.cmd(base, owner, owner.ws, "space.list", map[string]any{}, nil) }
	for range 8 { // fill the pool with live connections
		if call().status != 200 {
			t.Fatal("baseline")
		}
	}
	// A failover or admin restart kills every backend of the app role (except this one).
	admin := e.openPool("", "")
	if _, err := admin.Exec(context.Background(),
		`select pg_terminate_backend(pid) from pg_stat_activity where usename = current_user and pid <> pg_backend_pid() and application_name = ''`); err != nil {
		t.Fatal(err)
	}
	bad := 0
	for range 20 {
		if call().status != 200 {
			bad++
		}
	}
	// pgxpool health-checks idle conns, so a request may fail at most once per killed connection, then recovers.
	if bad > 4 {
		t.Fatalf("%d of 20 requests failed after the backends were terminated; the pool should heal within its size", bad)
	}
	if call().status != 200 {
		t.Fatal("not recovered")
	}
}

func TestRecovery_RustUpstreamDownAndUp(t *testing.T) {
	e := recoveryEnv(t)
	owner := e.signup("Rec", true)
	pool := e.openPool("", "")
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "upstream ok") })
	up := serveOn(t, upstreamAddr, ok)
	base := e.front(pool, "http://"+upstreamAddr)
	get := func(path string) resp { return e.do(base, "GET", path, owner, nil, nil) }

	if r := get("/api/auth/me"); r.status != 200 || string(r.body) != "upstream ok" {
		t.Fatalf("proxied while up: %d %s", r.status, r.body)
	}
	up.Close()
	// docs/go-migration.md: non-native traffic returns 502 while Rust is unreachable; native commands keep working.
	if r := get("/api/auth/me"); r.status != http.StatusBadGateway {
		t.Fatalf("upstream down must be 502, got %d %s", r.status, r.body)
	}
	if r := e.cmd(base, owner, owner.ws, "space.list", map[string]any{}, nil); r.status != 200 {
		t.Fatalf("native command while upstream is down: %d %s", r.status, r.body)
	}
	if r := get("/healthz"); r.status != 200 {
		t.Fatalf("healthz while upstream is down: %d", r.status)
	}
	up = serveOn(t, upstreamAddr, ok)
	defer up.Close()
	if !until(5*time.Second, func() bool { return get("/api/auth/me").status == 200 }) {
		t.Fatal("proxy did not recover after the upstream returned")
	}
}

func TestRecovery_UnreachableUpstreamFailsFast(t *testing.T) {
	e := recoveryEnv(t)
	owner := e.signup("Rec", true)
	base := e.front(e.openPool("", ""), "http://"+deadAddr)
	start := time.Now()
	if r := e.do(base, "GET", "/api/auth/me", owner, nil, nil); r.status != http.StatusBadGateway || time.Since(start) > 5*time.Second {
		t.Fatalf("want fast 502, got %d after %s", r.status, time.Since(start))
	}
}

// Worker crash mid-job. The Rust worker (crates/cnote-server/src/worker.rs) claims with core.claim_jobs
// (status running, attempts+1) and applies the effect and the done mark in separate transactions; nothing
// requeues a job whose worker died (no lease expiry). This test pins what the queue does today and what a
// recovery must guarantee: a crash before commit leaves no partial effect, the job is stuck in running until
// requeued, and the retry then applies the effect exactly once. The jobs use a future run_at so the live Rust
// worker never touches them; the stand-in worker claims by id with the same transition claim_jobs makes.
func TestRecovery_WorkerCrashMidJobRetriedOnceInEffect(t *testing.T) {
	e := recoveryEnv(t)
	e.pool = e.openPool("", "")
	owner := e.signup("Rec", true)
	note := e.note(owner, "job")
	ctx := context.Background()

	tx, err := dbTenant(e, owner.ws)
	if err != nil {
		t.Fatal(err)
	}
	var job int64
	if err := tx.QueryRow(ctx, `insert into core.jobs (workspace_id, kind, payload, run_at) values ($1, 'recovery.test', '{}', now() + interval '1 day') returning id`, owner.ws).Scan(&job); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	state := func() (status string, attempts int, title string) {
		tx, err := dbTenant(e, owner.ws)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err := tx.QueryRow(ctx, `select status, attempts from core.jobs where id = $1`, job).Scan(&status, &attempts); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(ctx, `select title from core.notes where id = $1`, note).Scan(&title); err != nil {
			t.Fatal(err)
		}
		return
	}
	claim := func() {
		tx, _ := dbTenant(e, owner.ws)
		defer tx.Rollback(ctx)
		ct, err := tx.Exec(ctx, `update core.jobs set status = 'running', attempts = attempts + 1, updated_at = now() where id = $1 and status = 'queued'`, job)
		if err != nil || ct.RowsAffected() != 1 {
			t.Fatalf("claim: %v %v", err, ct)
		}
		_ = tx.Commit(ctx)
	}
	// work applies the effect and marks done in one transaction; crash=true kills the connection before commit.
	work := func(crash bool) {
		cfg := e.pool.Config().ConnConfig
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		tx, _ := conn.Begin(ctx)
		_, _ = tx.Exec(ctx, `select set_config('app.workspace_id', $1, true)`, owner.ws)
		if _, err := tx.Exec(ctx, `update core.notes set title = title || '+' where id = $1`, note); err != nil {
			t.Fatal(err)
		}
		if ct, err := tx.Exec(ctx, `update core.jobs set status = 'done', updated_at = now() where id = $1 and status = 'running'`, job); err != nil || ct.RowsAffected() != 1 {
			t.Fatalf("done: %v %v", err, ct)
		}
		if crash {
			conn.PgConn().Conn().Close() // process killed: the server rolls the open transaction back
			return
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		conn.Close(ctx)
	}

	claim()
	work(true)
	time.Sleep(300 * time.Millisecond) // let the server notice the dead connection
	if st, att, title := state(); st != "running" || att != 1 || title != "job" {
		t.Fatalf("after the crash want running/1 attempt/no effect, got %s/%d/%q", st, att, title)
	}
	// Known gap: no lease expiry exists, so the job stays 'running' forever. A reaper would run this statement.
	tx, _ = dbTenant(e, owner.ws)
	if _, err := tx.Exec(ctx, `update core.jobs set status = 'queued' where id = $1 and status = 'running' and updated_at < now()`, job); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)
	claim()
	work(false)
	if st, att, title := state(); st != "done" || att != 2 || title != "job+" {
		t.Fatalf("after the retry want done/2 attempts/one effect, got %s/%d/%q", st, att, title)
	}
	// A late duplicate delivery of the same job must not apply the effect again (done-guard in the same transaction).
	conn, err := pgx.ConnectConfig(ctx, e.pool.Config().ConnConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx2, _ := conn.Begin(ctx)
	_, _ = tx2.Exec(ctx, `select set_config('app.workspace_id', $1, true)`, owner.ws)
	ct, _ := tx2.Exec(ctx, `update core.jobs set status = 'done' where id = $1 and status = 'running'`, job)
	_ = tx2.Rollback(ctx)
	if ct.RowsAffected() != 0 {
		t.Fatal("a finished job must not be completable twice")
	}
}
