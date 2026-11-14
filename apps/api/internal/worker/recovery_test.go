package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/jobs"
)

// A job whose worker died stays 'running'; once its lease (updated_at, refreshed by the heartbeat) is older than the
// lease it is requeued, or failed when attempts are used up. Fresh leases and kinds Go does not run are left alone.
func TestReclaimExpiredLeases(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Tess")
	e.settle(w.ws)
	g := e.newGoSide(w)
	ins := func(kind string, attempts int, age string) int64 {
		var id int64
		if err := g.c.Tx.QueryRow(g.c.Context, `insert into core.jobs (workspace_id, kind, payload, status, attempts, max_attempts, updated_at)
			values ($1::uuid,$2,'{}','running',$3,5, now() - $4::interval) returning id`, w.ws, kind, attempts, age).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	dead, exhausted, alive, ai := ins("event.dispatch", 2, "1 hour"), ins("hook.deliver", 5, "1 hour"), ins("event.dispatch", 1, "1 minute"), ins("ai.index_note", 1, "1 hour")
	n, err := e.w.ReclaimWorkspace(g.c, 10*time.Minute)
	if err != nil || n != 2 {
		t.Fatalf("reclaimed %d %v", n, err)
	}
	st := func(id int64) (s string) {
		_ = g.c.Tx.QueryRow(g.c.Context, "select status from core.jobs where id = $1", id).Scan(&s)
		return
	}
	if st(dead) != "queued" || st(exhausted) != "failed" || st(alive) != "running" || st(ai) != "running" {
		t.Fatal(st(dead), st(exhausted), st(alive), st(ai))
	}
	var le string
	_ = g.c.Tx.QueryRow(g.c.Context, "select last_error from core.jobs where id = $1", exhausted).Scan(&le)
	if le == "" {
		t.Fatal("reclaim must record why")
	}
}

// The heartbeat keeps a long job's lease fresh.
func TestHeartbeatRefreshesLease(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Tess")
	e.settle(w.ws)
	ctx := context.Background()
	wk := newWorker(e.pool, e.reg)
	wk.Lease = 300 * time.Millisecond
	started := make(chan struct{})
	wk.OnJob("worker.test", func(c *command.Ctx, _ json.RawMessage) error {
		close(started)
		time.Sleep(900 * time.Millisecond)
		return nil
	})
	tx, _ := beginTenant(e, w.ws)
	var j jobs.Job
	if err := tx.QueryRow(ctx, `insert into core.jobs (workspace_id, kind, status, attempts, updated_at) values ($1::uuid,'worker.test','running',1, now() - interval '1 hour')
		returning id, workspace_id::text, kind, payload, attempts, max_attempts`, w.ws).Scan(&j.ID, &j.WorkspaceID, &j.Kind, &j.Payload, &j.Attempts, &j.MaxAttempts); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)
	t.Cleanup(func() { cleanup(e, w.ws, j.ID) })
	done := make(chan error)
	go func() { done <- wk.RunClaimed(ctx, j) }()
	<-started
	time.Sleep(500 * time.Millisecond)
	rt, _ := beginTenant(e, w.ws)
	var age float64
	_ = rt.QueryRow(ctx, "select extract(epoch from now() - updated_at) from core.jobs where id = $1", j.ID).Scan(&age)
	_ = rt.Rollback(ctx)
	if age > 5 {
		t.Fatalf("lease not refreshed, age %.0fs", age)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// The effect and the done mark commit in one transaction: a failure after the effect leaves neither; success leaves both.
func TestEffectAndDoneMarkAreAtomic(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Tess")
	e.settle(w.ws)
	ctx := context.Background()
	wk := newWorker(e.pool, e.reg)
	fail := false
	wk.OnJob("worker.test", func(c *command.Ctx, _ json.RawMessage) error {
		if _, err := c.Tx.Exec(c.Context, "insert into core.usage (workspace_id, kind, quantity) values ($1::uuid,'worker.atomic',1)", c.WorkspaceID()); err != nil {
			return err
		}
		if fail {
			return errors.New("crash after the effect")
		}
		return nil
	})
	run := func() (usage int, status string) {
		tx, _ := beginTenant(e, w.ws)
		var j jobs.Job
		if err := tx.QueryRow(ctx, `insert into core.jobs (workspace_id, kind, status, attempts) values ($1::uuid,'worker.test','running',1)
			returning id, workspace_id::text, kind, payload, attempts, max_attempts`, w.ws).Scan(&j.ID, &j.WorkspaceID, &j.Kind, &j.Payload, &j.Attempts, &j.MaxAttempts); err != nil {
			t.Fatal(err)
		}
		_ = tx.Commit(ctx)
		t.Cleanup(func() { cleanup(e, w.ws, j.ID) })
		if err := wk.RunClaimed(ctx, j); err != nil {
			t.Fatal(err)
		}
		rt, _ := beginTenant(e, w.ws)
		defer rt.Rollback(ctx)
		_ = rt.QueryRow(ctx, "select count(*) from core.usage where kind = 'worker.atomic'").Scan(&usage)
		_ = rt.QueryRow(ctx, "select status from core.jobs where id = $1", j.ID).Scan(&status)
		return
	}
	fail = true
	if u, s := run(); u != 0 || s != "queued" {
		t.Fatalf("failed job must leave no effect: usage %d status %s", u, s)
	}
	fail = false
	if u, s := run(); u != 1 || s != "done" {
		t.Fatalf("effect and done together: usage %d status %s", u, s)
	}
}
