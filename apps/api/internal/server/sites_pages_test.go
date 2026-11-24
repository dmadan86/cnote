package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cnote/api/internal/modules/sites"
)

func (p *pubEnv) setPro(on bool) {
	p.t.Helper()
	if _, err := adminPool(p.t).Exec(context.Background(), `insert into sites.site_platform (workspace_id, site_id, pro) values ($1::uuid, $2::uuid, $3)
		on conflict (site_id) do update set pro = $3`, p.owner.ws, p.site, on); err != nil {
		p.t.Fatal(err)
	}
}

func (p *pubEnv) ops(w who, source string, ops ...map[string]any) resp {
	return p.call(w, "site.edit", map[string]any{"id": p.site, "expected_version_id": p.draft(), "expected_rev": p.get(w, p.site)["site"].(map[string]any)["rev"], "source": source, "ops": ops})
}

func addPageOp(title string) map[string]any {
	return map[string]any{"tool": "add_page", "input": map[string]any{"title": title}}
}

func TestSitePagesEditVersionsRestore(t *testing.T) {
	p := newPubEnv(t)
	start := p.draft()
	r := adminOK(t, p.ops(p.mia, "", addPageOp("About us")))
	data := r["data"].(map[string]any)
	if len(data["pages"].([]any)) != 1 || r["site"].(map[string]any)["draft_version_id"] == start {
		t.Fatalf("add_page: %v", r)
	}
	// a stale expected version is a conflict; a bad op is a 400 and writes nothing
	if x := p.call(p.mia, "site.edit", map[string]any{"id": p.site, "expected_version_id": start, "ops": []any{addPageOp("x")}}); x.status != 409 {
		t.Fatalf("stale: %d", x.status)
	}
	if x := p.ops(p.mia, "", map[string]any{"tool": "set_page", "input": map[string]any{"page": "nope", "title": "x"}}); x.status != 400 {
		t.Fatalf("bad page: %d", x.status)
	}
	if x := p.ops(p.mia, "bogus", addPageOp("x")); x.status != 400 {
		t.Fatalf("bad source: %d", x.status)
	}
	vs := p.call(p.owner, "site.versions", map[string]any{"id": p.site}).jsonList()
	if len(vs) != 2 {
		t.Fatalf("versions %v", vs)
	}
	top := vs[0]
	if top["source"] != "edit" || top["summary"] == "" || !top["is_draft"].(bool) || top["is_published"].(bool) || top["created_by"].(map[string]any)["name"] != "Mia" {
		t.Fatalf("top version %v", top)
	}
	if old := p.call(p.owner, "site.versions", map[string]any{"id": p.site, "before_seq": 2}).jsonList(); len(old) != 1 || old[0]["source"] != "template" {
		t.Fatalf("page 2 %v", old)
	}
	// restore as new keeps history: the first version comes back on top
	before := p.versions()
	rr := adminOK(t, p.call(p.mia, "site.restore", map[string]any{"id": p.site, "expected_version_id": p.draft(), "version_id": start, "as_new": true}))
	if _, has := rr["data"].(map[string]any)["pages"]; has && len(rr["data"].(map[string]any)["pages"].([]any)) != 0 {
		t.Fatalf("restore data %v", rr["data"])
	}
	if p.sql("select source from sites.site_versions where id = $1::uuid", p.draft()) != "restore" || p.versions() == before {
		t.Fatal("restore did not add a version")
	}
	if got := p.sql("select summary from sites.site_versions where id = $1::uuid", p.draft()); got != "Restored version 1" {
		t.Fatalf("summary %q", got)
	}
	// the other mode still moves the pointer only
	n := p.versions()
	adminOK(t, p.call(p.mia, "site.restore", map[string]any{"id": p.site, "expected_version_id": p.draft(), "version_id": start}))
	if p.versions() != n || p.draft() != start {
		t.Fatal("pointer restore changed history")
	}
	// another workspace cannot read the versions
	o := p.signup("Other", true)
	if x := p.cmd(p.gosv.URL, o, o.ws, "site.versions", map[string]any{"id": p.site}, nil); x.status == 200 {
		t.Fatalf("foreign workspace read versions: %s", x.body)
	}
}

