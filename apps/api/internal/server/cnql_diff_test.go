package server_test

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestDifferentialCNQLCorpus runs a large corpus of CNQL queries (valid and invalid) through item.list on the
// Rust and the Go server against the same data and requires identical status codes and identical response bytes
// (so identical error messages, column positions, filtering and ordering).

type cnqlFixture struct {
	owner, member, bob, guest, agent who
	agentID                          string
	data                             map[string]string
}

func (e *env) cnqlFixture() cnqlFixture {
	o := e.signup("Tess Owner", true)
	f := cnqlFixture{owner: o, data: map[string]string{}}
	f.member = e.addMember(o, e.signup("Mia Member", false), "member")
	f.bob = e.addMember(o, e.signup("Bob", false), "member")
	f.guest = e.addMember(o, e.signup("Gus Guest", false), "guest")
	a := e.must(o, "agent.create", map[string]any{"name": "Code Bot"})
	f.agent = who{ws: o.ws, bearer: a["api_key"].(string)}
	f.agentID = a["agent_id"].(string)
	id := func(cmd string, in map[string]any) string { return e.must(o, cmd, in)["id"].(string) }
	eng := id("space.create", map[string]any{"key": "ENG", "name": "Engineering", "template": "software"})
	kan := id("space.create", map[string]any{"key": "KAN", "name": "Kanban", "template": "kanban"})
	prv := id("space.create", map[string]any{"key": "PRV", "name": "Private", "template": "software", "visibility": "private"})
	// Item numbers are unique across spaces (fillers are created and deleted), so every ORDER BY ends in a total
	// order: rows that tie on rank or priority are sorted by number, which differs for items of different spaces.
	filler := func(space string, n int) {
		for i := 0; i < n; i++ {
			e.must(o, "note.delete", map[string]any{"id": e.must(o, "item.create", map[string]any{"space_id": space, "title": "filler"})["id"].(string)})
		}
	}
	filler(kan, 10)
	filler(prv, 20)
	s3 := id("sprint.create", map[string]any{"space_id": eng, "name": "Old Sprint"})
	e.must(o, "sprint.start", map[string]any{"id": s3})
	e.must(o, "sprint.complete", map[string]any{"id": s3})
	s1 := id("sprint.create", map[string]any{"space_id": eng, "name": "Sprint 1"})
	e.must(o, "sprint.start", map[string]any{"id": s1})
	s2 := id("sprint.create", map[string]any{"space_id": eng, "name": "Sprint 2"})
	v1 := id("version.create", map[string]any{"space_id": eng, "name": "1.0"})
	e.must(o, "version.release", map[string]any{"id": v1})
	v2 := id("version.create", map[string]any{"space_id": eng, "name": "2.0"})
	c1 := id("component.create", map[string]any{"space_id": eng, "name": "api"})
	c2 := id("component.create", map[string]any{"space_id": eng, "name": "UI"})
	f.data = map[string]string{"s1": s1, "s2": s2, "s3": s3, "v1": v1, "v2": v2, "c1": c1, "c2": c2}
	item := func(w who, in map[string]any) {
		e.must(w, "item.create", in)
	}
	today := time.Now().UTC().Format("2006-01-02")
	item(o, map[string]any{"space_id": eng, "title": "Login page broken", "description": "users cannot log in", "priority": "urgent", "labels": []string{"auth", "Backend"}, "estimate": 3, "assignee_id": f.member.userID, "sprint_id": s1, "fix_version_id": v2, "component_id": c1, "due_date": "2030-01-02", "item_type": "bug"})
	item(o, map[string]any{"space_id": eng, "title": "100% sure a_b path", "description": "wildcards % and _ and \\ backslash", "priority": "high", "labels": []string{"docs"}, "assignee_id": f.bob.userID, "sprint_id": s2, "due_date": today})
	item(o, map[string]any{"space_id": eng, "title": "Epic: onboarding", "item_type": "epic", "priority": "medium", "fix_version_id": v1, "component_id": c2})
	item(o, map[string]any{"space_id": eng, "title": "O'Reilly \"quotes\"", "item_type": "story", "priority": "low", "labels": []string{"ui"}, "assignee_agent_id": f.agentID, "estimate": 0.5, "due_date": "2029-12-31"})
	item(o, map[string]any{"space_id": eng, "title": "ünï 日本語 🚀 Σίσυφος", "item_type": "subtask", "priority": "none", "labels": []string{"i18n", "ÜNÏ"}, "custom": map[string]any{"team": "core"}})
	item(f.agent, map[string]any{"space_id": eng, "title": "Filed by the agent", "description": "triage me", "estimate": 8})
	item(f.member, map[string]any{"space_id": eng, "title": "Member task", "assignee_id": f.member.userID, "priority": "high", "sprint_id": s3, "labels": []string{"auth"}})
	item(o, map[string]any{"space_id": eng, "title": "Mixed Case Title", "description": "login flow mention", "priority": "medium", "assignee_id": f.owner.userID})
	item(o, map[string]any{"space_id": kan, "title": "Kanban bug", "item_type": "bug", "priority": "high", "labels": []string{"auth"}})
	item(o, map[string]any{"space_id": kan, "title": "Kanban task", "assignee_id": f.bob.userID, "due_date": "2031-06-15"})
	item(o, map[string]any{"space_id": prv, "title": "Secret login plan", "priority": "urgent", "assignee_id": f.owner.userID})
	for _, t := range []struct{ key, to string }{{"ENG-1", "Todo"}, {"ENG-1", "In Progress"}, {"ENG-2", "Todo"}, {"ENG-3", "In Progress"}, {"ENG-3", "In Review"}, {"ENG-4", "Cancelled"},
		{"ENG-7", "Todo"}, {"ENG-7", "In Progress"}, {"ENG-7", "Done"}, {"KAN-11", "Doing"}, {"KAN-12", "Done"}, {"PRV-21", "Todo"}} {
		e.must(o, "item.transition", map[string]any{"key": t.key, "to": t.to})
	}
	e.must(o, "item.rank", map[string]any{"key": "ENG-3", "before": "ENG-1"})
	e.must(o, "evidence.record", map[string]any{"source": "ci", "kind": "build_ok", "item_keys": []string{"ENG-1"}})
	return f
}

