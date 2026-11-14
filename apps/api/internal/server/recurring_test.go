package server_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/modules/recurring"
)

func (b *bt) recurring(start string, auto bool) string {
	return b.ok("recurring.create", map[string]any{"space_id": b.space, "contact_id": b.contact, "frequency": "monthly", "start_date": start, "auto_issue": auto, "lines": []any{map[string]any{"description": "Retainer", "unit_price_minor": 10000}}})["id"].(string)
}
func TestRecurringLifecycle(t *testing.T) {
	b := newBT(t, false)
	id := b.recurring(day(0), false)
	in := map[string]any{"id": id}
	eq(t, b.ok("recurring.update", map[string]any{"id": id, "interval": 2})["interval"], float64(2))
	eq(t, b.ok("recurring.pause", in)["status"], "paused")
	b.errIs("recurring.run_now", in, 409, "conflict")
	b.ok("recurring.resume", in)
	s := b.ok("recurring.run_now", in)
	inv := s["last_invoice_id"].(string)
	eq(t, b.ok("invoice.get", map[string]any{"id": inv})["status"], "draft")
	eq(t, b.ok("invoice.get", map[string]any{"id": inv})["invoice_date"], day(0))
	b.errIs("recurring.run_now", in, 409, "conflict")
	b.errIs("recurring.update", map[string]any{"id": id, "start_date": day(1)}, 409, "conflict")
	eq(t, b.ok("recurring.end", in)["status"], "ended")
	b.errIs("recurring.resume", in, 409, "conflict")
	for _, k := range []string{"recurring.created", "recurring.generated", "recurring.paused", "recurring.ended"} {
		eq(t, count(b.events(id), k), 1)
	}
	o := b.env.tenant(false)
	o.errIs("recurring.get", in, 404, "not_found")
	eq(t, len(o.okList("recurring.list", map[string]any{})), 0)
	guest := b.addMember(b.owner, b.signup("Guest", false), "guest")
	b.errAs(guest, "recurring.create", map[string]any{}, 403, "forbidden")
}
func TestRecurringSweepCatchupConcurrentAndAutoIssue(t *testing.T) {
	b := newBT(t, false)
	id := b.recurring("2030-01-31", true)
	fixed, _ := time.Parse(time.DateOnly, "2031-02-28")
	// Fixtures are ended after generation so subsequent fixed-time sweeps remain repeatable.
	reg := modules.Registry()
	var wg sync.WaitGroup
	// Hold the schedule lock: another API instance must skip it without blocking.
	tx, e := db.BeginTenant(context.Background(), b.pool, b.owner.ws)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(context.Background(), `select note_id from billing.recurring_invoices where note_id=$1::uuid for update`, id); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if e := recurring.Sweep(ctx, b.pool, reg, fixed); e != nil {
		t.Fatal(e)
	}
	eq(t, count(b.events(id), "recurring.generated"), 0)
	_ = tx.Rollback(context.Background())
	if e := recurring.Sweep(context.Background(), b.pool, reg, fixed); e != nil {
		t.Fatal(e)
	}
	eq(t, b.ok("recurring.get", map[string]any{"id": id})["next_run"], "2031-01-31")
	eq(t, count(b.events(id), "recurring.generated"), 12)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := recurring.Sweep(context.Background(), b.pool, reg, fixed); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	s := b.ok("recurring.get", map[string]any{"id": id})
	// Concurrent workers must consume the remaining two periods exactly once.
	if s["next_run"] != "2031-03-31" {
		t.Fatalf("next run %v", s)
	}
	if e := recurring.Sweep(context.Background(), b.pool, reg, fixed); e != nil {
		t.Fatal(e)
	}
	eq(t, b.ok("recurring.get", map[string]any{"id": id})["next_run"], "2031-03-31")
	eq(t, count(b.events(id), "recurring.generated"), 14)
	inv := b.ok("invoice.get", map[string]any{"id": s["last_invoice_id"]})
	eq(t, inv["status"], "issued")
	b.ok("recurring.end", map[string]any{"id": id})
}
func TestRecurringSweepEndAndRollback(t *testing.T) {
	b := newBT(t, false)
	id := b.recurring("2032-01-31", false)
	b.ok("recurring.update", map[string]any{"id": id, "end_date": "2032-02-29"})
	fixed, _ := time.Parse(time.DateOnly, "2032-03-31")
	if e := recurring.Sweep(context.Background(), b.pool, modules.Registry(), fixed); e != nil {
		t.Fatal(e)
	}
	s := b.ok("recurring.get", map[string]any{"id": id})
	eq(t, s["status"], "ended")
	eq(t, count(b.events(id), "recurring.generated"), 2)
	// An issue failure rolls back the draft and leaves next_run unchanged.
	bad := b.recurring("2033-01-31", true)
	b.ok("workspace.update", map[string]any{"settings": map[string]any{"gstin": ""}})
	fixed, _ = time.Parse(time.DateOnly, "2033-01-31")
	if e := recurring.Sweep(context.Background(), b.pool, modules.Registry(), fixed); e == nil {
		t.Fatal("expected GST configuration failure")
	}
	eq(t, b.ok("recurring.get", map[string]any{"id": bad})["next_run"], "2033-01-31")
	eq(t, count(b.events(bad), "recurring.generated"), 0)
	b.ok("recurring.end", map[string]any{"id": bad})
}

func TestRecurringMachineConfirmation(t *testing.T) {
	b := newBT(t, false)
	b.ok("workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "manage_invoices"}}})
	a := b.ok("agent.create", map[string]any{"name": "Recurring operator", "scopes": []string{"read", "manage_invoices"}})
	agent := who{ws: b.owner.ws, bearer: a["api_key"].(string)}
	id := b.recurring(day(0), true)
	in := map[string]any{"id": id}
	r := b.okAs(agent, "recurring.run_now", in)
	eq(t, r["confirmation_required"], true)
	eq(t, count(b.events(id), "recurring.generated"), 0)
	cid := r["confirmation_id"].(string)
	b.ok("confirmation.decide", map[string]any{"id": cid, "approve": true})
	out := b.cmd(b.gosv.URL, agent, b.owner.ws, "recurring.run_now", in, map[string]string{"X-Confirmation-Id": cid})
	if out.status != 200 {
		t.Fatalf("approved run: %d %s", out.status, out.body)
	}
	inv := out.json()["last_invoice_id"].(string)
	eq(t, b.ok("invoice.get", map[string]any{"id": inv})["status"], "draft")
	eq(t, count(b.events(id), "recurring.issue_confirmation_required"), 1)
	b.ok("recurring.end", in)
}
