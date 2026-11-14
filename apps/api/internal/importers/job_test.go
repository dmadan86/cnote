package importers

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
)

// Ports of crates/cnote-importers/tests/import.rs (the assertions about the import.run job). The Go side runs in
// one rolled-back transaction; import.start/status/list stay in Rust and are not exercised here.

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func num(v any) float64 { f, _ := v.(float64); return f }

func warnHas(report map[string]any, needle string) bool {
	for k := range report["warnings"].(map[string]any) {
		if strings.Contains(k, needle) {
			return true
		}
	}
	return false
}

func TestJiraImportKeepsIdentityHistoryAndRelations(t *testing.T) {
	ten := newEnv(t).newTenant()
	g := ten.goRun()
	id := g.start("jira", ten.fix(fixture(t, "jira_export.json")), nil, nil)
	g.all(id)
	st := g.status(id)
	if st["status"] != "done" {
		t.Fatalf("%v", st)
	}
	r := st["report"].(map[string]any)
	if num(r["imported"]) != 3 || num(r["updated"]) != 0 || num(r["skipped"]) != 0 {
		t.Fatalf("%v", r)
	}
	for k, want := range map[string]float64{"comments": 2, "history_events": 3, "attachments": 1, "views": 2} {
		if num(r[k]) != want {
			t.Errorf("%s = %v want %v", k, r[k], want)
		}
	}
	for _, n := range []string{"schemes are flattened", "unknown field 'customfield_10099'", "JQL not translatable for filter 'My open bugs'",
		"link to ENG-99 skipped", "only the first component", "sprint history flattened", "is not a workspace member", "disabled draft"} {
		if !warnHas(r, n) {
			t.Errorf("missing warning %q in %v", n, r["warnings"])
		}
	}
	if warnHas(r, "Payments backlog") {
		t.Error("supported filter must not warn")
	}

	tx := g.tx
	type item struct {
		key, typ, cat, prio string
		assignee, reporter  *string
		labels              []string
	}
	var it item
	if err := tx.QueryRow(context.Background(),
		`select i.key, i.item_type, i.status_category, i.priority, i.assignee_id::text, i.reporter_id::text, i.labels
		 from projects.items i join core.notes n on n.id = i.note_id where n.source_system = 'jira' and n.source_id = 'ENG-2'`).
		Scan(&it.key, &it.typ, &it.cat, &it.prio, &it.assignee, &it.reporter, &it.labels); err != nil {
		t.Fatal(err)
	}
	if it.key != "ENG-2" || it.typ != "story" || it.cat != "done" || it.prio != "urgent" {
		t.Fatalf("%+v", it)
	}
	if it.assignee == nil || *it.assignee != ten.adaID {
		t.Errorf("assignee matched by email: %v", it.assignee)
	}
	if it.reporter != nil {
		t.Error("bob is not a member")
	}
	if !reflect.DeepEqual(it.labels, []string{"payments", "india"}) {
		t.Errorf("%v", it.labels)
	}
	var sprint, version, component string
	var points float64
	if err := tx.QueryRow(context.Background(),
		`select sp.name, v.name, c.name, i.estimate::float8 from projects.items i
		 join projects.sprints sp on sp.id = i.sprint_id join projects.versions v on v.id = i.fix_version_id
		 join projects.components c on c.id = i.component_id where i.key = 'ENG-2'`).Scan(&sprint, &version, &component, &points); err != nil {
		t.Fatal(err)
	}
	if sprint != "Sprint 2" || version != "1.4" || component != "Payments" || points != 5 {
		t.Fatal(sprint, version, component, points)
	}
	var url, created string
	if err := tx.QueryRow(context.Background(),
		"select source_url, to_char(created_at at time zone 'utc', 'YYYY-MM-DD HH24:MI') from core.notes where source_id = 'ENG-2'").Scan(&url, &created); err != nil {
		t.Fatal(err)
	}
	if url != "https://acme.atlassian.net/browse/ENG-2" || created != "2024-01-11 04:30" {
		t.Fatal(url, created)
	}
	if got := q1[string](t, tx, "select custom->>'source_assignee' from projects.items where key = 'ENG-3'"); got != "Dave Unknown" {
		t.Error(got)
	}
	if got := q1[string](t, tx, "select priority from projects.items where key = 'ENG-3'"); got != "low" {
		t.Error(got)
	}

	// History arrives as import events carrying the original author and time.
	rows, err := tx.Query(context.Background(),
		`select e.payload->>'from', e.payload->>'author' from core.events e join core.notes n on n.id = e.note_id
		 where n.source_id = 'ENG-2' and e.actor_type = 'import' and e.kind = 'item.transitioned' order by e.created_at`)
	if err != nil {
		t.Fatal(err)
	}
	var evs [][2]string
	for rows.Next() {
		var a, b string
		_ = rows.Scan(&a, &b)
		evs = append(evs, [2]string{a, b})
	}
	rows.Close()
	if len(evs) != 2 || evs[0] != [2]string{"To Do", "Ada Example"} {
		t.Fatalf("%v", evs)
	}
	if got := q1[string](t, tx, `select to_char(e.created_at at time zone 'utc', 'YYYY-MM-DD HH24:MI') from core.events e join core.notes n on n.id = e.note_id
		 where n.source_id = 'ENG-2' and e.kind = 'item.transitioned' order by e.created_at limit 1`); got != "2024-01-20 03:30" {
		t.Error(got)
	}
	crows, err := tx.Query(context.Background(),
		"select c.author_name, c.body from core.comments c join core.notes n on n.id = c.note_id where n.source_id = 'ENG-2' order by c.created_at")
	if err != nil {
		t.Fatal(err)
	}
	var cs [][2]string
	for crows.Next() {
		var a, b string
		_ = crows.Scan(&a, &b)
		cs = append(cs, [2]string{a, b})
	}
	crows.Close()
	if len(cs) != 2 || cs[0] != [2]string{"Bob Sample", "Which PSP are we using?"} || cs[1][0] != "Carol Nobody" {
		t.Fatalf("%v", cs)
	}
	if key := q1[string](t, tx, "select a.storage_key from core.attachments a join core.notes n on n.id = a.note_id where n.source_id = 'ENG-2'"); !strings.HasSuffix(key, "/attachment/content/30001") {
		t.Error(key)
	}

	// Relations: epic -child-> story, story -blocks-> bug; nothing for the missing ENG-99.
	rr, err := tx.Query(context.Background(),
		`select a.source_id, r.kind, b.source_id from core.relations r
		 join core.notes a on a.id = r.from_id join core.notes b on b.id = r.to_id order by r.kind`)
	if err != nil {
		t.Fatal(err)
	}
	var rels [][3]string
	for rr.Next() {
		var a, b, c string
		_ = rr.Scan(&a, &b, &c)
		rels = append(rels, [3]string{a, b, c})
	}
	rr.Close()
	if !reflect.DeepEqual(rels, [][3]string{{"ENG-2", "blocks", "ENG-3"}, {"ENG-1", "child", "ENG-2"}}) {
		t.Fatalf("%v", rels)
	}
	if jql := q1[string](t, tx, "select query from core.views where name = 'My open bugs'"); !strings.Contains(jql, "currentUser()") {
		t.Error(jql)
	}
	var draft, enabled bool
	if err := tx.QueryRow(context.Background(), "select draft, enabled from projects.automations").Scan(&draft, &enabled); err != nil || !draft || enabled {
		t.Fatal(err, draft, enabled)
	}
	if seq := q1[int32](t, tx, "select item_seq from core.spaces where key = 'ENG'"); seq != 3 {
		t.Error(seq)
	}
}

