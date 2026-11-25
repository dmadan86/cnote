package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/modules/billing"
)

func (e *env) sync(s side) {
	e.t.Helper()
	if x, ok := s.(*goSide); ok {
		x.drain(500)
	}
}

func str(v any, k string) string { return v.(map[string]any)[k].(string) }

func TestParseBindings(t *testing.T) {
	id := "0b2ed6f4-0d63-4a56-98a6-4c0b3b4f5a11"
	md := "See ENG-12 and [[Deploy Guide]] ([[Deploy Guide|again]]).\n" +
		"Link: cnote://acme/space/x/note/" + id + "\n" +
		"Code: repo:acme/api/src/lib.rs, PR https://github.com/acme/api/pull/9.\n" +
		"Docs at https://example.com/a?b=1, ENG-12 again."
	want := []binding{{bItemKey, "ENG-12"}, {bWikiLink, "Deploy Guide"}, {bNoteLink, id}, {bRepo, "acme/api/src/lib.rs"},
		{bGithub, "https://github.com/acme/api/pull/9"}, {bURL, "https://example.com/a?b=1"}}
	if got := parseBindings(md); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	// keys inside urls and lowercase are ignored
	if got := parseBindings("https://jira.example.com/browse/ENG-1 eng-3 A-1 ENG-0"); fmt.Sprint(got) != fmt.Sprint([]binding{{bURL, "https://jira.example.com/browse/ENG-1"}}) {
		t.Fatalf("got %v", got)
	}
	// Rust's \b is Unicode aware: a key glued to a letter or a mark is not a key, and neither is a repo: prefix.
	for _, s := range []string{"éENG-12", "ENG-12é", "ENG-12_", "Ωrepo:a/b", "XENG-1x", "ENG-1٣"} {
		for _, b := range parseBindings(s) {
			if b.kind == bItemKey || b.kind == bRepo {
				t.Errorf("%q must not bind %v", s, b)
			}
		}
	}
	if got := parseBindings("x ENG-12 y"); len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	// \s is Unicode White_Space: NBSP ends a URL like a space does.
	if got := parseBindings("https://example.com/a ENG-5"); fmt.Sprint(got) != fmt.Sprint([]binding{{bURL, "https://example.com/a"}, {bItemKey, "ENG-5"}}) {
		t.Fatalf("got %v", got)
	}
	// U+0130 lowercases to two chars in Rust, so this host is not github.com.
	if got := parseBindings("https://GİTHUB.com/a/b"); got[0].kind != bURL {
		t.Fatalf("got %v", got)
	}
}

