package server_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Twin-fixture differential tests for the commands ported late: note.revisions/note.revision (note.restore stays in
// Rust and is proxied), oauth_app.*, and import.*. Same method as TestDifferentialDocs: one script through Rust on
// fixture A and through Go on fixture B; normalized responses and resulting rows must be equal.

func runTwin(t *testing.T, script func(r *runner), dump map[string]string, minOK, minErr int) (rust, gov *runner) {
	t.Helper()
	e := newEnv(t)
	run := func(base string) *runner {
		r := &runner{e: e, base: base, f: e.fixture(), vars: map[string]string{}, toks: map[string]string{}}
		script(r)
		e.settleDbg(r.f.owner.ws)
		r.transcript = append(r.transcript, e.dumpRows(r, dump)...)
		return r
	}
	rust, gov = run(e.rust), run(e.gosv.URL)
	compareTranscripts(t, rust.transcript, gov.transcript, minOK, minErr)
	return
}

func (r *runner) list(caller, cmd string, in any) []map[string]any {
	time.Sleep(30 * time.Millisecond)
	return r.e.cmd(r.base, r.f.who(caller), r.f.owner.ws, cmd, in, nil).jsonList()
}

func TestDifferentialRevisions(t *testing.T) {
	script := func(r *runner) {
		sp := r.f.space
		ph := "00000000-0000-4000-8000-000000000001"
		r.id("prv", "owner", "space.create", map[string]any{"key": "PRV", "name": "Private", "visibility": "private"})
		r.id("p1", "owner", "note.create", map[string]any{"space_id": sp, "title": "Versioned", "content_md": "v0"})
		r.id("pp", "owner", "note.create", map[string]any{"space_id": r.v("prv"), "title": "Hidden versions", "content_md": "p0"})
		r.id("rest", "owner", "note.create", map[string]any{"space_id": sp, "title": "Restricted versions", "content_md": "r0"})
		r.id("empty", "owner", "note.create", map[string]any{"space_id": sp, "title": "No versions"})
		for i := 1; i <= 2; i++ {
			for _, n := range []string{"p1", "pp", "rest"} {
				r.call("owner", "note.update", map[string]any{"id": r.v(n), "content_md": fmt.Sprintf("v%d ünï 🚀 %s", i, n), "title": fmt.Sprintf("T%d", i)})
			}
		}
		// versions are normally taken by the collab service while people type; seed more of every reason
		for _, n := range []string{"p1", "pp", "rest"} {
			for i, reason := range []string{"edit", "before_restore", "restore", "edit"} {
				adminSQL(r.e, r.f.owner.ws, `insert into core.note_revisions (workspace_id, note_id, number, title, content_md, reason, restored_from, created_by)
					select n.workspace_id, n.id, coalesce((select max(number) from core.note_revisions x where x.note_id = n.id), 0) + 1, 'Title ' || $2::int, $3, $4,
					       (select id from core.note_revisions y where y.note_id = n.id and $4 = 'restore' order by number limit 1),
					       case when $2::int % 2 = 0 then $5::uuid end
					from core.notes n where n.id = $1::uuid`, r.v(n), i, fmt.Sprintf("body %d\n\nünï ✓ <b>&</b>", i), reason, r.f.member.userID)
				time.Sleep(3 * time.Millisecond)
			}
		}
		r.call("owner", "note.restrict", map[string]any{"note_id": r.v("rest"), "restriction": "view"})
		ids := map[string][]string{}
		for _, n := range []string{"p1", "pp", "rest"} {
			for i, rev := range r.list("owner", "note.revisions", map[string]any{"note_id": r.v(n)}) {
				ids[n] = append(ids[n], rev["id"].(string))
				r.vars[fmt.Sprintf("%s-rev%d", n, i)] = rev["id"].(string)
			}
		}
		for _, c := range aiCallers {
			for _, n := range []string{"p1", "pp", "rest", "empty"} {
				r.call(c, "note.revisions", map[string]any{"note_id": r.v(n)})
			}
			for _, n := range []string{"p1", "pp", "rest"} {
				for _, id := range ids[n] {
					r.call(c, "note.revision", map[string]any{"id": id})
				}
			}
			r.call(c, "note.revisions", map[string]any{"note_id": ph})
			r.call(c, "note.revision", map[string]any{"id": ph})
		}
		for _, body := range []string{`{}`, `[]`, `null`, `{"note_id":"x"}`, `{"id":"x"}`, `{"note_id":null}`, `{"id":5}`, `{"note_id":"` + strings.ToUpper(r.v("p1")) + `"}`} {
			r.raw("owner", "note.revisions", body)
			r.raw("owner", "note.revision", body)
		}
		// note.restore stays in Rust: through the Go server it is proxied
		r.call("member", "note.restore", map[string]any{"note_id": r.v("p1"), "revision_id": ids["p1"][1]})
		r.call("owner", "note.restore", map[string]any{"note_id": r.v("p1"), "revision_id": ids["p1"][2], "expected_epoch": 99})
		r.call("owner", "note.restore", map[string]any{"note_id": r.v("p1"), "revision_id": ids["p1"][2]})
		r.call("owner", "note.restore", map[string]any{"note_id": r.v("pp"), "revision_id": ids["p1"][0]})
		for _, c := range []string{"owner", "member", "other"} {
			r.call(c, "note.revisions", map[string]any{"note_id": r.v("p1")})
		}
	}
	runTwin(t, script, map[string]string{
		"revisions": `select (to_jsonb(x) - 'id')::text from core.note_revisions x order by x.created_at, x.number`,
		"events":    `select (to_jsonb(x) - 'id')::text from core.events x where x.actor_type in ('user','agent') and x.kind <> 'command.denied' order by x.id`,
	}, 40, 30)
}

func TestDifferentialOAuthApps(t *testing.T) {
	var cid int
	script := func(r *runner) {
		e, ws := r.e, r.f.owner.ws
		ph := "00000000-0000-4000-8000-000000000001"
		client := fmt.Sprintf("cnc_diff_%s_%d", rnd(), cid)
		cid++
		adminSQL(e, ws, `insert into core.oauth_clients (client_id, kind, name, redirect_uris) values ($1,'registered','Diff client ünï',ARRAY['https://example.test/cb'])`, client)
		grant := func(name, user string, revoked bool) {
			r.vars[name] = docsScalar(e, ws, `insert into core.oauth_grants (client_id, user_id, workspace_id, resource, scope, revoked_at)
				values ($1,$2::uuid,$3::uuid,'https://example.test/mcp','read', case when $4 then now() end) returning id::text`, client, user, ws, revoked)
			adminSQL(e, ws, `insert into core.oauth_tokens (token_hash, kind, grant_id, expires_at) values ($1,'access',$2::uuid,now() + interval '1 hour'), ($3,'refresh',$2::uuid,now() + interval '1 day')`,
				rnd()+rnd(), r.vars[name], rnd()+rnd())
			time.Sleep(5 * time.Millisecond)
		}
		grant("go", r.f.owner.userID, false)
		grant("go2", r.f.owner.userID, false)
		grant("gm", r.f.member.userID, false)
		grant("gr", r.f.owner.userID, true)
		for _, c := range aiCallers {
			r.call(c, "oauth_app.list", map[string]any{})
		}
		r.call("owner", "oauth_app.list", map[string]any{"ignored": 1})
		for _, c := range aiCallers {
			r.call(c, "oauth_app.revoke", map[string]any{"id": r.v("gm")})
		}
		r.call("owner", "oauth_app.revoke", map[string]any{"id": r.v("gr")}) // already revoked
		r.call("owner", "oauth_app.revoke", map[string]any{"id": ph})
		for _, body := range []string{`{}`, `[]`, `null`, `{"id":"x"}`, `{"id":null}`, `{"id":5}`, `{"id":"` + strings.ToUpper(r.v("go")) + `"}`} {
			r.raw("owner", "oauth_app.revoke", body)
			r.raw("owner", "oauth_app.list", body)
		}
		r.call("owner", "oauth_app.list", map[string]any{})
		r.call("member", "oauth_app.list", map[string]any{})
		r.call("owner", "oauth_app.revoke", map[string]any{"id": r.v("go")})
		r.call("owner", "oauth_app.revoke", map[string]any{"id": r.v("go")})
		r.call("owner", "oauth_app.list", map[string]any{})
		r.transcript = append(r.transcript, "grant rows "+adminScalar(e, ws, "select count(*)::text from core.oauth_grants where workspace_id = $1::uuid", ws))
	}
	runTwin(t, script, map[string]string{
		"grants": `select (to_jsonb(g) - 'id' - 'client_id' - 'created_at')::text from core.oauth_grants g where g.workspace_id = current_setting('app.workspace_id')::uuid order by g.created_at`,
		"tokens": `select kind || ' ' || (select (revoked_at is not null)::text from core.oauth_grants g where g.id = t.grant_id) from core.oauth_tokens t where t.grant_id in (select id from core.oauth_grants where workspace_id = current_setting('app.workspace_id')::uuid) order by 1`,
		"events": `select (to_jsonb(x) - 'id')::text from core.events x where x.actor_type in ('user','agent') and x.kind <> 'command.denied' order by x.id`,
	}, 10, 15)
}

func TestDifferentialImports(t *testing.T) {
	trello := func(cards string) string {
		return `{"lists":[{"id":"l1","name":"Doing"},{"id":"l2","name":"Done"}],"cards":` + cards + `,"checklists":[{"idCard":"c1","name":"Steps","checkItems":[{"name":"a","state":"complete"},{"name":"b","state":"incomplete"}]}],
		"actions":[{"type":"commentCard","id":"a1","date":"2026-01-02T03:04:05.000Z","data":{"card":{"id":"c1"},"text":"hi ünï"},"memberCreator":{"fullName":"Asha"}}]}`
	}
	cards := `[{"id":"c1","name":"Fix login ünï","idList":"l1","desc":"d","labels":[{"name":"bug"}],"due":"2026-03-04T00:00:00.000Z"},
		{"id":"c2","name":"Done card","idList":"l2","dateLastActivity":"2026-02-01T00:00:00.000Z"},{"id":"c3","name":"Archived","closed":true},{"name":"no id"},{"id":"c5"},{"id":"c6","name":""}]`
	pages := `{"pages":[{"id":"p1","title":"Onboarding","markdown":"# Onboarding\n\nWelcome","comments":[{"body":"nice"}]},{"id":"p2","title":"Child","parent_id":"p1","html":"<p>Hello <b>x</b></p>"},{"id":"","title":"x"},{"title":"y"},5]}`
	csvm := func(m map[string]any) map[string]any { return m }
	mapping := csvm(map[string]any{"title": "Name", "id": "Key", "status": "Status", "labels": "Tags", "extra_ignored": 1})
	script := func(r *runner) {
		sp := r.f.space
		ph := "00000000-0000-4000-8000-000000000001"
		r.id("prv", "owner", "space.create", map[string]any{"key": "PRV", "name": "Private", "visibility": "private"})
		start := func(caller string, in map[string]any) {
			in["space_id"] = sp
			// one import at a time: two imports of the same record race in the worker (which one "imports" and
			// which one "updates" depends on scheduling), on both sides alike
			if out := r.call(caller, "import.start", in); out["status"] == "queued" {
				r.e.settleDbg(r.f.owner.ws)
			}
		}
		for _, c := range aiCallers {
			start(c, map[string]any{"source": "trello", "content": trello(cards)})
		}
		for _, in := range []map[string]any{
			{"source": "trello", "content": trello(`[]`)}, {"source": "trello", "content": `{"cards":[]}`}, {"source": "trello", "content": `not json`}, {"source": "trello", "content": `[]`},
			{"source": "trello", "content": `{"cards":[{"id":"1","name":"x"}]}`, "batch_size": 5}, {"source": "trello", "content": `{"cards":[{"id":"1","name":"x"}]}`, "batch_size": 4294967295}, {"source": "trello", "content": `{"cards":[{"id":"1","name":"x"}]}`, "batch_size": 4294967296},
			{"source": "trello", "content": `{"cards":[{"id":"1","name":"x"}]}`, "batch_size": -1}, {"source": "trello", "content": `{"cards":[{"id":"1","name":"x"}]}`, "batch_size": 1.5},
			{"source": "trello", "content": `{"cards":[{"id":"1","name":"x"}]}`, "mapping": map[string]any{"title": "T"}}, {"source": "trello", "content": `{"cards":[{"id":"1","name":"x"}]}`, "mapping": map[string]any{"id": "T"}},
			{"source": "trello", "content": `{"cards":[{"id":"1","name":"x"}]}`, "mapping": "x"},
			{"source": "trello", "content": `{"cards":[{"id":"1","name":"x"}]}`, "jira": map[string]any{"base_url": "https://x.atlassian.net", "email": "a@b.c", "api_token": "x", "jql": "project=X"}},
			{"source": "trello"}, {"source": "trello", "content": nil}, {"source": "trello", "content": 5}, {"source": "nope", "content": "x"}, {"source": "", "content": "x"}, {"content": "x"},
			{"source": "notion", "content": pages}, {"source": "confluence", "content": pages}, {"source": "notion", "content": `{"pages":{}}`}, {"source": "notion", "content": `{"nopages":1}`}, {"source": "notion", "content": `{"pages":[]}`}, {"source": "notion", "content": `{"pages":[5,"x",null]}`},
			{"source": "notion", "content": "# Hello ünï\n\nbody"}, {"source": "notion", "content": "# \nempty heading"}, {"source": "notion", "content": "# ---\nonly punctuation"}, {"source": "confluence", "content": "<html><title>T ünï</title><body>x</body></html>"},
			{"source": "confluence", "content": "<h1>Head</h1><p>x</p>"}, {"source": "notion", "content": "{bad"}, {"source": "notion", "content": "[1,2"},
			{"source": "csv", "content": "Key,Name,Status,Tags,Other\n1,Fix ünï,Open,\"a,b\",x\n2,\"Quoted \"\"q\"\"\",Done,,y\n3, ,Open,,z\n\n4,Short\n5,Last,Open,t,u,extra\n", "mapping": mapping},
			{"source": "csv", "content": "Name\nA\nB\n", "mapping": map[string]any{"title": "Name", "target": "pages"}}, {"source": "csv", "content": "Name\nA\n", "mapping": map[string]any{"title": "Name", "target": "bogus"}}, {"source": "csv", "content": "Name\nA\n", "mapping": map[string]any{"title": "Nope"}},
			{"source": "csv", "content": "Name\nA\n", "mapping": map[string]any{"title": "Name", "status": "Nope"}}, {"source": "csv", "content": "Name\nA\n"}, {"source": "csv", "content": "Name\nA\n", "mapping": nil},
			{"source": "csv", "content": "Name\n", "mapping": map[string]any{"title": "Name"}}, {"source": "csv", "content": "", "mapping": map[string]any{"title": "Name"}},
			{"source": "csv", "content": "\uFEFFName\nA\n", "mapping": map[string]any{"title": "Name"}}, {"source": "csv", "content": "Name\rA\rB\r", "mapping": map[string]any{"title": "Name"}},
			{"source": "csv", "content": "a;b\n1;2\n", "mapping": map[string]any{"title": "a;b"}},
			{"source": "csv", "content": "Name\n\"unterminated\n", "mapping": map[string]any{"title": "Name"}}, {"source": "csv", "content": "Name\nA\"b\n", "mapping": map[string]any{"title": "Name"}},
			{"source": "csv", "content": "Name\nA\n", "mapping": map[string]any{"title": 5}}, {"source": "csv", "content": "Name\nA\n", "mapping": map[string]any{"title": nil}},
			{"source": "jira", "content": `{"issues":[{"key":"ENG-1","fields":{"summary":"Jira one","status":{"name":"To Do"}}},{"key":"ENG-2","fields":{"summary":"two"}}]}`},
			{"source": "jira", "content": `{"issues":[]}`}, {"source": "jira", "content": `nope`}, {"source": "jira", "jira": map[string]any{"base_url": "https://x.atlassian.net", "email": "a@b.c", "api_token": "x", "jql": "project=X"}},
			{"source": "jira", "jira": map[string]any{"base_url": "http://127.0.0.1", "email": "a", "api_token": "x", "jql": "x"}}, {"source": "jira", "jira": map[string]any{"base_url": 5}},
		} {
			start("owner", in)
		}
		for _, c := range []string{"member", "guest", "other", "agent", "apikey"} {
			start(c, map[string]any{"source": "csv", "content": "Name\nA\n", "mapping": map[string]any{"title": "Name"}})
		}
		r.call("owner", "import.start", map[string]any{"source": "trello", "content": `{"cards":[{"id":"1","name":"x"}]}`, "space_id": ph})
		r.call("owner", "import.start", map[string]any{"source": "trello", "content": `{"cards":[{"id":"1","name":"x"}]}`, "space_id": r.v("prv")})
		r.call("other", "import.start", map[string]any{"source": "trello", "content": `{"cards":[{"id":"1","name":"x"}]}`, "space_id": sp})
		for _, body := range []string{`{}`, `[]`, `null`, `{"source":"trello"}`, `{"space_id":"x","source":"trello","content":"{}"}`, `{"source":"trello","content":"{\"cards\":[{\"id\":\"1\",\"name\":\"x\"}]}","space_id":"` + strings.ToUpper(sp) + `"}`} {
			r.raw("owner", "import.start", body)
		}
		r.e.settleDbg(r.f.owner.ws) // the Rust worker runs every queued import.run, whichever server enqueued it
		jobs := r.list("owner", "import.list", map[string]any{})
		for _, c := range aiCallers {
			r.call(c, "import.list", map[string]any{})
			for i, j := range jobs {
				if i%6 == 0 {
					r.call(c, "import.status", map[string]any{"id": j["id"]})
				}
			}
			r.call(c, "import.status", map[string]any{"id": ph})
		}
		for _, j := range jobs {
			r.call("owner", "import.status", map[string]any{"id": j["id"]})
		}
		for _, body := range []string{`{}`, `[]`, `{"id":"x"}`, `{"id":null}`} {
			r.raw("owner", "import.status", body)
			r.raw("owner", "import.list", body)
		}
		r.call("owner", "import.list", map[string]any{"x": 1})
		_ = json.Marshal
	}
	runTwin(t, script, map[string]string{
		"jobs":     `select (to_jsonb(j) - 'id' - 'created_at' - 'finished_at')::text from importers.jobs j order by j.input::text, j.created_at`,
		"notes":    `select (to_jsonb(n) - 'search' - 'content_ydoc')::text from core.notes n order by n.title, n.created_at`,
		"items":    `select count(*)::text from projects.items`,
		"jobqueue": `select kind || ' ' || count(*) from core.jobs where kind = 'import.run' group by kind`,
	}, 40, 60)
}