func counts(t *testing.T, g *gorun) [7]int64 {
	var out [7]int64
	for i, q := range []string{
		"select count(*) from core.notes", "select count(*) from projects.items", "select count(*) from core.comments",
		"select count(*) from core.events where actor_type = 'import'", "select count(*) from core.relations",
		"select count(*) from core.attachments", "select count(*) from core.views",
	} {
		out[i] = q1[int64](t, g.tx, q)
	}
	return out
}

func TestRerunUpdatesWithoutDuplicating(t *testing.T) {
	ten := newEnv(t).newTenant()
	g := ten.goRun()
	jira := ten.fix(fixture(t, "jira_export.json"))
	g.all(g.start("jira", jira, nil, nil))
	before := counts(t, g)

	changed := strings.NewReplacer("Add UPI payment option", "Add UPI and cards", "Support UPI collect and intent flows.", "Support UPI and card flows.").Replace(jira)
	second := g.start("jira", changed, nil, nil)
	g.all(second)
	r := g.status(second)["report"].(map[string]any)
	if num(r["imported"]) != 0 || num(r["updated"]) != 3 {
		t.Fatalf("%v", r)
	}
	if after := counts(t, g); after != before {
		t.Fatalf("duplicates: %v vs %v", after, before)
	}
	var title, body, key string
	var version int32
	if err := g.tx.QueryRow(context.Background(),
		"select n.title, n.content_md, i.key, n.version from core.notes n join projects.items i on i.note_id = n.id where n.source_id = 'ENG-2'").
		Scan(&title, &body, &key, &version); err != nil {
		t.Fatal(err)
	}
	if title != "Add UPI and cards" || body != "Support UPI and card flows." || key != "ENG-2" || version != 2 {
		t.Fatal(title, body, key, version)
	}
}

