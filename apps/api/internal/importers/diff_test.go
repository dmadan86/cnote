package importers

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Differential test: the same import runs on the Rust server (committed data in its own workspace, processed by the
// shared Rust worker) and in Go (one never-committed transaction on a twin workspace); then the rows are compared
// excluding ids and timestamps.

type snapQuery struct{ name, sql string }

// $1 = starter (owner) user id.
var snapQueries = []snapQuery{
	{"job", `select jsonb_build_object('status',status,'cursor',cursor,'report',report,'source',source)::text from importers.jobs where id = $2::uuid and $1::uuid is not null`},
	{"notes", `select coalesce(jsonb_agg(jsonb_build_object('src',n.source_id,'system',n.source_system,'title',n.title,'content_md',n.content_md,'types',n.types,
	   'freshness',n.freshness,'source_url',n.source_url,'version',n.version,'verify_by',n.verify_by,
	   'parent',(select p.source_id from core.notes p where p.id = n.parent_id),'has_ydoc',n.content_ydoc is not null,
	   'owner_is_starter',n.owner_id is not distinct from $1::uuid,'created_by_is_starter',n.created_by is not distinct from $1::uuid,
	   'created_at',(select n.created_at::text from core.events e where e.note_id = n.id and e.kind = 'note.imported' and e.payload->>'original_created_at' is not null limit 1)
	 ) order by n.source_id, n.title), '[]')::text from core.notes n where n.source_system is not null`},
	{"items", `select coalesce(jsonb_agg(jsonb_build_object('src',n.source_id,'key',i.key,'number',i.number,'item_type',i.item_type,'status',i.status,
	   'status_category',i.status_category,'priority',i.priority,'assignee',i.assignee_id,'reporter',i.reporter_id,
	   'sprint',(select jsonb_build_object('name',s.name,'state',s.state,'start',s.start_date,'end',s.end_date) from projects.sprints s where s.id = i.sprint_id),
	   'version',(select jsonb_build_object('name',v.name,'released',v.released,'date',v.release_date) from projects.versions v where v.id = i.fix_version_id),
	   'component',(select c.name from projects.components c where c.id = i.component_id),
	   'estimate',i.estimate,'due',i.due_date,'labels',i.labels,'custom',i.custom,'resolved_at',i.resolved_at::text) order by n.source_id), '[]')::text
	 from projects.items i join core.notes n on n.id = i.note_id`},
	{"sprints", `select coalesce(jsonb_agg(jsonb_build_object('name',name,'state',state,'start',start_date,'end',end_date) order by name), '[]')::text from projects.sprints`},
	{"versions", `select coalesce(jsonb_agg(jsonb_build_object('name',name,'released',released,'date',release_date) order by name), '[]')::text from projects.versions`},
	{"components", `select coalesce(jsonb_agg(name order by name), '[]')::text from projects.components`},
	{"relations", `select coalesce(jsonb_agg(jsonb_build_object('a',x.a,'b',x.b,'kind',x.kind) order by x.a, x.b, x.kind), '[]')::text
	 from (select case when r.kind = 'relates' then least(a.source_id,b.source_id) else a.source_id end as a,
	   case when r.kind = 'relates' then greatest(a.source_id,b.source_id) else b.source_id end as b, r.kind
	   from core.relations r join core.notes a on a.id = r.from_id join core.notes b on b.id = r.to_id
	   where a.source_system is not null and b.source_system is not null) x`},
	{"events", `select coalesce(jsonb_agg(jsonb_build_object('note',n.source_id,'kind',e.kind,'actor_type',e.actor_type,'actor_id',e.actor_id::text,
	   'on_behalf_of_is_starter',e.on_behalf_of is not distinct from $1::uuid,'payload',e.payload,
	   'at',case when e.payload->>'at' is not null or e.payload->>'original_created_at' is not null then e.created_at::text end)
	   order by n.source_id, e.kind, e.payload::text), '[]')::text
	 from core.events e left join core.notes n on n.id = e.note_id where (n.source_system is not null or e.kind = 'import.completed') and e.kind <> 'doc.bindings_synced'`},
	{"comments", `select coalesce(jsonb_agg(jsonb_build_object('note',n.source_id,'author_id',c.author_id,'author_name',c.author_name,'body',c.body,'source_id',c.source_id)
	   order by n.source_id, c.source_id), '[]')::text from core.comments c join core.notes n on n.id = c.note_id`},
	{"attachments", `select coalesce(jsonb_agg(jsonb_build_object('note',n.source_id,'filename',a.filename,'content_type',a.content_type,'size',a.size_bytes,'key',a.storage_key)
	   order by n.source_id, a.storage_key), '[]')::text from core.attachments a join core.notes n on n.id = a.note_id`},
	{"views", `select coalesce(jsonb_agg(jsonb_build_object('name',name,'layout',layout,'query',query,'options',options,'creator_is_starter',created_by is not distinct from $1::uuid)
	   order by name, options::text), '[]')::text from core.views`},
	{"automations", `select coalesce(jsonb_agg(jsonb_build_object('name',name,'enabled',enabled,'draft',draft,'trigger',trigger,'conditions',conditions,'actions',actions,
	   'creator_is_starter',created_by is not distinct from $1::uuid) order by name), '[]')::text from projects.automations`},
	{"flags", `select coalesce(jsonb_agg(jsonb_build_object('note',n.source_id,'reason',f.reason,'resolved',f.resolved_at is not null) order by n.source_id), '[]')::text
	   from docs.freshness_flags f join core.notes n on n.id = f.note_id`},
	{"misc", `select jsonb_build_object('item_seq',(select item_seq from core.spaces where key = 'ENG'),
	   'run_jobs',(select count(*) from core.jobs where kind = 'import.run'),
	   'notes',(select count(*) from core.notes))::text`},
}

func snapshot(t *testing.T, tx pgx.Tx, ten *tenant, importID string) map[string]any {
	t.Helper()
	out := map[string]any{}
	rep := strings.NewReplacer(ten.space, "<SPACE>", ten.ws, "<WS>", importID, "<IMPORT>", ten.owner, "<OWNER>")
	for _, q := range snapQueries {
		var raw string
		var err error
		if q.name == "job" {
			err = tx.QueryRow(context.Background(), q.sql, ten.owner, importID).Scan(&raw)
		} else if strings.Contains(q.sql, "$1") {
			err = tx.QueryRow(context.Background(), q.sql, ten.owner).Scan(&raw)
		} else {
			err = tx.QueryRow(context.Background(), q.sql).Scan(&raw)
		}
		if err != nil {
			t.Fatalf("%s: %v", q.name, err)
		}
		var v any
		if err := json.Unmarshal([]byte(rep.Replace(raw)), &v); err != nil {
			t.Fatalf("%s: %v", q.name, err)
		}
		out[q.name] = v
	}
	return out
}

func short(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 1500 {
		return string(b[:1500]) + "..."
	}
	return string(b)
}

// compare reports every section that differs, with the first differing row.
func compare(t *testing.T, name string, rust, gosnap map[string]any) {
	t.Helper()
	keys := make([]string, 0, len(rust))
	for k := range rust {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if reflect.DeepEqual(rust[k], gosnap[k]) {
			continue
		}
		ra, rok := rust[k].([]any)
		ga, gok := gosnap[k].([]any)
		if rok && gok {
			t.Errorf("%s: section %s differs (rust %d rows, go %d rows)", name, k, len(ra), len(ga))
			for i := 0; i < len(ra) && i < len(ga); i++ {
				if !reflect.DeepEqual(ra[i], ga[i]) {
					t.Errorf("  first diff at row %d\n  rust: %s\n  go:   %s", i, short(ra[i]), short(ga[i]))
					break
				}
			}
			if len(ra) != len(ga) && len(ra) > 0 && len(ga) > 0 {
				longer, who := ra, "rust"
				if len(ga) > len(ra) {
					longer, who = ga, "go"
				}
				t.Errorf("  extra row in %s: %s", who, short(longer[min(len(ra), len(ga))]))
			}
			continue
		}
		t.Errorf("%s: section %s differs\n  rust: %s\n  go:   %s", name, k, short(rust[k]), short(gosnap[k]))
	}
}

// rustStart runs command import.start on the Rust server.
func (t *tenant) rustStart(source, content string, mapping any, batch any) (int, string, map[string]any) {
	in := map[string]any{"source": source, "space_id": t.space, "content": content}
	if mapping != nil {
		in["mapping"] = mapping
	}
	if batch != nil {
		in["batch_size"] = batch
	}
	st, body := t.e.call(t.cookie, t.ws, "import.start", in)
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	return st, string(body), m
}

func (t *tenant) rustWait(id string) {
	t.e.t.Helper()
	waitFor(t.e.t, "rust import "+id, 5*time.Minute, func() bool {
		st, body := t.e.call(t.cookie, t.ws, "import.status", map[string]any{"id": id})
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		return st == 200 && (m["status"] == "done" || m["status"] == "failed")
	})
}

// rejected mirrors import.start's own validation: the message Rust returns, or "" when the import would start.
func rejected(source, content string, mapping any) string {
	var input any
	input, _ = decodeJSON(string(jsonOf(map[string]any{"content": content, "mapping": mapping})))
	p, err := parseInput(source, input)
	if err != nil {
		return err.Error()
	}
	if len(p.Recs) == 0 {
		return "nothing to import: the content has no records"
	}
	return ""
}

func jsonOf(v any) []byte { b, _ := json.Marshal(v); return b }

type diffCase struct {
	name, source, content string
	mapping, batch        any
}

func runDiff(t *testing.T, cases []diffCase) {
	e := newEnv(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e.t = t
			rt := e.newTenant()
			content := rt.fix(c.content)
			rst, rbody, rm := rt.rustStart(c.source, content, c.mapping, c.batch)
			if msg := rejected(c.source, content, c.mapping); msg != "" {
				if rst != 400 {
					t.Fatalf("Go rejects (%q) but Rust started: %d %s", msg, rst, rbody)
				}
				if rmsg := rustMessage(rbody); rmsg != msg && !strings.Contains(msg, "not valid JSON") && !strings.HasPrefix(rmsg, "invalid input:") {
					t.Fatalf("rejection text differs\n rust: %v\n go:   %s", rmsg, msg)
				}
				return
			}
			if rst != 200 {
				t.Fatalf("Rust rejected an import Go accepts: %d %s", rst, rbody)
			}
			rid := rm["id"].(string)
			rt.rustWait(rid)

			gt := e.newTenant(rt)
			g := gt.goRun()
			gid := g.start(c.source, content, c.mapping, c.batch)
			g.all(gid)

			rtx := rt.begin()
			defer func() { _ = rtx.Rollback(context.Background()) }()
			rs, gs := snapshot(t, rtx, rt, rid), snapshot(t, g.tx, gt, gid)
			if rs["job"].(map[string]any)["status"] != "done" {
				t.Logf("note: rust job status %v", rs["job"])
			}
			compare(t, c.name, rs, gs)
			t.Logf("report: %s", short(rs["job"].(map[string]any)["report"]))
		})
	}
}

