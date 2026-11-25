package worker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/server"
)

// Integration tests use the real Postgres (DATABASE_URL, role cnote_app) and the Go server (httptest) as fixture
// factory. A background Go worker claims every committed queued job, so the checked side of a test runs inside ONE
// transaction that is rolled back: nothing it enqueues is ever visible to that worker. Nothing is mocked or skipped.

var workerOnce sync.Once

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	api  string
	reg  *command.Registry
	w    *Worker
}

func rnd() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newEnv(t *testing.T) *env {
	t.Helper()
	t.Setenv("AUTH_SIGNUP_FREE", "1000000") // every test signs up from 127.0.0.1; the per-IP throttle is shared through the database
	t.Setenv("AUTH_IP_FREE", "1000000")
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Fatal("DATABASE_URL is required (load the repo .env)")
	}
	pool, err := db.Connect(context.Background(), url, 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	reg := modules.Registry()
	workerOnce.Do(func() { // one background worker for the test binary, like production WITH_WORKER=1
		wp, err := db.Connect(context.Background(), url, 5)
		if err != nil {
			t.Fatal(err)
		}
		go New(wp, reg).Run(context.Background())
	})
	srv := httptest.NewServer(server.New(pool, reg))
	t.Cleanup(srv.Close)
	return &env{t: t, pool: pool, api: srv.URL, reg: reg, w: New(pool, reg)}
}

type who struct {
	userID, ws, cookie, email string
}

func (e *env) signup(name string) who {
	e.t.Helper()
	w := who{email: "go-" + rnd() + "@example.test"}
	b, _ := json.Marshal(map[string]any{"email": w.email, "name": name, "password": rnd() + rnd(), "workspace_name": name + " WS"})
	res, err := http.Post(e.api+"/api/auth/signup", "application/json", bytes.NewReader(b))
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
	if res.StatusCode != 200 || w.cookie == "" || out.WorkspaceID == nil {
		e.t.Fatalf("signup failed: %d", res.StatusCode)
	}
	w.userID, w.ws = out.UserID, *out.WorkspaceID
	return w
}

// apiCmd runs a command on the Go server and returns the decoded JSON body.
func (e *env) apiCmd(w who, name string, in any) any {
	e.t.Helper()
	b, _ := json.Marshal(in)
	req, _ := http.NewRequest("POST", e.api+"/api/w/"+w.ws+"/commands/"+name, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "cn_session="+w.cookie)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		e.t.Fatalf("%s: %d %s", name, res.StatusCode, body)
	}
	var v any
	_ = json.Unmarshal(body, &v)
	return v
}

// side is one workspace plus how to run commands in it: Go inside an uncommitted transaction (the test plays the
// worker).
type side interface {
	cmd(name string, in any) any
	ws() who
}

type goSide struct {
	e        *env
	w        who
	c        *command.Ctx
	user     command.Principal
	baseline int64
}

