// Package worker is the Go port of crates/cnote-server/src/worker.rs: the durable job loop (event dispatch to
// module handlers, module jobs, periodic sweeps).
//
// Coexistence with the Rust worker: both claim through core.claim_jobs (UPDATE ... WHERE id IN (SELECT ... FOR
// UPDATE SKIP LOCKED)), so a job is run by exactly one of them. The Go worker only claims the kinds it can run
// (see Kinds): jobs that call an AI provider (ai.index_note, docs.propose_edit) are left to Rust, which claims
// every kind. Cutover is therefore: start this worker, stop Rust's.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/jobs"
)

type EventHandler func(c *command.Ctx, e jobs.Event) error
type JobHandler func(c *command.Ctx, payload json.RawMessage) error
type SweepFunc func(ctx context.Context, w *Worker) error

type eventHandler struct {
	prefix string
	h      EventHandler
}

type Sweep struct {
	Name  string
	Every time.Duration
	Run   SweepFunc
}

// Worker holds the handler tables. Order of event handlers matters and follows the Rust module order.
type Worker struct {
	Pool     *pgxpool.Pool
	Registry *command.Registry // used by handlers that run commands (automations): same gate as Rust ctx.call
	events   []eventHandler
	jobs     map[string]JobHandler
	sweeps   []Sweep
	Limit    int           // jobs claimed per poll (Rust: 20)
	Idle     time.Duration // sleep when the queue is empty (Rust: 500ms)
}

func newWorker(pool *pgxpool.Pool, reg *command.Registry) *Worker {
	return &Worker{Pool: pool, Registry: reg, jobs: map[string]JobHandler{}, Limit: 20, Idle: 500 * time.Millisecond}
}

func (w *Worker) OnEvent(prefix string, h EventHandler) {
	w.events = append(w.events, eventHandler{prefix, h})
}
func (w *Worker) OnJob(kind string, h JobHandler) { w.jobs[kind] = h }
func (w *Worker) AddSweep(s Sweep)                { w.sweeps = append(w.sweeps, s) }

// Kinds are the job kinds this worker claims. Anything else stays queued for another worker (Rust).
func (w *Worker) Kinds() []string {
	k := []string{"event.dispatch"}
	for kind := range w.jobs {
		k = append(k, kind)
	}
	return k
}

// Run starts the sweeps and polls until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	for _, s := range w.sweeps {
		go func() {
			t := time.NewTicker(s.Every)
			defer t.Stop()
			for { // like tokio's interval: the first tick is immediate
				if err := w.safeSweep(ctx, s); err != nil && ctx.Err() == nil {
					slog.Error("sweep failed", "sweep", s.Name, "error", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}()
	}
	slog.Info("worker started", "kinds", strings.Join(w.Kinds(), ","))
	for ctx.Err() == nil {
		n, err := w.DrainOnce(ctx, w.Limit)
		wait := time.Duration(0)
		switch {
		case err != nil:
			if ctx.Err() == nil {
				slog.Error("worker poll failed", "error", err)
			}
			wait = 2 * time.Second
		case n == 0:
			wait = w.Idle
		}
		if wait > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(wait):
			}
		}
	}
}

func (w *Worker) safeSweep(ctx context.Context, s Sweep) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return s.Run(ctx, w)
}

