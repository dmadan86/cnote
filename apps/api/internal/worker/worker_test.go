package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/modules/billing"
)

func (e *env) sync(s side) {
	e.t.Helper()
	switch x := s.(type) {
	case *goSide:
		x.drain(500)
	case *rustSide:
		e.settle(x.w.ws)
	}
}

func str(v any, k string) string { return v.(map[string]any)[k].(string) }

// twins creates two identical fixtures: a Rust one (the real Rust worker reacts to everything) and a Go one
// (this test plays the Go worker inside a rolled-back transaction).
func (e *env) twins() (*rustSide, *goSide) {
	e.t.Helper()
	rw, gw := e.signup("Tess"), e.signup("Tess")
	e.settle(rw.ws)
	e.settle(gw.ws)
	return &rustSide{e, rw}, e.newGoSide(gw)
}

func (e *env) snap(s side, extra ...string) string {
	if g, ok := s.(*goSide); ok {
		g.c.Principal = command.Principal{Kind: command.KindSystem, WorkspaceID: g.w.ws, Role: command.Owner}
		return snapshot(e.t, g.c, extra...)
	}
	return snapshot(e.t, e.readCtx(s.ws().ws), extra...)
}

func (e *env) sameRows(r *rustSide, g *goSide) {
	e.t.Helper()
	rs, gs := e.snap(r), e.snap(g)
	if rs != gs {
		rl, gl := strings.Split(rs, "\n"), strings.Split(gs, "\n")
		seen := map[string]int{}
		for _, l := range rl {
			seen[l]++
		}
		var onlyGo, onlyRust []string
		for _, l := range gl {
			if seen[l] > 0 {
				seen[l]--
			} else {
				onlyGo = append(onlyGo, l)
			}
		}
		for l, n := range seen {
			for ; n > 0; n-- {
				onlyRust = append(onlyRust, l)
			}
		}
		e.t.Fatalf("Go and Rust worker results differ.\nonly Go:\n%s\nonly Rust:\n%s", strings.Join(onlyGo, "\n"), strings.Join(onlyRust, "\n"))
	}
}

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

// Bindings and freshness: the same page and item edits produce the same relations, bindings, flags, events and
// queued jobs under the Rust worker and under this worker.
func TestDifferentialBindingsAndFreshness(t *testing.T) {
	e := newEnv(t)
	r, g := e.twins()
	run := func(s side) {
		sp := str(s.cmd("space.create", map[string]any{"key": "ENG", "name": "Engineering", "template": "software"}), "id")
		item := s.cmd("item.create", map[string]any{"space_id": sp, "title": "Ship it"})
		target := str(s.cmd("note.create", map[string]any{"space_id": sp, "title": "Pricing", "content_md": "v1"}), "id")
		page := str(s.cmd("note.create", map[string]any{"space_id": sp, "title": "Guide", "content_md": "Tracks ENG-1 and [[Pricing]] cnote://x/note/" + target +
			" see https://github.com/acme/api/pull/9 and https://example.com/x. repo:acme/api/deploy.md ünïcödeENG-2 ENG-3 [[Missing]]"}), "id")
		e.sync(s)
		s.cmd("note.update", map[string]any{"id": target, "content_md": "v2"})
		e.sync(s)
		s.cmd("note.update", map[string]any{"id": target, "content_md": "v3"}) // an open flag exists: no second proposal
		e.sync(s)
		s.cmd("note.update", map[string]any{"id": target, "title": "Pricing 2"}) // title change counts too
		e.sync(s)
		s.cmd("item.transition", map[string]any{"key": str(item, "key"), "to": "In Progress"})
		e.sync(s)
		s.cmd("note.update", map[string]any{"id": page, "content_md": "Tracks ENG-1 only"})
		e.sync(s)
		s.cmd("note.update", map[string]any{"id": page, "title": "Guide 2"}) // title only: bindings are not re-synced
		e.sync(s)
	}
	run(r)
	run(g)
	e.sameRows(r, g)
	// the AI-owned follow-up jobs exist on the Go side too (the Rust worker runs them)
	var n int
	if err := g.c.Tx.QueryRow(g.c.Context, "select count(*) from core.jobs where kind = 'ai.index_note' and id > $1", g.baseline).Scan(&n); err != nil || n < 3 {
		t.Fatalf("ai.index_note jobs: %d %v", n, err)
	}
	if err := g.c.Tx.QueryRow(g.c.Context, "select count(*) from core.jobs where kind = 'docs.propose_edit' and status = 'queued' and id > $1", g.baseline).Scan(&n); err != nil || n < 2 {
		t.Fatalf("docs.propose_edit jobs: %d %v", n, err)
	}
}