func TestRustLower(t *testing.T) {
	for in, want := range map[string]string{"ΑΣ": "ας", "ΑΣΑ": "ασα", "Σ": "σ", "İ": "i̇", "ÀB": "àb", "Σ.": "σ."} {
		if got := rustLower(in); got != want {
			t.Errorf("rustLower(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSubst(t *testing.T) {
	v, _ := decodeAny([]byte(`{"a":["{{item.key}} x",1],"b":{"c":"{{item.id}}"}}`))
	id := "00000000-0000-0000-0000-000000000000"
	b, _ := json.Marshal(subst(v, &id, "ENG-1"))
	if string(b) != `{"a":["ENG-1 x",1],"b":{"c":"`+id+`"}}` {
		t.Fatal(string(b))
	}
}

func TestPagesCannotBindToNotesTheirAuthorCannotRead(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Tess")
	member := e.signup("Mo")
	e.apiCmd(w, "member.add", map[string]any{"email": member.email, "role": "member"})
	e.settle(w.ws)
	g := e.newGoSide(w)
	priv := str(g.cmd("space.create", map[string]any{"key": "HID", "name": "HID", "visibility": "private", "template": "software"}), "id")
	secret := g.cmd("item.create", map[string]any{"space_id": priv, "title": "Secret"})
	open := str(g.cmd("space.create", map[string]any{"key": "OPN", "name": "OPN", "visibility": "open"}), "id")
	g.user = command.Principal{Kind: command.KindUser, UserID: &member.userID, WorkspaceID: w.ws, Role: command.Member}
	page := str(g.cmd("note.create", map[string]any{"space_id": open, "title": "Notes", "content_md": "Tracking HID-1 closely"}), "id")
	g.drain(100)
	var bound int
	if err := g.c.Tx.QueryRow(g.c.Context, "select count(*) from core.relations where from_id = $1::uuid and to_id = $2::uuid and kind = 'binds'", page, str(secret, "id")).Scan(&bound); err != nil {
		t.Fatal(err)
	}
	if bound != 0 {
		t.Fatal("member must not bind to an item in a private space")
	}
}

// An automation that keeps firing is blocked after 10 runs in a minute (Go only: the cut-off depends on timing, so
// it cannot be compared row by row with the Rust worker).
func TestAutomationLoopProtection(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Tess")
	e.settle(w.ws)
	g := e.newGoSide(w)
	sp := str(g.cmd("space.create", map[string]any{"key": "ENG", "name": "Engineering", "template": "software"}), "id")
	a := str(g.cmd("automation.create", map[string]any{"space_id": sp, "name": "noisy", "trigger": map[string]any{"kind": "event", "event": "item.created"},
		"actions": []any{map[string]any{"type": "comment", "body": "x"}}}), "id")
	for n := 0; n < 12; n++ {
		g.cmd("item.create", map[string]any{"space_id": sp, "title": fmt.Sprintf("n%d", n), "item_type": "story"})
	}
	g.drain(500)
	var ok, blocked int
	_ = g.c.Tx.QueryRow(g.c.Context, "select count(*) filter (where status = 'success'), count(*) filter (where status = 'loop_blocked') from projects.automation_runs where automation_id = $1::uuid", a).Scan(&ok, &blocked)
	if ok != 10 || blocked != 2 {
		t.Fatalf("success %d blocked %d", ok, blocked)
	}
}

func TestScheduledAutomationsSweep(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Tess")
	e.settle(w.ws)
	g := e.newGoSide(w)
	sp := str(g.cmd("space.create", map[string]any{"key": "ENG", "name": "Engineering", "template": "software"}), "id")
	g.cmd("item.create", map[string]any{"space_id": sp, "title": "stale", "priority": "low"})
	hot := g.cmd("item.create", map[string]any{"space_id": sp, "title": "hot", "priority": "urgent"})
	a := str(g.cmd("automation.create", map[string]any{"space_id": sp, "name": "nag urgent",
		"trigger": map[string]any{"kind": "schedule", "every_minutes": 60, "cnql": "priority = urgent AND statusCategory != Done"},
		"actions": []any{map[string]any{"type": "comment", "body": "still urgent: {{item.key}}"}}}), "id")
	g.c.Principal = command.Principal{Kind: command.KindSystem, WorkspaceID: w.ws, Role: command.Owner}
	fired, err := SweepAutomation(g.c, a)
	if err != nil || !fired {
		t.Fatalf("first sweep: %v %v", fired, err)
	}
	if fired, err = SweepAutomation(g.c, a); err != nil || fired {
		t.Fatalf("second sweep must not fire (not due again yet): %v %v", fired, err)
	}
	var body, status string
	if err := g.c.Tx.QueryRow(g.c.Context, "select body from core.comments where note_id = $1::uuid", str(hot, "id")).Scan(&body); err != nil || body != "still urgent: ENG-2" {
		t.Fatalf("comment %q %v", body, err)
	}
	var n int
	_ = g.c.Tx.QueryRow(g.c.Context, "select count(*) from core.comments").Scan(&n)
	_ = g.c.Tx.QueryRow(g.c.Context, "select status from projects.automation_runs where automation_id = $1::uuid", a).Scan(&status)
	if n != 1 || status != "success" {
		t.Fatalf("comments %d status %s", n, status)
	}
}

// The run-command action is gone: the API refuses to store one, and an old rule that still has one fails
// with "unknown action" without running anything (the migration disables such rules; this is the backstop).
func TestAutomationHasNoCommandAction(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Tess")
	e.settle(w.ws)
	g := e.newGoSide(w)
	sp := str(g.cmd("space.create", map[string]any{"key": "ENG", "name": "Engineering", "template": "software"}), "id")
	g.cmd("item.create", map[string]any{"space_id": sp, "title": "work"})
	in := map[string]any{"space_id": sp, "name": "cmd",
		"trigger": map[string]any{"kind": "event", "event": "item.assigned"},
		"actions": []any{map[string]any{"type": "command", "name": "space.create", "input": map[string]any{"key": "ZZZ", "name": "Z", "template": "software"}}}}
	g.c.Principal = g.user
	if _, err := g.c.Call("automation.create", in); err == nil || !strings.Contains(err.Error(), "run-command") {
		t.Fatalf("automation.create must refuse a command action, got %v", err)
	}
	out, err := g.insertAutomation(in) // an old row
	if err != nil {
		t.Fatal(err)
	}
	var row struct{ ID string }
	_ = json.Unmarshal(out, &row)
	g.cmd("item.assign", map[string]any{"key": "ENG-1", "user_id": w.userID})
	g.drain(100)
	var status, log string
	if err := g.c.Tx.QueryRow(g.c.Context, "select status, log::text from projects.automation_runs where automation_id = $1::uuid", row.ID).Scan(&status, &log); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = g.c.Tx.QueryRow(g.c.Context, "select count(*) from projects.spaces where key = 'ZZZ'").Scan(&n)
	if status != "failed" || !strings.Contains(log, "unknown action") || n != 0 {
		t.Fatalf("command action must not run: %s %s spaces=%d", status, log, n)
	}
}

func TestFreshnessSweepLapsesAndGraveyard(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Tess")
	e.settle(w.ws)
	g := e.newGoSide(w)
	sp := str(g.cmd("space.create", map[string]any{"key": "DOC", "name": "Docs", "visibility": "open"}), "id")
	page := str(g.cmd("note.create", map[string]any{"space_id": sp, "title": "Onboarding", "content_md": "zebra onboarding checklist"}), "id")
	old := str(g.cmd("note.create", map[string]any{"space_id": sp, "title": "Old import", "content_md": "x"}), "id")
	newer := str(g.cmd("note.create", map[string]any{"space_id": sp, "title": "New import", "content_md": "y"}), "id")
	sweep := func(days int) {
		g.c.Principal = command.Principal{Kind: command.KindSystem, WorkspaceID: w.ws, Role: command.Owner}
		if err := FreshnessSweepWorkspace(g.c, time.Now().UTC().AddDate(0, 0, days)); err != nil {
			t.Fatal(err)
		}
	}
	state := func(id string) (string, int) {
		var f string
		var l int
		if err := g.c.Tx.QueryRow(g.c.Context, "select freshness, lapse_count from core.notes where id = $1::uuid", id).Scan(&f, &l); err != nil {
			t.Fatal(err)
		}
		return f, l
	}
	sweep(1)
	if f, _ := state(page); f != "fresh" {
		t.Fatal("not yet due:", f)
	}
	sweep(91)
	if f, l := state(page); f != "stale" || l != 1 {
		t.Fatal("first lapse:", f, l)
	}
	var kinds string
	_ = g.c.Tx.QueryRow(g.c.Context, "select string_agg(kind || ':' || payload::text, ' ' order by id) from core.events where note_id = $1::uuid and kind like 'doc.%'", page).Scan(&kinds)
	if !strings.Contains(kinds, "doc.lapsed") || !strings.Contains(kinds, `"lapse_count": 1`) {
		t.Fatal(kinds)
	}
	var reason string
	_ = g.c.Tx.QueryRow(g.c.Context, "select reason from docs.freshness_flags where note_id = $1::uuid", page).Scan(&reason)
	if reason != "date_lapsed" {
		t.Fatal(reason)
	}
	sweep(91) // same day again: the grace window has not elapsed
	if f, _ := state(page); f != "stale" {
		t.Fatal("grace:", f)
	}
	sweep(91 + 15)
	if f, l := state(page); f != "archived" || l != 2 {
		t.Fatal("second lapse:", f, l)
	}
	// unclaimed imports are archived after 90 days; fresh ones are not
	if _, err := g.c.Tx.Exec(g.c.Context, "update core.notes set freshness = 'unverified', created_at = now() - interval '91 days' where id = $1::uuid", old); err != nil {
		t.Fatal(err)
	}
	if _, err := g.c.Tx.Exec(g.c.Context, "update core.notes set freshness = 'unverified' where id = $1::uuid", newer); err != nil {
		t.Fatal(err)
	}
	sweep(0)
	if f, _ := state(old); f != "archived" {
		t.Fatal("old import:", f)
	}
	if f, _ := state(newer); f != "unverified" {
		t.Fatal("new import:", f)
	}
}

func TestIRNWindowSweep(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Tess")
	e.settle(w.ws)
	g := e.newGoSide(w)
	g.cmd("workspace.update", map[string]any{"settings": map[string]any{"gstin": billing.CompleteGSTIN("29ABCDE1234F1Z"), "state_code": "29", "aato_above_10cr": true}})
	sp := str(g.cmd("space.create", map[string]any{"key": "FIN", "name": "Finance"}), "id")
	contact := str(g.cmd("contact.create", map[string]any{"space_id": sp, "name": "Buyer", "kind": "company", "gstin": billing.CompleteGSTIN("29AABCT1332L1Z")}), "id")
	today := billing.TodayIST()
	mk := func(offset int) string {
		d := str(g.cmd("invoice.create_draft", map[string]any{"space_id": sp, "contact_id": contact, "invoice_date": today.AddDate(0, 0, offset).Format(time.DateOnly),
			"lines": []any{map[string]any{"description": "Consulting", "hsn_sac": "998314", "quantity": 2, "unit_price_minor": 40001}}}), "id")
		g.cmd("invoice.issue", map[string]any{"id": d})
		return d
	}
	d20, d29, fresh := mk(-20), mk(-29), mk(0)
	g.c.Principal = command.Principal{Kind: command.KindSystem, WorkspaceID: w.ws, Role: command.Owner}
	warns := func(id string) int {
		var n int
		_ = g.c.Tx.QueryRow(g.c.Context, "select count(*) from core.events where note_id = $1::uuid and kind = 'invoice.irn_window_warning'", id).Scan(&n)
		return n
	}
	n, err := IRNWarnWorkspace(g.c, today)
	if err != nil || n != 2 {
		t.Fatalf("first sweep warned %d (%v), want the day-20 and the day-29 invoice", n, err)
	}
	if n, err = IRNWarnWorkspace(g.c, today); err != nil || n != 0 {
		t.Fatalf("second sweep warned %d (%v): once per invoice", n, err)
	}
	if warns(d29) != 1 || warns(d20) != 1 || warns(fresh) != 0 {
		t.Fatal("warnings:", warns(d29), warns(d20), warns(fresh))
	}
	var payload string
	_ = g.c.Tx.QueryRow(g.c.Context, "select payload::text from core.events where note_id = $1::uuid and kind = 'invoice.irn_window_warning'", d29).Scan(&payload)
	for _, want := range []string{`"days_elapsed": 29`, `"days_left": 1`, `"blocked": true`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("%s missing in %s", want, payload)
		}
	}
}

// Worker loop: outcome bookkeeping of RunClaimed (done, retry with 2^attempts backoff, failed at max_attempts).
func TestRunClaimedOutcomes(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Tess")
	e.settle(w.ws)
	ctx := context.Background()
	insert := func(kind, payload string, attempts, max int) jobs.Job {
		var j jobs.Job
		tx, err := beginTenant(e, w.ws)
		if err != nil {
			t.Fatal(err)
		}
		// status running: the Rust worker only claims queued jobs
		err = tx.QueryRow(ctx, `insert into core.jobs (workspace_id, kind, payload, status, attempts, max_attempts)
			values ($1::uuid,$2,$3::jsonb,'running',$4,$5) returning id, workspace_id::text, kind, payload, attempts, max_attempts`,
			w.ws, kind, payload, attempts, max).Scan(&j.ID, &j.WorkspaceID, &j.Kind, &j.Payload, &j.Attempts, &j.MaxAttempts)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cleanup(e, w.ws, j.ID) })
		return j
	}
	status := func(id int64) (st string, lastErr *string, wait float64) {
		tx, _ := beginTenant(e, w.ws)
		defer tx.Rollback(ctx)
		if err := tx.QueryRow(ctx, "select status, last_error, extract(epoch from run_at - now()) from core.jobs where id = $1", id).Scan(&st, &lastErr, &wait); err != nil {
			t.Fatal(err)
		}
		return
	}
	// an event that does not exist is a no-op, like Rust
	j := insert("event.dispatch", `{"event_id": 999999999999}`, 1, 5)
	if err := e.w.RunClaimed(ctx, j); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := status(j.ID); st != "done" {
		t.Fatal("done expected, got", st)
	}
	// a failing handler: back to queued with last_error and run_at = now + 2^attempts seconds
	j = insert("hook.deliver", `{"hook_id": "nope"}`, 2, 5)
	if err := e.w.RunClaimed(ctx, j); err != nil {
		t.Fatal(err)
	}
	st, le, wait := status(j.ID)
	if st != "queued" || le == nil || !strings.HasPrefix(*le, "hook.deliver: ") || wait < 2 || wait > 4.5 {
		t.Fatalf("retry: %s %v %.1f", st, le, wait)
	}
	// out of attempts: failed
	j = insert("hook.deliver", `{"hook_id": "nope"}`, 5, 5)
	if err := e.w.RunClaimed(ctx, j); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := status(j.ID); st != "failed" {
		t.Fatal("failed expected, got", st)
	}
}