// Claim takes up to limit due jobs of this worker's kinds, exactly like Rust's drain_once (core.claim_jobs).
func (w *Worker) Claim(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, limit int) ([]jobs.Job, error) {
	rows, err := q.Query(ctx,
		"select id, workspace_id::text, kind, payload, attempts, max_attempts from core.claim_jobs($1::text[], $2)", w.Kinds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []jobs.Job
	for rows.Next() {
		var j jobs.Job
		if err := rows.Scan(&j.ID, &j.WorkspaceID, &j.Kind, &j.Payload, &j.Attempts, &j.MaxAttempts); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// DrainOnce claims and runs up to limit jobs and returns how many were claimed.
func (w *Worker) DrainOnce(ctx context.Context, limit int) (int, error) {
	list, err := w.Claim(ctx, w.Pool, limit)
	if err != nil {
		return 0, err
	}
	for _, j := range list {
		if err := w.RunClaimed(ctx, j); err != nil {
			return len(list), err
		}
	}
	return len(list), nil
}

// RunClaimed runs one already-claimed job and records the outcome (done, or retry with exponential backoff,
// or failed once attempts reach max_attempts).
func (w *Worker) RunClaimed(ctx context.Context, j jobs.Job) error {
	result := w.safeRun(ctx, j)
	var tx pgx.Tx
	var err error
	if j.WorkspaceID != nil {
		tx, err = db.BeginTenant(ctx, w.Pool, *j.WorkspaceID)
	} else {
		tx, err = w.Pool.Begin(ctx)
	}
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if result == nil {
		_, err = tx.Exec(ctx, "update core.jobs set status = 'done', updated_at = now() where id = $1", j.ID)
	} else {
		slog.Warn("job failed", "job", j.ID, "kind", j.Kind, "error", result.Error())
		status := "queued"
		if j.Attempts >= j.MaxAttempts {
			status = "failed"
		}
		_, err = tx.Exec(ctx,
			`update core.jobs set status = $2, last_error = $3, updated_at = now(),
			   run_at = now() + make_interval(secs => power(2, attempts)) where id = $1`, j.ID, status, result.Error())
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (w *Worker) safeRun(ctx context.Context, j jobs.Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return w.runJob(ctx, j)
}

func (w *Worker) runJob(ctx context.Context, j jobs.Job) error {
	if j.WorkspaceID == nil {
		return errors.New("job without workspace")
	}
	ws := *j.WorkspaceID
	tx, err := db.BeginTenant(ctx, w.Pool, ws)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	c := &command.Ctx{Context: ctx, Tx: tx, Principal: command.Principal{Kind: command.KindSystem, WorkspaceID: ws, Role: command.Owner}}
	if w.Registry != nil {
		w.Registry.Bind(c)
	}
	if err := w.Handle(c, j); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Handle runs the job inside an open tenant transaction (system principal, role owner). Tests drive it directly
// so nothing is committed to the shared queue.
func (w *Worker) Handle(c *command.Ctx, j jobs.Job) error {
	if j.Kind == "event.dispatch" {
		return w.dispatch(c, j.Payload)
	}
	h, ok := w.jobs[j.Kind]
	if !ok {
		slog.Warn("no handler for job kind", "kind", j.Kind)
		return nil
	}
	if err := h(c, j.Payload); err != nil {
		return fmt.Errorf("%s: %w", j.Kind, err)
	}
	return nil
}

func (w *Worker) dispatch(c *command.Ctx, payload json.RawMessage) error {
	var p struct {
		EventID int64 `json:"event_id"`
	}
	_ = json.Unmarshal(payload, &p)
	e, err := LoadEvent(c, p.EventID)
	if err != nil {
		return err
	}
	if e == nil {
		return nil
	}
	// Handlers run as the system principal, attributed to the human behind the event when there is one.
	c.Principal.UserID = e.OnBehalfOf
	if c.Principal.UserID == nil && e.ActorID != nil && e.ActorType == "user" {
		if isUUID(*e.ActorID) {
			c.Principal.UserID = e.ActorID
		}
	}
	for _, h := range w.events {
		if strings.HasPrefix(e.Kind, h.prefix) {
			if err := h.h(c, *e); err != nil {
				return fmt.Errorf("%s: %w", h.prefix, err)
			}
		}
	}
	return nil
}

// LoadEvent reads a core.events row (nil when it does not exist).
func LoadEvent(c *command.Ctx, id int64) (*jobs.Event, error) {
	var e jobs.Event
	err := c.Tx.QueryRow(c.Context,
		`select id, workspace_id::text, note_id::text, kind, actor_type, actor_id, on_behalf_of::text, payload, created_at
		 from core.events where id = $1`, id).
		Scan(&e.ID, &e.WorkspaceID, &e.NoteID, &e.Kind, &e.ActorType, &e.ActorID, &e.OnBehalfOf, &e.Payload, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &e, err
}

// isUUID accepts the canonical 36-char form Rust's Uuid::parse_str accepts for ids we store (plus the other
// spellings it takes are never written to actor_id).
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if r != '-' {
				return false
			}
		case !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'):
			return false
		}
	}
	return true
}

// Boolish parses a flag-like env value the way clap does (1, true, yes, on, y, t).
func Boolish(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "y", "t":
		return true
	}
	return false
}