func TestDifferentialJira(t *testing.T) {
	runDiff(t, []diffCase{
		{name: "repo-fixture", source: "jira", content: fixture(t, "jira_export.json")},
		{name: "repo-fixture-batch1", source: "jira", content: fixture(t, "jira_export.json"), batch: 1},
		{name: "edge-unicode-malformed", source: "jira", content: jiraEdge},
		{name: "edge-batch-size-3", source: "jira", content: jiraEdge, batch: 3},
		{name: "bare-array", source: "jira", content: `[{"key":"A-1","fields":{"summary":"x"}}]`},
		{name: "empty-issues", source: "jira", content: `{"issues":[]}`},
		{name: "only-bad-issues", source: "jira", content: `{"issues":[{"key":"A-1"}]}`},
		{name: "no-issues-key", source: "jira", content: `{"foo":1}`},
		{name: "malformed-json", source: "jira", content: `{"issues":[`},
		{name: "trailing-garbage", source: "jira", content: `{"issues":[{"key":"A-1","fields":{"summary":"x"}}]} x`},
		{name: "large-1100", source: "jira", content: jiraLarge(1100)},
		{name: "large-batch-1000-clamp", source: "jira", content: jiraLarge(130), batch: 5000},
		{name: "large-batch-0-clamp", source: "jira", content: jiraLarge(12), batch: 0},
	})
}