func TestImportResumesBatchByBatch(t *testing.T) {
	ten := newEnv(t).newTenant()
	g := ten.goRun()
	id := g.start("jira", ten.fix(fixture(t, "jira_export.json")), nil, 2)
	if !g.batch(id) {
		t.Fatal("first batch")
	}
	st := g.status(id)
	if st["status"] != "running" || !reflect.DeepEqual(st["cursor"], map[string]any{"phase": "records", "offset": float64(2)}) {
		t.Fatalf("%v", st)
	}
	if n := q1[int64](t, g.tx, "select count(*) from projects.items"); n != 2 {
		t.Error(n)
	}
	if n := q1[int64](t, g.tx, "select count(*) from core.relations"); n != 0 {
		t.Error("relations wait for every record:", n)
	}
	g.all(id)
	st = g.status(id)
	if st["status"] != "done" || num(st["report"].(map[string]any)["imported"]) != 3 {
		t.Fatalf("%v", st)
	}
	if n := q1[int64](t, g.tx, "select count(*) from projects.items"); n != 3 {
		t.Error(n)
	}
	if n := q1[int64](t, g.tx, "select count(*) from core.relations"); n != 2 {
		t.Error(n)
	}
	if g.batch(id) {
		t.Error("nothing left to run")
	}
	if n := q1[int64](t, g.tx, "select count(*) from core.events where kind = 'import.completed'"); n != 1 {
		t.Error(n)
	}
	// a finished job is a no-op even when run again
	if err := RunJob(g.ctx, []byte(`{"import_id":"`+id+`"}`)); err != nil {
		t.Fatal(err)
	}
	if n := q1[int64](t, g.tx, "select count(*) from core.events where kind = 'import.completed'"); n != 1 {
		t.Error(n)
	}
}

