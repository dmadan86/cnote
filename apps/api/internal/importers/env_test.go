package importers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
)

// Tests use the real Postgres (DATABASE_URL, role cnote_app) and the real Rust server (RUST_API_URL, default
// http://localhost:4000) to create fixtures (signup, space, member). The Go import itself runs inside one
// transaction that is rolled back: the shared Rust worker must never see a queued job of ours.

func rnd() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	rust string
}

func newEnv(t *testing.T) *env {
	t.Helper()
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
	return &env{t: t, pool: pool, rust: rust}
}

// tenant is a fresh workspace made through the Rust server: an owner, a space ENG and one extra member (ada).
type tenant struct {
	e        *env
	owner    string // user id
	cookie   string
	ws       string
	space    string
	adaID    string
	adaEmail string
}

func (e *env) call(cookie, ws, cmd string, in any) (int, []byte) {
	e.t.Helper()
	b, _ := json.Marshal(in)
	req, _ := http.NewRequest("POST", e.rust+"/api/w/"+ws+"/commands/"+cmd, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "cn_session="+cookie)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

func (e *env) signup(name string, workspace bool) (userID, ws, cookie, email string) {
	e.t.Helper()
	email = "go-" + rnd() + "@example.test"
	body := map[string]any{"email": email, "name": name, "password": rnd() + rnd()}
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
			cookie = c.Value
		}
	}
	if res.StatusCode != 200 || cookie == "" {
		e.t.Fatalf("signup failed: %d", res.StatusCode)
	}
	if out.WorkspaceID != nil {
		ws = *out.WorkspaceID
	}
	return out.UserID, ws, cookie, email
}

// newTenant makes a workspace; with a given ada that existing user becomes the member (twin workspaces share her).
func (e *env) newTenant(shared ...*tenant) *tenant {
	e.t.Helper()
	t := &tenant{e: e}
	t.owner, t.ws, t.cookie, _ = e.signup("Owner", true)
	st, body := e.call(t.cookie, t.ws, "space.create", map[string]any{"key": "ENG", "name": "Engineering"})
	var sp struct{ ID string }
	if st != 200 || json.Unmarshal(body, &sp) != nil || sp.ID == "" {
		e.t.Fatalf("space.create: %d %s", st, body)
	}
	t.space = sp.ID
	if len(shared) > 0 {
		t.adaID, t.adaEmail = shared[0].adaID, shared[0].adaEmail
	} else {
		t.adaID, _, _, t.adaEmail = e.signup("Ada", false)
	}
	if st, body := e.call(t.cookie, t.ws, "member.add", map[string]any{"email": t.adaEmail, "role": "member"}); st != 200 {
		e.t.Fatalf("member.add: %d %s", st, body)
	}
	return t
}

// fix replaces the fixture's member address with this tenant's (users are global, so emails must be unique per run).
func (t *tenant) fix(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "ada@example.com", t.adaEmail), "ADA@EXAMPLE.COM", strings.ToUpper(t.adaEmail))
}

func (t *tenant) begin() pgx.Tx {
	t.e.t.Helper()
	tx, err := db.BeginTenant(context.Background(), t.e.pool, t.ws)
	if err != nil {
		t.e.t.Fatal(err)
	}
	return tx
}

// gorun is the Go side of an import: everything happens in one transaction that is never committed.
type gorun struct {
	t   *testing.T
	ten *tenant
	tx  pgx.Tx
	ctx *command.Ctx
}

func (t *tenant) goRun() *gorun {
	tx := t.begin()
	t.e.t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return &gorun{t: t.e.t, ten: t, tx: tx, ctx: &command.Ctx{
		Context:   context.Background(),
		Tx:        tx,
		Principal: command.Principal{Kind: command.KindSystem, WorkspaceID: t.ws, Role: command.Owner},
	}}
}

// start inserts the importers.jobs row exactly like import.start (after its own parse check) and returns its id.
func (g *gorun) start(source, content string, mapping any, batch any) string {
	g.t.Helper()
	in := map[string]any{"content": content, "mapping": mapping, "batch_size": batch}
	var id string
	err := g.tx.QueryRow(context.Background(),
		"insert into importers.jobs (workspace_id, source, space_id, input, created_by) values ($1::uuid,$2,$3::uuid,$4::jsonb,$5::uuid) returning id::text",
		g.ten.ws, source, g.ten.space, marshal(in), g.ten.owner).Scan(&id)
	if err != nil {
		g.t.Fatal(err)
	}
	if _, err := g.tx.Exec(context.Background(),
		"insert into core.jobs (workspace_id, kind, payload, run_at) values ($1::uuid,'import.run',$2::jsonb,now())",
		g.ten.ws, marshal(map[string]any{"import_id": id})); err != nil {
		g.t.Fatal(err)
	}
	return id
}

// batch runs the next queued import.run job for this import (marking it done like the worker), false when none is queued.
func (g *gorun) batch(id string) bool {
	g.t.Helper()
	var payload []byte
	err := g.tx.QueryRow(context.Background(),
		`update core.jobs set status = 'done' where id = (
		   select id from core.jobs where kind = 'import.run' and status = 'queued' and payload->>'import_id' = $1 order by id limit 1)
		 returning payload`, id).Scan(&payload)
	if err == pgx.ErrNoRows {
		return false
	}
	if err != nil {
		g.t.Fatal(err)
	}
	if err := RunJob(g.ctx, payload); err != nil {
		g.t.Fatalf("RunJob: %v", err)
	}
	return true
}

func (g *gorun) all(id string) int {
	n := 0
	for g.batch(id) {
		n++
	}
	return n
}

// status is importers.jobs minus input, like import.status.
func (g *gorun) status(id string) map[string]any {
	return jobStatus(g.t, g.tx, id)
}

func jobStatus(t *testing.T, q pgx.Tx, id string) map[string]any {
	t.Helper()
	var raw string
	if err := q.QueryRow(context.Background(), "select (to_jsonb(j) - 'input')::text from importers.jobs j where id = $1::uuid", id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func q1[T any](t *testing.T, tx pgx.Tx, sql string, args ...any) T {
	t.Helper()
	var v T
	if err := tx.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func waitFor(t *testing.T, what string, d time.Duration, f func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if f() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