func TestDifferentialTrello(t *testing.T) {
	runDiff(t, []diffCase{
		{name: "repo-fixture", source: "trello", content: fixture(t, "trello_board.json")},
		{name: "edge", source: "trello", content: trelloEdge},
		{name: "edge-batch-2", source: "trello", content: trelloEdge, batch: 2},
		{name: "empty", source: "trello", content: `{"cards":[]}`},
		{name: "all-closed", source: "trello", content: `{"cards":[{"id":"a","name":"x","closed":true}]}`},
		{name: "malformed", source: "trello", content: `{"cards":`},
		{name: "large-600", source: "trello", content: trelloLarge(600)},
	})
}

func TestDifferentialNotion(t *testing.T) {
	runDiff(t, []diffCase{
		{name: "single-markdown", source: "notion", content: "# Onboarding\n\nWelcome to **Acme**.\n\n- step one\n"},
		{name: "markdown-no-h1", source: "notion", content: "just text\nmore ü\n"},
		{name: "markdown-crlf-unicode", source: "notion", content: "\r\n  # Ünï 日本語\r\n\r\nbody 🚀\r\n"},
		{name: "markdown-bom", source: "notion", content: "\ufeff# Title\n\nbody"},
		{name: "markdown-slug-empty-title", source: "notion", content: "# !!!\n\nbody"},
		{name: "html-single", source: "notion", content: "<html><head><title>Ünï T</title></head><body><h1>H</h1><p>b</p></body></html>"},
		{name: "json-pages", source: "notion", content: notionEdge()},
		{name: "json-bare-array", source: "notion", content: `[{"id":"a","title":"A","markdown":"x"},{"id":"b","title":"B","parent_id":"a","markdown":"# B\n\ny"}]`},
		{name: "json-no-pages", source: "notion", content: `{"x":1}`},
		{name: "empty", source: "notion", content: "   "},
		{name: "json-empty-pages", source: "notion", content: `{"pages":[]}`},
		{name: "json-malformed", source: "notion", content: `{"pages":[`},
	})
}

