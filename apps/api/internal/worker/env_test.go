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
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/modules"
)

// Integration tests use the real Postgres (DATABASE_URL, role cnote_app) and the Rust server on RUST_API_URL
// (default http://localhost:4000) as fixture factory and as the reference. A Rust worker runs against the same
// database and claims every committed queued job, so the Go side of every test runs inside ONE transaction that is
// rolled back: nothing it enqueues is ever visible to Rust. Nothing is mocked or skipped.

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	rust string
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
	pool, err := db.Connect(context.Background(), url, 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	reg := modules.Registry()
	return &env{t: t, pool: pool, rust: rust, reg: reg, w: New(pool, reg)}
}

type who struct {
	userID, ws, cookie, email string
}

func (e *env) signup(name string) who {
	e.t.Helper()
	w := who{email: "go-" + rnd() + "@example.test"}
	b, _ := json.Marshal(map[string]any{"email": w.email, "name": name, "password": rnd() + rnd(), "workspace_name": name + " WS"})
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
	if res.StatusCode != 200 || w.cookie == "" || out.WorkspaceID == nil {
		e.t.Fatalf("signup failed: %d", res.StatusCode)
	}
	w.userID, w.ws = out.UserID, *out.WorkspaceID
	return w
}

// rustCmd runs a command on the Rust server and returns the decoded JSON body.
func (e *env) rustCmd(w who, name string, in any) any {
	e.t.Helper()
	b, _ := json.Marshal(in)
	req, _ := http.NewRequest("POST", e.rust+"/api/w/"+w.ws+"/commands/"+name, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "cn_session="+w.cookie)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		e.t.Fatalf("rust %s: %d %s", name, res.StatusCode, body)
	}
	var v any
	_ = json.Unmarshal(body, &v)
	return v
}

// side is one workspace plus how to run commands in it: Rust over HTTP (the real worker then reacts), or Go inside
// an uncommitted transaction (the test then plays the worker).
type side interface {
	cmd(name string, in any) any
	ws() who
}

type rustSide struct {
	e *env
	w who
}

func (s *rustSide) cmd(name string, in any) any { return s.e.rustCmd(s.w, name, in) }
func (s *rustSide) ws() who                     { return s.w }

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
	c := &command.Ctx{Context: context.Background(), Tx: tx, Principal: user}
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

func (s *goSide) try(name string, in any) error {
	s.c.Principal = s.user
	_, err := s.c.Call(name, in)
	return err
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

// settle waits for the shared Rust worker to finish every dispatch/hook/import job of the workspace.
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
	e.t.Fatalf("Rust worker did not drain workspace %s", ws)
}

// readCtx is a read-only tenant ctx over committed data (the Rust side).
func (e *env) readCtx(ws string) *command.Ctx {
	e.t.Helper()
	tx, err := db.BeginTenant(context.Background(), e.pool, ws)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return &command.Ctx{Context: context.Background(), Tx: tx, Principal: command.Principal{Kind: command.KindSystem, WorkspaceID: ws, Role: command.Owner}}
}

var uuidRe = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// snapshot renders the worker-visible rows of a workspace with ids replaced by note titles / item keys, so a Rust
// run and a Go run of the same scenario compare equal. Timestamps and ids are excluded.
func snapshot(t *testing.T, c *command.Ctx, extra ...string) string {
	t.Helper()
	names := map[string]string{}
	label := func(sql string, prefix string) {
		rows, err := c.Tx.Query(c.Context, sql)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, n string
			if err := rows.Scan(&id, &n); err != nil {
				t.Fatal(err)
			}
			names[id] = prefix + n
		}
	}
	label("select id::text, title from core.notes", "note:")
	label("select id::text, name from projects.automations", "automation:")
	label("select id::text, name from core.hooks", "hook:")
	label("select id::text, '' from core.users", "user")
	names[c.WorkspaceID()] = "ws"
	norm := func(s string) string {
		return uuidRe.ReplaceAllStringFunc(s, func(m string) string {
			if n, ok := names[m]; ok {
				return n
			}
			return "<id>"
		})
	}
	var out []string
	add := func(section, sql string) {
		rows, err := c.Tx.Query(c.Context, sql)
		if err != nil {
			t.Fatalf("%s: %v", section, err)
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			vals, err := rows.Values()
			if err != nil {
				t.Fatal(err)
			}
			parts := make([]string, len(vals))
			for i, v := range vals {
				switch x := v.(type) {
				case nil:
					parts[i] = "null"
				case []byte:
					parts[i] = string(x)
				case map[string]any, []any:
					b, _ := json.Marshal(x)
					parts[i] = string(b)
				default:
					b, _ := json.Marshal(x)
					parts[i] = string(b)
				}
			}
			lines = append(lines, norm(strings.Join(parts, " | ")))
		}
		sort.Strings(lines)
		out = append(out, "## "+section)
		out = append(out, lines...)
	}
	add("events", "select kind, actor_type, payload::text from core.events where kind <> 'doc.proposal_created'")
	add("relations", "select kind, coalesce(external_ref,''), from_id::text, to_id::text from core.relations")
	add("external_bindings", "select kind, ref, note_id::text from docs.external_bindings")
	add("flags", "select note_id::text, reason, coalesce(source_note_id::text,''), (resolved_at is null)::text from docs.freshness_flags")
	add("notes", "select title, freshness, lapse_count::text, coalesce(verify_by::text,''), coalesce(content_md,'') from core.notes")
	add("jobs", "select kind, case when kind in ('ai.index_note','docs.propose_edit') then payload::text else '' end from core.jobs where kind <> 'event.dispatch'")
	add("automation_runs", "select automation_id::text, status, log::text from projects.automation_runs")
	add("comments", "select note_id::text, body, coalesce(author_name,'') from core.comments")
	add("usage", "select kind, quantity::text from core.usage where kind not like 'ai.%'")
	for _, q := range extra {
		add(q, q)
	}
	return strings.Join(out, "\n")
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