func TestConfluenceAndNotionPagesStartUnverifiedWithAFlag(t *testing.T) {
	ten := newEnv(t).newTenant()
	g := ten.goRun()
	conf := fixture(t, "confluence_pages.json")
	id := g.start("confluence", conf, nil, nil)
	g.all(id)
	r := g.status(id)["report"].(map[string]any)
	if num(r["imported"]) != 2 || !warnHas(r, "macro") {
		t.Fatalf("%v", r)
	}
	rows, err := g.tx.Query(context.Background(),
		`select n.title, n.freshness, n.content_md, n.parent_id::text,
		   exists(select 1 from docs.freshness_flags f where f.note_id = n.id and f.reason = 'imported' and f.resolved_at is null)
		 from core.notes n where n.source_system = 'confluence' order by n.source_id`)
	if err != nil {
		t.Fatal(err)
	}
	type pg struct {
		title, fresh, md string
		parent           *string
		flagged          bool
	}
	var pages []pg
	for rows.Next() {
		var p pg
		_ = rows.Scan(&p.title, &p.fresh, &p.md, &p.parent, &p.flagged)
		pages = append(pages, p)
	}
	rows.Close()
	if len(pages) != 2 {
		t.Fatal(len(pages))
	}
	for _, p := range pages {
		if p.fresh != "unverified" || !p.flagged {
			t.Errorf("%+v", p)
		}
	}
	if !strings.Contains(pages[0].md, "- Be kind\n- Ship small") || !strings.Contains(pages[0].md, "[policy](https://example.com/policy)") {
		t.Error(pages[0].md)
	}
	if !strings.Contains(pages[1].md, "```bash\nmake deploy ENV=prod\n```") || !strings.Contains(pages[1].md, "| prod | https://example.com |") {
		t.Error(pages[1].md)
	}
	parent := q1[string](t, g.tx, "select id::text from core.notes where source_id = '9001'")
	if pages[1].parent == nil || *pages[1].parent != parent {
		t.Error("hierarchy preserved")
	}
	if n := q1[int64](t, g.tx, "select count(*) from core.notes where source_id = '9002' and verify_by is not null"); n != 1 {
		t.Error("90-day clock is running")
	}

	// Re-running does not add a second flag.
	g.all(g.start("confluence", conf, nil, nil))
	if n := q1[int64](t, g.tx, "select count(*) from docs.freshness_flags"); n != 2 {
		t.Error(n)
	}
	if n := q1[int64](t, g.tx, "select count(*) from core.notes where source_system = 'confluence'"); n != 2 {
		t.Error(n)
	}

	g.all(g.start("notion", "# Onboarding\n\nWelcome to **Acme**.\n\n- step one\n", nil, nil))
	var title, bodyMD, fresh string
	if err := g.tx.QueryRow(context.Background(), "select title, content_md, freshness from core.notes where source_system = 'notion'").Scan(&title, &bodyMD, &fresh); err != nil {
		t.Fatal(err)
	}
	if title != "Onboarding" || fresh != "unverified" || !strings.HasPrefix(bodyMD, "Welcome to **Acme**.") {
		t.Fatal(title, fresh, bodyMD)
	}
}

func TestCSVMappingAndTrelloBoard(t *testing.T) {
	ten := newEnv(t).newTenant()
	g := ten.goRun()
	csv := ten.fix(fixture(t, "tasks.csv"))
	mapping := map[string]any{"id": "Ref", "title": "Summary", "description": "Details", "status": "State", "priority": "Severity",
		"assignee_email": "Owner", "labels": "Tags"}
	g.all(g.start("csv", csv, mapping, nil))
	rows, err := g.tx.Query(context.Background(),
		`select n.source_id, i.status_category, i.priority, i.assignee_id::text, i.labels, i.custom->>'Team'
		 from projects.items i join core.notes n on n.id = i.note_id order by n.source_id`)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		src, cat, prio string
		assignee       *string
		labels         []string
		team           *string
	}
	var rs []row
	for rows.Next() {
		var r row
		_ = rows.Scan(&r.src, &r.cat, &r.prio, &r.assignee, &r.labels, &r.team)
		rs = append(rs, r)
	}
	rows.Close()
	if len(rs) != 3 || rs[0].src != "T-1" || rs[0].cat != "in_progress" || rs[0].prio != "high" ||
		rs[0].assignee == nil || *rs[0].assignee != ten.adaID || !reflect.DeepEqual(rs[0].labels, []string{"auth", "web"}) ||
		rs[0].team == nil || *rs[0].team != "Platform" || rs[1].cat != "done" || rs[1].prio != "low" || rs[2].prio != "urgent" {
		t.Fatalf("%+v", rs)
	}

	// A mapping that names a missing column is refused at parse time (import.start refuses before queueing).
	if _, err := parseInput("csv", map[string]any{"content": csv, "mapping": map[string]any{"title": "Nope"}}); err == nil ||
		err.Error() != "mapped column 'Nope' is not in the CSV header" {
		t.Fatalf("%v", err)
	}

	trello := g.start("trello", fixture(t, "trello_board.json"), nil, nil)
	g.all(trello)
	r := g.status(trello)["report"].(map[string]any)
	if num(r["imported"]) != 2 || num(r["skipped"]) != 1 || num(r["comments"]) != 1 {
		t.Fatalf("%v", r)
	}
	var status, cat, body string
	if err := g.tx.QueryRow(context.Background(),
		"select i.status, i.status_category, n.content_md from projects.items i join core.notes n on n.id = i.note_id where n.source_id = 'c1'").Scan(&status, &cat, &body); err != nil {
		t.Fatal(err)
	}
	if status != "In Progress" || cat != "in_progress" || !strings.Contains(body, "- [x] Logo\n- [ ] Screenshots") {
		t.Fatal(status, cat, body)
	}
	if n := q1[int64](t, g.tx, "select count(*) from core.events where kind = 'item.transitioned' and actor_type = 'import'"); n != 1 {
		t.Error(n)
	}
}