func TestDifferentialConfluence(t *testing.T) {
	runDiff(t, []diffCase{
		{name: "repo-fixture", source: "confluence", content: fixture(t, "confluence_pages.json")},
		{name: "edge-html-pages", source: "confluence", content: confluenceEdge()},
		{name: "edge-batch-3", source: "confluence", content: confluenceEdge(), batch: 3},
		{name: "html-single", source: "confluence", content: confluenceHTML},
		{name: "html-untitled", source: "confluence", content: "<p>no title at all</p>"},
		{name: "html-unclosed-tag", source: "confluence", content: "<p>text <b"},
		{name: "large-400", source: "confluence", content: confluenceLarge(400)},
	})
}

func TestDifferentialCSV(t *testing.T) {
	runDiff(t, []diffCase{
		{name: "repo-fixture", source: "csv", content: fixture(t, "tasks.csv"), mapping: map[string]any{"id": "Ref", "title": "Summary", "description": "Details", "status": "State", "priority": "Severity", "assignee_email": "Owner", "labels": "Tags"}},
		{name: "edge-all-columns", source: "csv", content: csvEdge, mapping: allMapping},
		{name: "edge-no-id", source: "csv", content: csvEdge, mapping: map[string]any{"title": "Summary"}},
		{name: "edge-pages-target", source: "csv", content: csvEdge, mapping: map[string]any{"title": "Summary", "id": "Ref", "parent": "Parent", "description": "Details", "target": "pages"}},
		{name: "edge-batch-2", source: "csv", content: csvEdge, mapping: allMapping, batch: 2},
		{name: "bom-header", source: "csv", content: "\ufeffRef,Name\nr1,x\n", mapping: map[string]any{"title": "Name"}},
		{name: "missing-mapping", source: "csv", content: "a,b\n1,2\n"},
		{name: "missing-column", source: "csv", content: "a,b\n1,2\n", mapping: map[string]any{"title": "nope"}},
		{name: "missing-id-column", source: "csv", content: "a,b\n1,2\n", mapping: map[string]any{"title": "a", "id": "z"}},
		{name: "bad-target", source: "csv", content: "a,b\n1,2\n", mapping: map[string]any{"title": "a", "target": "other"}},
		{name: "mapping-wrong-type", source: "csv", content: "a,b\n1,2\n", mapping: map[string]any{"title": 5}},
		{name: "header-only", source: "csv", content: "a,b\n", mapping: map[string]any{"title": "a"}},
		{name: "empty", source: "csv", content: "", mapping: map[string]any{"title": "a"}},
		{name: "large-1500", source: "csv", content: csvLarge(1500), mapping: map[string]any{"id": "id", "title": "title", "description": "desc", "status": "status", "priority": "priority", "assignee_email": "owner", "labels": "tags", "parent": "parent"}},
		{name: "unknown-source-char", source: "csv", content: "t\n" + strings.Repeat("ü", 3) + "\n", mapping: map[string]any{"title": "t"}},
	})
}

var _ = fmt.Sprint

func rustMessage(body string) string {
	var m struct {
		Message string
		Error   struct{ Message string }
	}
	_ = json.Unmarshal([]byte(body), &m)
	return m.Message + m.Error.Message
}