func TestPagesCannotBindToNotesTheirAuthorCannotRead(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Tess")
	member := e.signup("Mo")
	e.rustCmd(w, "member.add", map[string]any{"email": member.email, "role": "member"})
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

// Automations: the Rust tests of crates/cnote-projects/tests/projects.rs, run through the Go event handler, then
// the same scenario on a Rust twin to compare every row.
func TestDifferentialAutomations(t *testing.T) {
	e := newEnv(t)
	r, g := e.twins()
	run := func(s side) {
		sp := str(s.cmd("space.create", map[string]any{"key": "ENG", "name": "Engineering", "template": "software"}), "id")
		mk := func(name string, trig map[string]any, conds, actions any, draft bool) map[string]any {
			return map[string]any{"space_id": sp, "name": name, "trigger": trig, "conditions": conds, "actions": actions, "draft": draft}
		}
		comment := []any{map[string]any{"type": "comment", "body": "Triage {{item.key}}"}}
		onCreated := map[string]any{"kind": "event", "event": "item.created", "cnql": "type = bug"}
		s.cmd("automation.create", mk("bug comment", onCreated, []any{}, comment, false))
		draft := str(s.cmd("automation.create", mk("imported", onCreated, []any{}, comment, true)), "id")
		s.cmd("automation.create", mk("urgent only", map[string]any{"kind": "event", "event": "item.created"}, []any{map[string]any{"cnql": "priority = urgent"}}, comment, false))
		s.cmd("automation.create", mk("bad target", map[string]any{"kind": "event", "event": "item.created", "cnql": "type = story"}, []any{}, []any{map[string]any{"type": "transition", "to": "Nowhere"}}, false))
		s.cmd("automation.create", mk("auto start", map[string]any{"kind": "event", "event": "item.assigned", "cnql": `statusCategory = "To Do"`}, []any{},
			[]any{map[string]any{"type": "transition", "to": "In Progress"}, map[string]any{"type": "command", "name": "item.update", "input": map[string]any{"key": "{{item.key}}", "labels": []any{"auto"}}}}, false))
		s.cmd("item.create", map[string]any{"space_id": sp, "title": "crash", "item_type": "bug"})
		s.cmd("item.create", map[string]any{"space_id": sp, "title": "plain", "priority": "urgent"})
		s.cmd("item.create", map[string]any{"space_id": sp, "title": "story", "item_type": "story"})
		e.sync(s)
		s.cmd("item.assign", map[string]any{"key": "ENG-2", "user_id": s.ws().userID})
		e.sync(s)
		s.cmd("automation.update", map[string]any{"id": draft, "draft": false})
		s.cmd("item.create", map[string]any{"space_id": sp, "title": "second crash", "item_type": "bug"})
		e.sync(s)
		s.cmd("automation.update", map[string]any{"id": draft, "enabled": false})
		s.cmd("automation.create", mk("noisy", map[string]any{"kind": "event", "event": "item.created"}, []any{}, comment, false))
		for n := 0; n < 12; n++ {
			s.cmd("item.create", map[string]any{"space_id": sp, "title": fmt.Sprintf("n%d", n), "item_type": "story"})
		}
		e.sync(s)
	}
	run(r)
	run(g)
	e.sameRows(r, g)

	// worker-visible expectations of the Rust test, asserted on the Go side
	counts := map[string]int{}
	rows, err := g.c.Tx.Query(g.c.Context, "select a.name, r.status, count(*) from projects.automation_runs r join projects.automations a on a.id = r.automation_id group by 1,2")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n, st string
		var c int
		_ = rows.Scan(&n, &st, &c)
		counts[n+"/"+st] = c
	}
	rows.Close()
	for k, want := range map[string]int{"bug comment/success": 2, "imported/success": 1, "urgent only/success": 1, "bad target/failed": 10, "noisy/success": 10, "noisy/loop_blocked": 2, "auto start/success": 1} {
		if counts[k] != want {
			t.Errorf("%s = %d, want %d (all: %v)", k, counts[k], want, counts)
		}
	}
	var actor string
	if err := g.c.Tx.QueryRow(g.c.Context, "select actor_type from core.events where kind = 'item.transitioned' order by id desc limit 1").Scan(&actor); err != nil || actor != "system" {
		t.Fatalf("automation changes are attributed to the system, got %q %v", actor, err)
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

// Automation actions run through the command gate with the author's role.
func TestAutomationActionsUseCreatorPermissions(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Tess")
	member := e.signup("Mo")
	e.rustCmd(w, "member.add", map[string]any{"email": member.email, "role": "member"})
	e.settle(w.ws)
	g := e.newGoSide(w)
	sp := str(g.cmd("space.create", map[string]any{"key": "ENG", "name": "Engineering", "template": "software"}), "id")
	g.cmd("item.create", map[string]any{"space_id": sp, "title": "work"})
	// an admin-only action in a rule authored by a member: stored through the owner, then the author is switched
	// to the member, as an imported or edited rule would be
	id := str(g.cmd("automation.create", map[string]any{"space_id": sp, "name": "escalate",
		"trigger": map[string]any{"kind": "event", "event": "item.assigned"},
		"actions": []any{map[string]any{"type": "command", "name": "workflow.update", "input": map[string]any{"space_id": sp, "definition": map[string]any{"statuses": []any{map[string]any{"name": "X", "category": "todo"}}, "transitions": []any{}}}}}}), "id")
	if _, err := g.c.Tx.Exec(g.c.Context, "update projects.automations set created_by = $2::uuid where id = $1::uuid", id, member.userID); err != nil {
		t.Fatal(err)
	}
	count := func() (n int) {
		_ = g.c.Tx.QueryRow(g.c.Context, "select count(*) from projects.workflows").Scan(&n)
		return
	}
	before := count()
	g.cmd("item.assign", map[string]any{"key": "ENG-1", "user_id": w.userID})
	g.drain(100)
	var status, log string
	if err := g.c.Tx.QueryRow(g.c.Context, "select status, log::text from projects.automation_runs where automation_id = $1::uuid", id).Scan(&status, &log); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || !strings.Contains(log, "requires role admin") {
		t.Fatalf("a member's rule must not run an admin command: %s %s", status, log)
	}
	if count() != before {
		t.Fatal("workflow was changed by a member's rule")
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
// queue with the Rust worker: a job claimed by one transaction is invisible to the other, and the AI kinds are
// never taken.
func TestClaimSharesQueueWithRust(t *testing.T) {
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
		if j.Kind == "ai.index_note" || j.Kind == "docs.propose_edit" {
			t.Fatalf("the Go worker must leave %s to Rust", j.Kind)
		}
	}
	if !has[ev] || has[ai] || has[doc] {
		t.Fatalf("claimed %v, want only the event.dispatch job %d", has, ev)
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