func cnqlCorpus(f cnqlFixture) []string {
	today := time.Now().UTC()
	d := func(off int) string { return today.AddDate(0, 0, off).Format("2006-01-02") }
	var qs []string
	add := func(q ...string) { qs = append(qs, q...) }

	// plain and combined filters per field
	for _, field := range []string{"project", "Project"} {
		for _, v := range []string{"ENG", "eng", `"Eng"`, "KAN", "PRV", "NOPE", `'ENG'`, `""`, "E-N-G", "ünï"} {
			add(field+" = "+v, field+" != "+v, field+" IN ("+v+", KAN)", field+" NOT IN ("+v+", KAN)")
		}
	}
	add("project IS EMPTY", "project IS NOT EMPTY", "project is null", "project IS NOT NULL", "project ~ ENG", "project > ENG", "project <= ENG", "project !~ ENG")
	for _, v := range []string{"ENG-1", "eng-2", "ENG-99", "KAN-11", `"PRV-21"`, "ENG-1,ENG-2"} {
		add("key = "+v, "issuekey = "+v, "key != "+v, "key IN ("+v+", KAN-12)", "key NOT IN ("+v+")")
	}
	add("key ~ ENG", "key > ENG-1", "key IS EMPTY", "key IS NOT EMPTY")
	for _, v := range []string{"Backlog", "Todo", `"In Progress"`, "in-progress", `'in review'`, "Done", "done", "Cancelled", "Doing", "Nope", `"To Do"`} {
		add("status = "+v, "status != "+v, "status IN ("+v+", Done)", "status NOT IN ("+v+")")
	}
	add("status IS EMPTY", "status IS NOT EMPTY", "status ~ Do", "status >= Done")
	for _, v := range []string{"todo", `"To Do"`, "to_do", "to-do", "open", "OPEN", "in_progress", `"In Progress"`, "in-progress", "inprogress", "InProgress", "Done", "closed", "complete", "DONE", "Weird", `"in progress "`, `""`, "ünï"} {
		add("statusCategory = "+v, "statusCategory != "+v, "statusCategory IN ("+v+", Done)", "statusCategory NOT IN ("+v+")", "statuscategory = "+v)
	}
	add("statusCategory IS EMPTY", "statusCategory IS NOT EMPTY", "statusCategory ~ Done", "statusCategory > Done", "statusCategory = currentUser()", "statusCategory = now()")
	for _, field := range []string{"assignee", "reporter"} {
		vals := []string{"currentUser()", "CURRENTUSER()", `"Mia Member"`, `"mia member"`, "Bob", "bob", `"Tess Owner"`, `"Code Bot"`, `"code bot"`, "Nobody", `""`,
			f.member.userID, strings.ToUpper(f.member.userID), strings.ReplaceAll(f.member.userID, "-", ""), "{" + f.member.userID + "}", `"urn:uuid:` + f.bob.userID + `"`, f.agentID, strings.ReplaceAll(f.agentID, "-", ""),
			f.owner.userID, f.guest.userID, `"` + f.member.email + `"`, `"` + strings.ToUpper(f.bob.email) + `"`, f.owner.email, "00000000-0000-0000-0000-000000000000", f.member.userID[:35], `"éΣ"`}
		for _, v := range vals {
			add(field+" = "+v, field+" != "+v, field+" IN ("+v+", Bob)", field+" NOT IN ("+v+")")
		}
		add(field+" IS EMPTY", field+" IS NOT EMPTY", field+" is null", field+" ~ Bob", field+" > Bob", field+" = now()", field+" = openSprints()", field+" IN (currentUser(), startOfDay())",
			field+" = currentUser(1)", field+" = currentUser(a, b)", field+" in currentUser()")
	}
	for _, v := range []string{"urgent", "Urgent", "highest", "high", "HIGH", "medium", "low", "none", "lowest", "None", "spicy", `""`, "critical", "ünï"} {
		add("priority = "+v, "priority != "+v, "priority IN ("+v+", low)", "priority NOT IN ("+v+")")
		for _, op := range []string{">", ">=", "<", "<="} {
			add("priority " + op + " " + v)
		}
	}
	add("priority IS EMPTY", "priority IS NOT EMPTY", "priority ~ high", "priority = now()", "priority >= currentUser()")
	for _, v := range []string{"bug", "Task", "epic", "story", "subtask", "BUG", "deal", `""`} {
		add("type = "+v, "issuetype = "+v, "type != "+v, "type IN ("+v+", task)", "type NOT IN ("+v+")")
	}
	add("type IS EMPTY", "type IS NOT EMPTY", "type ~ bug", "type < bug")
	for _, v := range []string{"auth", "Auth", "Backend", "docs", "ui", "i18n", "ÜNÏ", "ünï", "nope", `""`, `"a b"`} {
		add("labels = "+v, "label = "+v, "labels != "+v, "labels IN ("+v+", docs)", "labels NOT IN ("+v+")")
	}
	add("labels IS EMPTY", "labels IS NOT EMPTY", "labels ~ auth", "labels > a", "labels = openSprints()")
	for _, v := range []string{"Sprint 1", "sprint 2", `"Old Sprint"`, "nope", f.data["s1"], strings.ToUpper(f.data["s2"]), strings.ReplaceAll(f.data["s3"], "-", ""), `""`} {
		add("sprint = "+quoteIfSpace(v), "sprint != "+quoteIfSpace(v), "sprint IN ("+quoteIfSpace(v)+", \"Sprint 2\")", "sprint NOT IN ("+quoteIfSpace(v)+")")
	}
	add("sprint in openSprints()", "sprint = openSprints()", "sprint IN (openSprints(), closedSprints())", "sprint in closedSprints()", "sprint IN futureSprints()", "sprint NOT IN (openSprints())", "sprint != futureSprints()",
		"sprint = OPENSPRINTS()", "sprint IN (openSprints(), \"Sprint 2\", closedSprints())", "sprint IS EMPTY", "sprint IS NOT EMPTY", "sprint > a", "sprint ~ a", "sprint = nope()", "sprint = releasedVersions()", "sprint = currentUser()", "sprint in openSprints", "sprint IN (a, openSprints())")
	for _, v := range []string{`"1.0"`, "2.0", "3.0", f.data["v1"], f.data["v2"]} {
		add("fixVersion = "+v, "fixversion != "+v, "fixVersion IN ("+v+", \"2.0\")", "fixVersion NOT IN ("+v+")")
	}
	add("fixVersion in releasedVersions()", "fixVersion = unreleasedVersions()", "fixVersion NOT IN (releasedVersions())", "fixVersion IS EMPTY", "fixVersion IS NOT EMPTY", "fixVersion > 1", "fixVersion ~ 1", "fixVersion = openSprints()", "fixVersion = RELEASEDVERSIONS()")
	for _, v := range []string{"api", "API", "ui", "UI", "nope", f.data["c1"], f.data["c2"], `""`} {
		add("component = "+v, "component != "+v, "component IN ("+v+", ui)", "component NOT IN ("+v+")")
	}
	add("component IS EMPTY", "component IS NOT EMPTY", "component < a", "component ~ a", "component = releasedVersions()", "component IN (openSprints())")

	// dates: relative, functions and absolute forms
	for _, field := range []string{"created", "updated", "resolved", "due"} {
		for _, v := range []string{"-1d", "+1d", "1d", "-7d", "-30m", "2h", "-1w", "-1M", "1M", "-1y", "1y", "-0d", "+0m", "-12345d", "-99999999999d", "-9999999999999999999d", "--1d", "-+1d", "1.5d", "-1D", "-7x", "7", "d", "-", "", `" -1d"`, `"-1d "`,
			d(0), d(-1), d(1), "2030-01-02", "2029-12-31", "2000-02-29", "2001-02-29", "2026-02-30", "2026-13-01", "2026-00-10", "2026-1-5", "26-1-5", "2026/11/14", "2026/1/5", "2026-11-14T09:30:00", "2026-11-14T09:30:00Z", "2026-11-14T09:30:00+05:30",
			`"` + d(0) + ` 09:30"`, `"` + d(0) + ` 9:5"`, `"` + d(0) + ` 09:30:15"`, `"` + d(0) + ` 23:59:60"`, `"` + d(0) + `T09:30:15"`, `"` + d(0) + `T09:30"`, `"` + d(0) + ` 24:00"`, `"` + d(0) + `09:30"`, `"2026/11/14 09:30"`,
			`"2026-11-14 09:30:00.123"`, `"2026-11-14T09:30:00.123456789012Z"`, `"2026-11-14T09:30:00+0530"`, `"2026-11-14 09:30:00z"`, `"2026-11-14t09:30:00Z"`, `"2026-11-14T09:30:00−05:00"`, `"2026-11-14T09:30:00+24:00"`, `"2026-11-14T25:30:00Z"`,
			`"+2026-11-14"`, `"-0001-11-14"`, `"12345-11-14"`, `" 2026-11-14"`, `"2026-11-14 "`, "banana", "tomorrow", "0000-01-01", "9999-12-31", `"2026-11-14 09:30 "`} {
			add(field+" = "+v, field+" != "+v, field+" >= "+v, field+" < "+v, field+" IN ("+v+", -1d)")
		}
		for _, v := range []string{"now()", "NOW()", "startOfDay()", "startOfWeek()", "startOfMonth()", `startOfDay("-1d")`, `startOfDay(-1d)`, `startOfWeek("1w")`, `startOfMonth("-1M")`, `now("-1h")`, `now(-1h, extra)`, `now(x)`, `startOfYear()`, "now(1)", `now("")`, "nope()", "currentUser()", "openSprints()"} {
			add(field+" >= "+v, field+" < "+v, field+" = "+v, field+" IN ("+v+")")
		}
		add(field+" IS EMPTY", field+" IS NOT EMPTY", field+" ~ x", field+" !~ x", "ORDER BY "+field, "ORDER BY "+field+" DESC")
	}
	// text
	for _, v := range []string{"login", "LOGIN", `"log in"`, `"100%"`, `"a_b"`, `"%"`, `"_"`, `"\\"`, `"wildcards % and _"`, `"O'Reilly"`, `'O\'Reilly'`, `"\"quotes\""`, `"ünï"`, `"ÜNÏ"`, "日本語", "🚀", "Σίσυφος", "ΣΊΣΥΦΟΣ", `""`, "ENG-3", "eng", "triage", "mention", `"a\\b"`} {
		add("text ~ "+v, "text !~ "+v, "NOT text ~ "+v, "text ~ "+v+" AND project = ENG", "text ~ "+v+" OR text ~ login")
	}
	add("text IS EMPTY", "text IS NOT EMPTY", "text = login", "text != login", "text IN (login)", "text > a", "text >= a", "text < a", "text <= a", "text ~ now()", "text ~ x()", "text ~ currentUser()")

	// boolean structure
	add("project = ENG AND status = Todo", "project = ENG OR project = KAN", "NOT project = ENG", "NOT NOT project = ENG", "NOT (project = ENG AND status = Todo)",
		"project = ENG AND (status = Todo OR status = Backlog)", "(project = ENG AND status = Todo) OR (project = KAN AND status = Doing)", "project = ENG AND status = Todo OR project = KAN",
		"NOT project = ENG AND NOT status = Todo", "((((project = ENG))))", "(project = ENG) (status = Todo)", "project = ENG AND", "AND project = ENG", "project = ENG OR OR status = Todo", "NOT", "NOT AND",
		"project = ENG and status = todo or project = KAN", "Project = ENG And Status = Todo", "assignee = currentUser() AND statusCategory != Done AND priority >= High",
		"statusCategory != Done AND assignee IS EMPTY AND sprint IS EMPTY", "labels = auth AND NOT labels = Backend", "labels IS EMPTY OR assignee IS EMPTY", "NOT assignee = Bob", "NOT assignee IS EMPTY", "assignee != Bob AND assignee IS NOT EMPTY",
		"project = ENG AND ("+strings.Repeat("(", 20)+"status = Todo"+strings.Repeat(")", 20)+")", strings.Repeat("project = ENG OR ", 60)+"project = KAN", strings.Repeat("NOT ", 50)+"project = ENG")

	// ordering
	for _, fld := range []string{"project", "key", "issuekey", "status", "statusCategory", "assignee", "reporter", "priority", "type", "labels", "sprint", "fixVersion", "component", "created", "updated", "due", "duedate", "resolved", "resolutiondate", "text", "rank", "bogus", "RANK", "Key"} {
		add("ORDER BY "+fld, "ORDER BY "+fld+" ASC", "ORDER BY "+fld+" DESC", "project = ENG ORDER BY "+fld+" desc, key", "order by "+fld+" asc, priority desc, rank")
	}
	add("ORDER BY priority DESC, due ASC, created DESC, key", "ORDER BY key DESC, priority", "ORDER", "ORDER BY", "ORDER BY ,", "ORDER BY key,", "ORDER BY key DESC DESC", "ORDER BY key ASC DESC", "ORDER BY key, , priority", "ORDER BY \"key\"", "ORDER BY key LIMIT 1",
		"ORDER key", "ORDER BY 1", "ORDER BY status = Done", "ORDER BY (key)", "project = ENG ORDER BY", "project = ENG ORDER", "project = ENG ORDER BY key AND status = Todo", "ORDER BY key ORDER BY priority",
		"ORDER BY key ORDER", "ORDER BY currentUser()", "ORDER BY priority, priority, priority", "NOT ORDER BY key", "order  by\tkey\ndesc")

	// lexer and parser errors, odd characters, unicode
	add("", "   ", "\t\n", " ORDER BY key", "project", "project =", "project = ", "= ENG", "= ", "=", "!", "!=", "~", "!~", "<", ">", "<=", ">=", ",", "(", ")", "()", "(project = ENG", "project = ENG)", "project = (ENG)", "project = ENG,KAN", "project = ENG KAN",
		"project IN", "project IN (", "project IN ()", "project IN (ENG", "project IN (ENG,", "project IN (ENG,)", "project IN ENG", "project IN (ENG KAN)", "project IN ((ENG))", "project NOT", "project NOT ENG", "project NOT IN", "project NOT IN ENG", "project IS", "project IS ENG", "project IS NOT", "project IS NOT ENG", "project IS EMPTY EMPTY",
		"project IS NULL OR", "project ==ENG", "project =! ENG", "project =~ ENG", "project ! = ENG", "project ! ENG", "project !ENG", "project !", "project != ", "project !~", "project <> ENG", "project => ENG", "project =< ENG",
		`project = "ENG`, `project = 'ENG`, `project = "EN\"G"`, `project = "EN\\"`, `project = "EN\`, `project = 'a"b'`, `project = "a'b"`, `project = ""`, `project = ''`, `project = "\n"`, "project = \"line\nbreak\"", "project = EN\"G\"", `"project" = ENG`, `'project' = ENG`,
		"project = ENG #", "project = ENG;", "project = ENG; DROP TABLE core.notes", "project = ENG -- comment", "project = ENG /* c */", "project = $1", "project = ${x}", "project = ENG & status = Todo", "project = ENG && status = Todo", "project = ENG || status = Todo",
		"project = {ENG}", "project = [ENG]", "project = ENG*", "project = *", "project = ENG?", "project = ENG|KAN", "project = ENG\\", "project = `ENG`", "project = ENG@x.y", "project = a+b", "project = a:b", "project = a/b", "project = a.b", "project = a_b", "project = a-b", "project = -", "project = +", "project = .", "project = :", "project = @", "project = /",
		"project = ENG", "project　=　ENG", "project​=​ENG", "project = ENG\u0085", "project = éng", "project = ́x", "project = x́", "project = กิ", "project = ①", "project = Ⅷ", "project = ²", "project = ٣", "project = ½", "project = ٣٤", "project = ₁",
		"project = 🚀", "🚀 = ENG", "pröject = ENG", "ünï = x", "日本語 = x", "text ~ 日本語 AND 🚀", "project = ENG 🚀", "project = \"🚀\" AND stauts = x", "text ~ \"日本語\" AND x", "日本語日本語日本語 = x", "text ~ 日本語 AND stauts = x",
		"stauts = Done", "project = ENG AND stauts = Done", "projec = ENG", "project = ENG AND  stauts = Done", "(stauts = Done)", "NOT stauts = Done", "project = ENG OR (status = Todo AND foo = bar)", "unknown", "unknown IN (a)", "unknown IS EMPTY", "unknown ~ a", "rank = a", "rank IS EMPTY", "rank ~ a", "rank IN (a)", "rank > a",
		"currentUser()", "now()", "project = ENG now()", "project = f(", "project = f(a", "project = f(a,", "project = f(a,)", "project = f(,)", "project = f(a b)", "project = f(a,b)", "project = f((a))", "project = f()", "project = f( )", "project = f(\"a\", 'b', c)", "project = f(=)", "project = f(1.5)", "project = f()()", "project = f()g()", "project IN (f(), g(a))",
		"sprint IN (openSprints(), )", "sprint IN openSprints() AND x", "sprint IN openSprints(", "sprint IN openSprints(a", "sprint NOT IN openSprints()", "sprint NOT IN closedSprints", "sprint IN (openSprints()", "status IN (Done Todo)", "status IN (Done,, Todo)", "status IN (,Done)", "status IN (Done) AND", "status IN (Done) status = Todo",
		"status = Done AND AND status = Todo", "status = Done OR", "status = Done ORDER", "status = Done ORDER BY", "status = Done ORDER BY key DESC,", "status = Done) ORDER BY key", "(status = Done ORDER BY key)", "status = Done AND (ORDER BY key)", "status = Done foo", "status = Done foo bar", "status = Done = Todo", "status = Done status = Todo", "status = Done, status = Todo",
		"status = Done\n AND\n status = Todo", "status\n=\nDone", "  status  =  Done  ", "\tstatus\t=\tDone\t", "status = Done  AND status = Todo", strings.Repeat("a", 5000), "project = "+strings.Repeat("A", 5000), "project IN ("+strings.Repeat("A,", 500)+"B)", strings.Repeat("(", 300)+"project = ENG"+strings.Repeat(")", 300), strings.Repeat(" ", 3000)+"x",
		`text ~ "`+strings.Repeat("%_\\", 500)+`"`, "status = \""+strings.Repeat("é", 3000)+"\"")

	// everything from the Rust unit tests and the Rust integration test
	add("project = ENG AND status = \"In Progress\"", "status = \"x'; drop table core.notes; --\"", "text ~ \"100%_\\\\\"", "status = a OR status = b AND NOT type = bug", "(status = a OR status = b) AND type = bug", "assignee != currentUser()", "status NOT IN (Done, Cancelled)",
		"assignee IS EMPTY", "assignee is not empty", "labels IS EMPTY", "due IS NULL", `assignee IN ("Tess", `+f.owner.userID+`)`, "reporter = currentUser()", `statusCategory = "To Do"`, `statusCategory IN ("In Progress", Done)`, "statusCategory = Weird", "priority >= High", "priority = Highest", "priority = Spicy",
		"sprint in openSprints()", `sprint IN (closedSprints(), "Sprint 4")`, "sprint IS EMPTY", "component = openSprints()", `labels = urgent AND fixVersion = "1.0" AND component = Api`, "created >= -7d", "updated < -2w", "created > startOfDay()", `created > startOfDay("-1d")`, "resolved <= now()",
		"created > startOfWeek() AND created > startOfMonth()", "created > -1M", "due < 2026-04-01", "created = 2026-04-01", `created = "2026-04-01 09:00"`, "text ~ login", "text !~ login", "text = login", "Issuetype = bug and Project in (eng, ops) order by Priority desc", "project = ENG ORDER BY rank", "ORDER BY labels", "rank = a",
		"status = ", "stauts = Done", "status = Done AND", "status Done", "(status = Done", "status IN Done", "status IN (a, b", `status = "abc`, "status = Done foo", "status = a # b", "status ! a", "assignee IS FULL", "ORDER status", "status = f(", "= a", "priority ~ high", "status > a", "created = banana", "created > nope()", "created > -7x", "assignee = now()", "ORDER BY updated DESC, key",
		"text ~ login", "project = SEC", "type = bug", "labels IN (docs, nothing) AND project = ENG", "assignee = currentUser()", "assignee IS EMPTY AND project = ENG", "priority >= high", "project = ENG ORDER BY key DESC", "created >= -1d AND NOT (type = bug) AND project = ENG", `statusCategory = "To Do" AND status != Done AND project = ENG`, "statusCategory = Done",
		`fixVersion = "1.0" AND component = api`, "assignee = Coder", "assignee = \"Code Bot\"")

	// every field with every operator and a value that suits it
	vals := map[string]string{"project": "ENG", "key": "ENG-1", "status": "Todo", "statusCategory": "done", "assignee": "Bob", "reporter": "Tess", "priority": "high", "type": "bug", "labels": "auth", "sprint": `"Sprint 1"`, "fixVersion": `"2.0"`,
		"component": "api", "created": "-1d", "updated": "-1d", "due": "2030-01-02", "resolved": "-1d", "text": "login", "rank": "a"}
	for fld, v := range vals {
		for _, op := range []string{"=", "!=", ">", ">=", "<", "<=", "~", "!~"} {
			add(fld + " " + op + " " + v)
		}
		for _, p := range []string{"IN (" + v + ")", "NOT IN (" + v + ")", "IN (" + v + ", " + v + ")", "IS EMPTY", "IS NOT EMPTY", "IS NULL", "IS NOT NULL", "in (" + v + ") and " + fld + " is not empty"} {
			add(fld + " " + p)
		}
	}
	return qs
}