func TestSiteEditorCoalesce(t *testing.T) {
	p := newPubEnv(t)
	hero := blockOf(p.get(p.owner, p.site)["data"], "Hero")
	setTitle := func(w who, title string) map[string]any {
		d := p.get(w, p.site)["data"].(map[string]any)
		content := d["content"].([]any)
		for _, b := range content {
			if m := b.(map[string]any); m["type"] == "Hero" {
				m["props"].(map[string]any)["title"] = title
			}
		}
		return adminOK(t, p.ops(w, "editor", map[string]any{"tool": "set_content", "input": map[string]any{"page": "", "content": content}}))
	}
	n := p.versions()
	r1 := setTitle(p.owner, "One")
	n1 := p.versions()
	if n1 == n || p.sql("select source from sites.site_versions where id = $1::uuid", p.draft()) != "editor" {
		t.Fatal("first editor save must add a version")
	}
	id1 := p.draft()
	setTitle(p.owner, "Two")
	if p.versions() != n1 || p.draft() != id1 {
		t.Fatal("second save by the same person within 5 minutes must replace the version")
	}
	if propOf(p.get(p.owner, p.site)["data"], "Hero", "title") != "Two" || blockOf(r1["data"], "Hero") != hero {
		t.Fatal("content or ids lost")
	}
	if got := p.sql("select summary from sites.site_versions where id = $1::uuid", id1); got != "Updated Hero" {
		t.Fatalf("summary %q", got)
	}
	// another person starts a new version
	setTitle(p.mia, "Three")
	if p.versions() == n1 || p.draft() == id1 {
		t.Fatal("a different person must not overwrite")
	}
	// a published version is never replaced
	p.publishOK(uniq())
	pub := p.draft()
	setTitle(p.mia, "Four")
	if p.draft() == pub || propOf(p.sql2(pub), "Hero", "title") != "Three" {
		t.Fatal("published version was changed")
	}
	// a version that is only an old time slot is replaced no more: age it past five minutes
	d := p.draft()
	adminSQL(p.env, p.owner.ws, "update sites.site_versions set updated_at = now() - interval '6 minutes' where id = $1::uuid", d)
	m := p.versions()
	setTitle(p.mia, "Five")
	if p.versions() == m {
		t.Fatal("an old editor version was replaced")
	}
}

func (p *pubEnv) sql2(version string) any {
	var out map[string]any
	_ = json.Unmarshal([]byte(p.sql("select data::text from sites.site_versions where id = $1::uuid", version)), &out)
	return out
}

func TestSitePagePlanLimits(t *testing.T) {
	p := newPubEnv(t)
	for i := 0; i < 4; i++ {
		adminOK(t, p.ops(p.owner, "", addPageOp("Page "+string(rune('a'+i)))))
	}
	if x := p.ops(p.owner, "", addPageOp("Fifth")); x.status != 402 && x.status != 403 && x.status != 400 && x.status != 429 {
		t.Fatalf("fifth page: %d %s", x.status, x.body)
	} else if !strings.Contains(string(x.body), "Home plus 4") {
		t.Fatalf("message: %s", x.body)
	}
	p.setPro(true)
	for i := 0; i < 6; i++ {
		adminOK(t, p.ops(p.owner, "", addPageOp("More "+string(rune('a'+i)))))
	}
	if x := p.ops(p.owner, "", addPageOp("Eleventh")); x.status == 200 {
		t.Fatal("11th page accepted on Pro")
	}
	// Pro lapses: the 10 pages stay editable, publish refuses until they fit
	over := p.draft()
	p.setPro(false)
	adminOK(t, p.ops(p.owner, "", map[string]any{"tool": "set_page", "input": map[string]any{"page": "page-a", "title": "Renamed"}}))
	if x := p.publish(p.owner, uniq()); x.status == 200 || !strings.Contains(string(x.body), "Remove pages") {
		t.Fatalf("publish over the limit: %d %s", x.status, x.body)
	}
	if x := p.ops(p.owner, "", addPageOp("Twelfth")); x.status == 200 {
		t.Fatal("page added over the limit after lapse")
	}
	// restoring a version with more pages than now and than the plan is refused; going down is fine
	for i := 0; i < 6; i++ {
		adminOK(t, p.ops(p.owner, "", map[string]any{"tool": "remove_page", "input": map[string]any{"page": []string{"more-a", "more-b", "more-c", "more-d", "more-e", "more-f"}[i]}}))
	}
	if x := p.call(p.owner, "site.restore", map[string]any{"id": p.site, "expected_version_id": p.draft(), "version_id": over, "as_new": true}); x.status == 200 {
		t.Fatal("restored 10 pages onto a 4-page plan")
	}
	adminOK(t, p.call(p.owner, "site.publish", map[string]any{"id": p.site, "expected_version_id": p.draft(), "subdomain": uniq()}))
}