// Claiming uses core.claim_jobs (FOR UPDATE SKIP LOCKED) and only the kinds this worker runs, so it can share the
// queue with other workers: a job claimed by one transaction is invisible to the other, and every kind is taken.
func TestClaimSharesQueue(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Tess")
	e.settle(w.ws)
	ctx := context.Background()
	tx1, err := beginTenant(e, w.ws)
	if err != nil {
		t.Fatal(err)
	}
	defer tx1.Rollback(ctx)
	// run_at in the past, uncommitted: only visible to tx1
	var ev, ai, doc int64
	for kind, dst := range map[string]*int64{"event.dispatch": &ev, "ai.index_note": &ai, "docs.propose_edit": &doc} {
		if err := tx1.QueryRow(ctx, "insert into core.jobs (workspace_id, kind, payload, run_at) values ($1::uuid,$2,'{}', now() - interval '1 day') returning id", w.ws, kind).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.w.Claim(ctx, tx1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	has := map[int64]bool{}
	for _, j := range got {
		has[j.ID] = true
	}
	if !has[ev] || !has[ai] || !has[doc] {
		t.Fatalf("claimed %v, want jobs %d %d %d", has, ev, ai, doc)
	}
	var st string
	_ = tx1.QueryRow(ctx, "select status from core.jobs where id = $1", ev).Scan(&st)
	if st != "running" {
		t.Fatal("claim must mark the job running:", st)
	}
	// a second claim in the same transaction finds nothing of ours: claimed jobs are not claimed twice
	again, err := e.w.Claim(ctx, tx1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range again {
		if j.ID == ev {
			t.Fatal("job claimed twice")
		}
	}
}

// long lanes: insert a committed job of a unique kind (cleaned up after), state read back without a tenant tx.
func laneJob(t *testing.T, e *env, ws, kind string, status string, attempts int) jobs.Job {
	t.Helper()
	ctx := context.Background()
	tx, err := beginTenant(e, ws)
	if err != nil {
		t.Fatal(err)
	}
	var j jobs.Job
	if err := tx.QueryRow(ctx, `insert into core.jobs (workspace_id, kind, status, attempts, run_at)
		values ($1::uuid,$2,$3,$4, now() - interval '1 minute') returning id, workspace_id::text, kind, payload, attempts, max_attempts`,
		ws, kind, status, attempts).Scan(&j.ID, &j.WorkspaceID, &j.Kind, &j.Payload, &j.Attempts, &j.MaxAttempts); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanup(e, ws, j.ID) })
	return j
}

func jobState(t *testing.T, e *env, ws string, id int64) (st string, attempts int, ageSecs float64) {
	t.Helper()
	tx, _ := beginTenant(e, ws)
	defer tx.Rollback(context.Background())
	if err := tx.QueryRow(context.Background(), "select status, attempts, extract(epoch from now() - updated_at) from core.jobs where id = $1", id).Scan(&st, &attempts, &ageSecs); err != nil {
		t.Fatal(err)
	}
	return
}

func TestLongLanesClaimOnlyTheirKinds(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Tess")
	e.settle(w.ws)
	ctx := context.Background()
	kind := fmt.Sprintf("test.long.%d", time.Now().UnixNano())
	wk := newWorker(e.w.Pool, nil)
	wk.OnLong(kind, 2, func(context.Context, jobs.Job) error { return nil })
	lj := laneJob(t, e, w.ws, kind, "queued", 0)
	nj := laneJob(t, e, w.ws, "event.dispatch", "queued", 0)
	// normal drain (Claim) must not take the long kind
	tx, err := e.w.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	got, err := wk.Claim(ctx, tx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	has := map[int64]bool{}
	for _, j := range got {
		has[j.ID] = true
	}
	if has[lj.ID] || !has[nj.ID] {
		t.Fatalf("normal claim: long=%v normal=%v", has[lj.ID], has[nj.ID])
	}
	_ = tx.Rollback(ctx)
	// the lane takes its kind and nothing else
	list, err := claimKinds(ctx, e.w.Pool, []string{kind}, 10)
	if err != nil || len(list) != 1 || list[0].ID != lj.ID {
		t.Fatalf("lane claim: %v %v", list, err)
	}
	if st, _, _ := jobState(t, e, w.ws, nj.ID); st != "queued" {
		t.Fatal("lane claimed a normal job:", st)
	}
	if !strings.Contains(strings.Join(wk.Kinds(), ","), kind) {
		t.Fatal("Kinds must include lane kinds so reclaim covers them")
	}
}

func TestLongLaneRunsAndRecordsOutcome(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Tess")
	e.settle(w.ws)
	kind := fmt.Sprintf("test.long.%d", time.Now().UnixNano())
	wk := newWorker(e.w.Pool, nil)
	wk.Idle = 20 * time.Millisecond
	wk.OnLong(kind, 1, func(ctx context.Context, j jobs.Job) error {
		if id, ok := jobs.JobID(ctx); !ok || id != j.ID {
			return fmt.Errorf("job id missing from ctx")
		}
		return nil
	})
	lj := laneJob(t, e, w.ws, kind, "queued", 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { wk.laneLoop(ctx, kind); close(done) }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st, _, _ := jobState(t, e, w.ws, lj.ID); st == "done" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done
	if st, _, _ := jobState(t, e, w.ws, lj.ID); st != "done" {
		t.Fatal("lane job not done:", st)
	}
}

// 16.7: once the job is reclaimed and claimed again (attempts moves on), the old executor can neither complete it
// nor keep its lease alive; its handler ctx is cancelled.
func TestLongJobFencing(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Tess")
	e.settle(w.ws)
	ctx := context.Background()
	kind := fmt.Sprintf("test.long.%d", time.Now().UnixNano())
	wk := newWorker(e.w.Pool, nil)
	wk.Lease = 300 * time.Millisecond // heartbeat every 100ms
	old := laneJob(t, e, w.ws, kind, "running", 1)
	bump := func() { // reclaim + a second claim by someone else
		tx, _ := beginTenant(e, w.ws)
		defer tx.Commit(ctx)
		if _, err := tx.Exec(ctx, "update core.jobs set attempts = attempts + 1, updated_at = now() - interval '1 hour' where id = $1", old.ID); err != nil {
			t.Fatal(err)
		}
	}
	wk.OnLong(kind, 1, func(c context.Context, j jobs.Job) error {
		bump()
		select { // the heartbeat finds the token gone and cancels us
		case <-c.Done():
			return nil // a lost executor that "succeeds" anyway
		case <-time.After(5 * time.Second):
			return fmt.Errorf("lease loss not signalled")
		}
	})
	if err := wk.RunLong(ctx, old); err != nil {
		t.Fatal(err)
	}
	st, attempts, age := jobState(t, e, w.ws, old.ID)
	if st != "running" || attempts != 2 {
		t.Fatalf("old executor completed a reclaimed job: %s attempts=%d", st, attempts)
	}
	if age < 3000 { // the old heartbeat must not have refreshed the lease
		t.Fatalf("old heartbeat touched the job: age %.0fs", age)
	}
}

func TestLongJobHeartbeatExtendsLease(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Tess")
	e.settle(w.ws)
	kind := fmt.Sprintf("test.long.%d", time.Now().UnixNano())
	wk := newWorker(e.w.Pool, nil)
	wk.Lease = 300 * time.Millisecond
	j := laneJob(t, e, w.ws, kind, "running", 1)
	var seen float64
	wk.OnLong(kind, 1, func(context.Context, jobs.Job) error {
		tx, _ := beginTenant(e, w.ws)
		_, _ = tx.Exec(context.Background(), "update core.jobs set updated_at = now() - interval '1 hour' where id = $1", j.ID)
		_ = tx.Commit(context.Background())
		time.Sleep(600 * time.Millisecond)
		_, _, seen = jobState(t, e, w.ws, j.ID)
		return nil
	})
	if err := wk.RunLong(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if seen > 60 {
		t.Fatalf("lease not extended while running: age %.0fs", seen)
	}
	if st, _, _ := jobState(t, e, w.ws, j.ID); st != "done" {
		t.Fatal("expected done:", st)
	}
}

// Migration 1331: search_plain is filled by a trigger on insert and edit, and by the sweep for rows that predate it.
func TestSearchPlainTriggerAndBackfill(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Sia")
	e.settle(w.ws)
	g := e.newGoSide(w)
	sp := str(g.cmd("space.create", map[string]any{"key": "SRC", "name": "Src", "visibility": "open"}), "id")
	page := str(g.cmd("note.create", map[string]any{"space_id": sp, "title": "Plan", "content_md": "> [!WARNING]\n> quokka onboarding"}), "id")
	var filled bool
	q := func(sql string, args ...any) bool {
		var ok bool
		if err := g.c.Tx.QueryRow(g.c.Context, sql, args...).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !q("select search_plain is not null and search_plain @@ plainto_tsquery('english','quokka') and not search_plain @@ plainto_tsquery('simple','warning') from core.notes where id = $1::uuid", page) {
		t.Fatal("trigger did not fill search_plain on insert, or the marker was indexed")
	}
	img := str(g.cmd("note.create", map[string]any{"space_id": sp, "title": "Img", "content_md": `![chart \[Q1\] here](https://ex.test/zzurl.png "ttitle \"x\"")`}), "id")
	if !q("select search_plain @@ plainto_tsquery('english','chart') and not search_plain @@ plainto_tsquery('simple','zzurl') and not search_plain @@ plainto_tsquery('simple','ttitle') from core.notes where id = $1::uuid", img) {
		t.Fatal("image alt (with escaped brackets) must be indexed, its url and title not")
	}
	g.cmd("note.update", map[string]any{"id": page, "content_md": "wombat only"})
	if !q("select search_plain @@ plainto_tsquery('english','wombat') and not search_plain @@ plainto_tsquery('english','quokka') from core.notes where id = $1::uuid", page) {
		t.Fatal("trigger did not refresh search_plain on edit")
	}
	// rows that predate the column: null until the sweep
	var many []string
	for i := 0; i < 5; i++ {
		many = append(many, str(g.cmd("note.create", map[string]any{"space_id": sp, "title": fmt.Sprintf("Old %d", i), "content_md": "legacy numbat"}), "id"))
	}
	if _, err := g.c.Tx.Exec(g.c.Context, "update core.notes set search_plain = null where id = any($1::text[]::uuid[])", many); err != nil {
		t.Fatal(err)
	}
	if err := g.c.Tx.Commit(g.c.Context); err != nil {
		t.Fatal(err)
	}
	// a batch size of 2 forces several batches; the workspace's other pages are filled too
	n, err := SearchPlainBackfillRun(context.Background(), e.w, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	if n < 5 {
		t.Fatalf("backfilled %d rows, want at least 5", n)
	}
	tx, err := db.BeginTenant(context.Background(), e.pool, w.ws)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	if err := tx.QueryRow(context.Background(), "select exists (select 1 from core.notes where id = any($1::text[]::uuid[]) and search_plain is null)", many).Scan(&filled); err != nil {
		t.Fatal(err)
	}
	if filled {
		t.Fatal("rows still unfilled after the sweep")
	}
}

// Migration 1334 (docs A2): pages deleted before it get one deletion_id each from the sweep, in small batches, and keep
// no restore evidence. New deletions and live pages are left alone.
func TestA2DeletionBackfill(t *testing.T) {
	e := newEnv(t)
	// the pages live in the open transaction of newGoSide, which a gateway cannot see: a stand-in gateway accepts the fence
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) }))
	defer gw.Close()
	t.Setenv("COLLAB_URL", gw.URL)
	t.Setenv("COLLAB_INTERNAL_SECRET", "x")
	w := e.signup("Bea")
	e.settle(w.ws)
	g := e.newGoSide(w)
	sp := str(g.cmd("space.create", map[string]any{"key": "BKF", "name": "Bkf", "visibility": "open"}), "id")
	var old []string
	for i := 0; i < 5; i++ {
		old = append(old, str(g.cmd("note.create", map[string]any{"space_id": sp, "title": fmt.Sprintf("Old %d", i)}), "id"))
	}
	live := str(g.cmd("note.create", map[string]any{"space_id": sp, "title": "Live"}), "id")
	fresh := str(g.cmd("note.create", map[string]any{"space_id": sp, "title": "Fresh"}), "id")
	g.cmd("note.delete", map[string]any{"id": fresh})                                                                                       // a deletion made by A2 code already has its columns
	if _, err := g.c.Tx.Exec(g.c.Context, "update core.notes set deleted_at = now() where id = any($1::text[]::uuid[])", old); err != nil { // the pre-A2 delete
		t.Fatal(err)
	}
	if err := g.c.Tx.Commit(g.c.Context); err != nil {
		t.Fatal(err)
	}
	n, err := A2DeletionBackfillRun(context.Background(), e.w, 2, 100) // batches of 2
	if err != nil {
		t.Fatal(err)
	}
	if n < 5 {
		t.Fatalf("backfilled %d", n)
	}
	tx, err := db.BeginTenant(context.Background(), e.pool, w.ws)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	var distinct, nulls, liveSet, freshKept int
	if err := tx.QueryRow(context.Background(),
		`select count(distinct deletion_id), count(*) filter (where deleted_restricted_by is null),
		   (select count(*) from core.notes where id = $2::uuid and deletion_id is not null),
		   (select count(*) from core.notes where id = $3::uuid and deleted_restricted_by is not null) from core.notes where id = any($1::text[]::uuid[])`, old, live, fresh).Scan(&distinct, &nulls, &liveSet, &freshKept); err != nil {
		t.Fatal(err)
	}
	if distinct != 5 || nulls != 5 || liveSet != 0 || freshKept != 1 {
		t.Fatalf("distinct %d nulls %d live %d fresh %d", distinct, nulls, liveSet, freshKept)
	}
	if n2, err := A2DeletionBackfillRun(context.Background(), e.w, 2, 100); err != nil || n2 != 0 {
		t.Fatalf("second run: %d %v", n2, err)
	}
}