// newGoSide opens the test transaction for a fresh workspace.
func (e *env) newGoSide(w who) *goSide {
	e.t.Helper()
	tx, err := db.BeginTenant(context.Background(), e.pool, w.ws)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	user := command.Principal{Kind: command.KindUser, UserID: &w.userID, WorkspaceID: w.ws, Role: command.Owner}
	c := &command.Ctx{Context: context.Background(), Tx: tx, Pool: e.pool, Principal: user}
	e.reg.Bind(c)
	s := &goSide{e: e, w: w, c: c, user: user}
	if err := tx.QueryRow(c.Context, "select coalesce(max(id),0) from core.jobs where workspace_id = $1::uuid", w.ws).Scan(&s.baseline); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (s *goSide) ws() who { return s.w }

func (s *goSide) cmd(name string, in any) any {
	s.e.t.Helper()
	s.c.Principal = s.user
	out, err := s.c.Call(name, in)
	if errors.Is(err, command.ErrFallback) && name == "automation.create" {
		out, err = s.insertAutomation(in.(map[string]any)) // Go defers `command` actions to Rust; store the row as Rust would
	}
	if err != nil {
		s.e.t.Fatalf("go %s: %v", name, err)
	}
	var v any
	_ = json.Unmarshal(out, &v)
	return v
}

func (s *goSide) insertAutomation(in map[string]any) ([]byte, error) {
	j := func(v any) []byte { b, _ := json.Marshal(v); return b }
	var conds any = []any{}
	if c, ok := in["conditions"]; ok {
		conds = c
	}
	enabled, draft := true, false
	if v, ok := in["enabled"].(bool); ok {
		enabled = v
	}
	if v, ok := in["draft"].(bool); ok {
		draft = v
	}
	var id string
	err := s.c.Tx.QueryRow(s.c.Context,
		`insert into projects.automations (workspace_id, space_id, name, enabled, draft, trigger, conditions, actions, created_by)
		 values ($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9::uuid) returning id::text`,
		s.w.ws, in["space_id"], in["name"], enabled, draft, j(in["trigger"]), j(conds), j(in["actions"]), s.w.userID).Scan(&id)
	if err != nil {
		return nil, err
	}
	_, err = s.c.Emit("automation.created", nil, map[string]any{"automation_id": id, "name": in["name"]})
	return j(map[string]any{"id": id}), err
}

// drain plays the Go worker over the jobs this transaction created: runs every claimable job (event.dispatch and
// the module jobs the Go worker owns) until none is left, marking them done. Jobs of kinds Go never claims stay queued.
func (s *goSide) drain(max int) int {
	s.e.t.Helper()
	ran := 0
	for ; ran < max; ran++ {
		rows, err := s.c.Tx.Query(s.c.Context,
			`select id, workspace_id::text, kind, payload, attempts, max_attempts from core.jobs
			 where workspace_id = $1::uuid and id > $2 and status = 'queued' and kind = any($3::text[]) order by id limit 1`,
			s.w.ws, s.baseline, s.e.w.Kinds())
		if err != nil {
			s.e.t.Fatal(err)
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (jobs.Job, error) {
			var j jobs.Job
			return j, r.Scan(&j.ID, &j.WorkspaceID, &j.Kind, &j.Payload, &j.Attempts, &j.MaxAttempts)
		})
		if err != nil {
			s.e.t.Fatal(err)
		}
		if len(list) == 0 {
			return ran
		}
		j := list[0]
		s.c.Principal = command.Principal{Kind: command.KindSystem, WorkspaceID: s.w.ws, Role: command.Owner}
		if err := s.e.w.Handle(s.c, j); err != nil {
			s.e.t.Fatalf("job %d %s: %v", j.ID, j.Kind, err)
		}
		if _, err := s.c.Tx.Exec(s.c.Context, "update core.jobs set status = 'done' where id = $1", j.ID); err != nil {
			s.e.t.Fatal(err)
		}
	}
	s.e.t.Fatal("job chain did not terminate")
	return ran
}

// settle waits for the background worker to finish every dispatch/hook/import job of the workspace.
func (e *env) settle(ws string) {
	e.t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		tx, err := db.BeginTenant(context.Background(), e.pool, ws)
		if err != nil {
			e.t.Fatal(err)
		}
		var n int
		err = tx.QueryRow(context.Background(),
			"select count(*) from core.jobs where workspace_id = $1::uuid and status in ('queued','running') and run_at <= now() + interval '5 seconds' and kind = any($2::text[])",
			ws, e.w.Kinds()).Scan(&n)
		_ = tx.Rollback(context.Background())
		if err != nil {
			e.t.Fatal(err)
		}
		if n == 0 {
			time.Sleep(300 * time.Millisecond) // the worker commits the job row after its work
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	e.t.Fatalf("worker did not drain workspace %s", ws)
}

func beginTenant(e *env, ws string) (pgx.Tx, error) {
	return db.BeginTenant(context.Background(), e.pool, ws)
}

func cleanup(e *env, ws string, id int64) {
	tx, err := beginTenant(e, ws)
	if err != nil {
		return
	}
	_, _ = tx.Exec(context.Background(), "delete from core.jobs where id = $1", id)
	_ = tx.Commit(context.Background())
}

func unsetenv(k string) error { return os.Unsetenv(k) }
