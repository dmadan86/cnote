package importers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
)

// Tests use the real Postgres (DATABASE_URL, role cnote_app) and create fixtures (user, workspace, space, member)
// with SQL. The import itself runs inside one transaction that is rolled back: a running worker must never see a
// queued job of ours.

func rnd() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
}

func newEnv(t *testing.T) *env {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Fatal("DATABASE_URL is required (load the repo .env)")
	}
	pool, err := db.Connect(context.Background(), url, 5)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return &env{t: t, pool: pool}
}

// tenant is a fresh workspace made with SQL: an owner, a space ENG and one extra member (ada).
type tenant struct {
	e        *env
	owner    string // user id
	ws       string
	space    string
	adaID    string
	adaEmail string
}

// signup inserts a user (users are global); the workspace is created for it when asked.
func (e *env) signup(name string, workspace bool) (userID, ws, email string) {
	e.t.Helper()
	ctx := context.Background()
	email = "go-" + rnd() + "@example.test"
	if err := e.pool.QueryRow(ctx, "insert into core.users(email, name) values ($1, $2) returning id::text", email, name).Scan(&userID); err != nil {
		e.t.Fatal(err)
	}
	if workspace {
		if err := e.pool.QueryRow(ctx, "select core.create_workspace($1, $2, $3::uuid)::text", name+" WS", "t-"+rnd(), userID).Scan(&ws); err != nil {
			e.t.Fatal(err)
		}
	}
	return userID, ws, email
}

// newTenant makes a workspace; with a given ada that existing user becomes the member (twin workspaces share her).
func (e *env) newTenant(shared ...*tenant) *tenant {
	e.t.Helper()
	t := &tenant{e: e}
	t.owner, t.ws, _ = e.signup("Owner", true)
	if len(shared) > 0 {
		t.adaID, t.adaEmail = shared[0].adaID, shared[0].adaEmail
	} else {
		t.adaID, _, t.adaEmail = e.signup("Ada", false)
	}
	tx := t.begin()
	defer tx.Rollback(context.Background())
	if err := tx.QueryRow(context.Background(), "insert into core.spaces (workspace_id, key, name) values ($1::uuid, 'ENG', 'Engineering') returning id::text", t.ws).Scan(&t.space); err != nil {
		e.t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), "insert into core.memberships (workspace_id, user_id, role) values ($1::uuid, $2::uuid, 'member')", t.ws, t.adaID); err != nil {
		e.t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		e.t.Fatal(err)
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