// import.run is the only writer: the job row says who started it and the notes carry that person.
func TestJobAttributionAndFailure(t *testing.T) {
	ten := newEnv(t).newTenant()
	g := ten.goRun()
	id := g.start("notion", "# Page\n\nbody", nil, nil)
	g.all(id)
	if got := q1[string](t, g.tx, "select created_by::text from core.notes where source_id = 'page'"); got != ten.owner {
		t.Errorf("created_by = %s want %s", got, ten.owner)
	}
	// system principal with the starter as on_behalf_of
	if got := q1[string](t, g.tx, "select actor_type || ':' || coalesce(on_behalf_of::text,'') from core.events where kind = 'import.completed'"); got != "system:"+ten.owner {
		t.Error(got)
	}

	// A job whose source no longer parses fails with a report instead of raising.
	bad := g.start("jira", "not json", nil, nil)
	g.all(bad)
	st := g.status(bad)
	if st["status"] != "failed" || !warnHas(st["report"].(map[string]any), "import failed: Jira content is not valid JSON") {
		t.Fatalf("%v", st)
	}
	// A deleted space fails the job too.
	g2 := g.start("notion", "# Other", nil, nil)
	if _, err := g.tx.Exec(context.Background(), "update importers.jobs set space_id = null where id = $1::uuid", g2); err != nil {
		t.Fatal(err)
	}
	g.all(g2)
	if st := g.status(g2); st["status"] != "failed" || !warnHas(st["report"].(map[string]any), "import failed: the target space was deleted") {
		t.Fatalf("%v", st)
	}
}

// Two tenants import the same source ids independently.
func TestTenantIsolation(t *testing.T) {
	e := newEnv(t)
	a, b := e.newTenant(), e.newTenant()
	ga, gb := a.goRun(), b.goRun()
	ga.all(ga.start("jira", a.fix(fixture(t, "jira_export.json")), nil, nil))
	gb.all(gb.start("jira", b.fix(fixture(t, "jira_export.json")), nil, nil))
	for _, g := range []*gorun{ga, gb} {
		if n := q1[int64](t, g.tx, "select count(*) from projects.items"); n != 3 {
			t.Error(n)
		}
	}
}

// A failing record is skipped through its savepoint and reported; the batch carries on.
func TestBadRecordIsSkipped(t *testing.T) {
	ten := newEnv(t).newTenant()
	g := ten.goRun()
	// &#0; decodes to a NUL character, which Postgres text refuses: only that record is lost
	id := g.start("notion", `{"pages":[{"id":"p1","title":"ok","body":"<p>fine</p>"},{"id":"p2","title":"bad","body":"<p>a&#0;b</p>"},{"id":"p3","title":"fine too","body":"<p>x</p>"}]}`, nil, nil)
	g.all(id)
	st := g.status(id)
	r := st["report"].(map[string]any)
	if st["status"] != "done" || num(r["imported"]) != 2 || num(r["skipped"]) != 1 || !warnHas(r, "skipped p2: database error") {
		t.Fatalf("%v", st)
	}
	if n := q1[int64](t, g.tx, "select count(*) from core.notes where source_system = 'notion'"); n != 2 {
		t.Error(n)
	}
}