func TestSiteVersionPrune(t *testing.T) {
	p := newPubEnv(t)
	adminOK(t, p.ops(p.owner, "", addPageOp("About")))
	first := p.sql("select id::text from sites.site_versions order by seq limit 1")
	p.publishOK(uniq()) // the published version must survive
	pubID := p.draft()
	// a long chain of editor saves on top, each the child of the last
	for i := 0; i < 230; i++ {
		adminSQL(p.env, p.owner.ws, `insert into sites.site_versions (workspace_id, site_id, seq, parent_id, source, summary, data, created_by)
			select workspace_id, site_id, seq + 1, id, 'editor', 'x', data, created_by from sites.site_versions where site_id = $1::uuid order by seq desc limit 1`, p.site)
	}
	adminSQL(p.env, p.owner.ws, "update sites.sites set draft_version_id = (select id from sites.site_versions where site_id = $1::uuid order by seq desc limit 1) where id = $1::uuid", p.site)
	_ = p.versions()
	if err := sites.PruneVersions(context.Background(), p.pool); err != nil {
		t.Fatal(err)
	}
	after := p.versions()
	if after != "201" {
		t.Fatalf("after %s", after)
	}
	if p.sql("select count(*) from sites.site_versions where id = $1::uuid", first) != "0" {
		t.Fatal("old unreferenced version kept")
	}
	if p.sql("select count(*) from sites.site_versions where id = $1::uuid", pubID) != "1" {
		t.Fatal("published version pruned")
	}
	// the chain is still connected: every kept version has its parent kept (or none)
	if p.sql(`select count(*) from sites.site_versions v where v.parent_id is not null and not exists (select 1 from sites.site_versions q where q.id = v.parent_id)`) != "0" {
		t.Fatal("dangling parent")
	}
	if got := p.sql("select count(*) from sites.site_versions where site_id = $1::uuid and seq > (select max(seq) from sites.site_versions) - 200", p.site); got != "200" {
		t.Fatalf("newest 200 not kept: %s", got)
	}
	if err := sites.PruneVersions(context.Background(), p.pool); err != nil || p.versions() != after {
		t.Fatalf("second prune changed: %v %s", err, p.versions())
	}
}

func TestSiteEditorStaleRevision(t *testing.T) {
	p := newPubEnv(t)
	data := p.get(p.owner, p.site)
	content := data["data"].(map[string]any)["content"].([]any)
	var hero map[string]any
	for _, block := range content {
		b := block.(map[string]any)
		if b["type"] == "Hero" {
			hero = b["props"].(map[string]any)
			break
		}
	}
	hero["title"] = "First save"
	op := map[string]any{"tool": "set_content", "input": map[string]any{"page": "", "content": content}}
	adminOK(t, p.ops(p.owner, "editor", op))
	snapshot := p.get(p.owner, p.site)["site"].(map[string]any)
	args := map[string]any{"id": p.site, "expected_version_id": snapshot["draft_version_id"], "expected_rev": snapshot["rev"], "source": "editor", "ops": []any{op}}
	hero["title"] = "Second save"
	adminOK(t, p.call(p.owner, "site.edit", args))
	hero["title"] = "Stale save"
	eq(t, p.call(p.owner, "site.edit", args).status, 409)
}