func quoteIfSpace(v string) string {
	if strings.ContainsAny(v, " \"") || v == "" {
		if strings.HasPrefix(v, `"`) {
			return v
		}
		return `"` + v + `"`
	}
	return v
}

func TestDifferentialCNQLCorpus(t *testing.T) {
	e := newEnv(t)
	f := e.cnqlFixture()
	e.settle(f.owner.ws)
	qs := cnqlCorpus(f)
	seen := map[string]bool{}
	var corpus []string
	for _, q := range qs {
		if !seen[q] {
			seen[q] = true
			corpus = append(corpus, q)
		}
	}
	if len(corpus) < 1500 {
		t.Fatalf("corpus too small: %d", len(corpus))
	}
	callers := []struct {
		name  string
		w     who
		every int
	}{{"owner", f.owner, 1}, {"member", f.member, 4}, {"agent", f.agent, 9}, {"guest", f.guest, 25}, {"bob", f.bob, 17}}
	pairs, ok, errs, bad := 0, 0, 0, 0
	for i, q := range corpus {
		for _, c := range callers {
			if i%c.every != 0 {
				continue
			}
			limit := 200
			in := map[string]any{"cnql": q, "limit": limit}
			time.Sleep(48 * time.Millisecond) // the shared Rust server rate-limits per seat
			g := e.cmd(e.gosv.URL, c.w, f.owner.ws, "item.list", in, nil)
			r := e.cmd(e.rust, c.w, f.owner.ws, "item.list", in, nil)
			pairs++
			switch {
			case r.status == 200:
				ok++
			case r.status == 400:
				errs++
			}
			if g.status != r.status || string(g.body) != string(r.body) {
				bad++
				if bad <= 25 {
					t.Errorf("%s %q\n go   %d %.600s\n rust %d %.600s", c.name, q, g.status, g.body, r.status, r.body)
				}
			}
		}
	}
	t.Logf("compared %d query pairs over %d distinct queries (%d ok, %d invalid); %d differ", pairs, len(corpus), ok, errs, bad)
	if ok < 800 || errs < 500 {
		t.Fatalf("corpus not balanced: %d ok, %d invalid", ok, errs)
	}
	// the ordering and assignee paths really return rows
	g := e.cmd(e.gosv.URL, f.owner, f.owner.ws, "item.list", map[string]any{"cnql": fmt.Sprintf("assignee = %q ORDER BY key", f.member.email)}, nil)
	if g.status != 200 || !strings.Contains(string(g.body), `"key":"ENG-1"`) {
		t.Fatalf("assignee by email: %d %s", g.status, g.body)
	}
}
