package server_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/modules/sites"
)

// Phase 3a (docs/design/site-builder-phase3.md sections 2, 3, 17): catalogue, blog, share images.

func (p *pubEnv) item(sku string, price, rate int) string {
	p.t.Helper()
	return p.ok(p.owner, "stock_item.create", map[string]any{"sku": sku, "name": "Item " + sku, "hsn_sac": "8471", "unit": "pcs", "price_minor": price, "gst_rate_bp": rate})["id"].(string)
}

func (p *pubEnv) addCatalogueBlock() {
	p.t.Helper()
	adminOK(p.t, p.editOps(p.owner, p.site, p.draft(), map[string]any{"tool": "add_block", "input": map[string]any{"type": "Catalogue", "props": map[string]any{"title": "Our products"}}}))
}

func (p *pubEnv) publicItems(sub string) (map[string]any, []any) {
	p.t.Helper()
	st, m := p.publicGet(sub)
	eq(p.t, st, 200)
	cat := m["catalog"].(map[string]any)
	return cat, cat["items"].([]any)
}

func (p *pubEnv) postHit(path string, body map[string]any) (int, map[string]any) {
	p.t.Helper()
	st, _, b := p.hit("POST", path, body, map[string]string{"x-sites-token": siteToken, "Content-Type": "application/json"})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return st, m
}

func TestSiteCatalogue(t *testing.T) {
	p := newPubEnv(t)
	a, b := p.item("cat-a", 100_000, 1800), p.item("cat-b", 5_000, 500)
	p.addCatalogueBlock()
	// nothing is picked by default; members read, a stranger-free check of the candidates
	g := p.ok(p.owner, "site.catalog_get", map[string]any{"id": p.site})
	eq(t, len(g["items"].([]any)), 0)
	eq(t, len(g["candidates"].([]any)), 2)
	// pick one item with a category and description; the other is never public
	p.ok(p.mia, "site.catalog_set", map[string]any{"id": p.site, "items": []map[string]any{{"item_id": a, "category": "Tools", "description": "Solid"}}})
	sub := uniq()
	p.publishOK(sub)
	cat, items := p.publicItems(sub)
	eq(t, len(items), 1)
	it := items[0].(map[string]any)
	eq(t, it["name"], "Item cat-a")
	eq(t, it["price_minor"], float64(118_000)) // price plus 18% GST
	eq(t, cat["price_mode"], "show_incl")
	if strings.Contains(string(jsonOf(cat)), b) || it["gst_rate_bp"] != nil || it["sku"] != nil {
		t.Fatalf("unpicked item or private field leaked: %v", cat)
	}
	// a price change in billing is invisible until Update prices
	p.ok(p.owner, "stock_item.update", map[string]any{"id": a, "price_minor": 200_000})
	_, items = p.publicItems(sub)
	eq(t, items[0].(map[string]any)["price_minor"], float64(118_000))
	g = p.ok(p.owner, "site.catalog_get", map[string]any{"id": p.site})
	eq(t, g["changed"], float64(1))
	eq(t, g["changes"].([]any)[0].(map[string]any)["kind"], "price")
	eq(t, len(g["snapshot"].([]any)), 1)
	p.fail(p.mia, "site.catalog_sync", map[string]any{"id": p.site}, 403)
	s := p.ok(p.owner, "site.catalog_sync", map[string]any{"id": p.site})
	eq(t, s["count"], float64(1))
	_, items = p.publicItems(sub)
	eq(t, items[0].(map[string]any)["price_minor"], float64(236_000))
	// plus GST shows the bare price; hide stores and shows no number anywhere
	p.ok(p.owner, "site.catalog_set", map[string]any{"id": p.site, "price_mode": "show_plus", "items": []map[string]any{{"item_id": a}, {"item_id": b}}})
	p.ok(p.owner, "site.catalog_sync", map[string]any{"id": p.site})
	cat, items = p.publicItems(sub)
	eq(t, cat["price_mode"], "show_plus")
	eq(t, items[0].(map[string]any)["price_minor"], float64(200_000))
	p.ok(p.owner, "site.catalog_set", map[string]any{"id": p.site, "price_mode": "hide", "items": []map[string]any{{"item_id": a}, {"item_id": b}}})
	p.ok(p.owner, "site.catalog_sync", map[string]any{"id": p.site})
	cat, _ = p.publicItems(sub)
	eq(t, cat["price_mode"], "hide")
	if strings.Contains(string(jsonOf(cat)), "price_minor") {
		t.Fatalf("hide mode leaked a price: %v", cat)
	}
	// "Ask a question": the server names the item; a forged or unknown id is ignored
	f := goodForm("is it in stock?")
	f["item_id"], f["consent_id"], f["consent_text"] = a, "enquiry-v1", "I AGREE TO ANYTHING"
	if st, _ := p.form("10.1.0.1", f); st != 200 {
		t.Fatalf("item form: %d", st)
	}
	eq(t, p.sql("select count(*)::text from crm.activities where summary = E'About: Item cat-a\\nis it in stock?'"), "1")
	eq(t, p.sql("select count(*)::text from crm.activities where summary like '%I AGREE TO ANYTHING%'"), "0")
	eq(t, p.sql("select count(*)::text from crm.activities where summary like 'Consent given%enquiry%' or summary like 'Consent given on the website%'"), "1")
	f = goodForm("forged")
	f["item_id"] = "00000000-0000-4000-8000-000000000000"
	if st, _ := p.form("10.1.0.2", f); st != 200 {
		t.Fatal(st)
	}
	eq(t, p.sql("select count(*)::text from crm.activities where summary like 'About:%forged'"), "0")
	f = goodForm("bad consent")
	f["consent_id"] = "made-up"
	if st, _ := p.form("10.1.0.3", f); st != 400 {
		t.Fatalf("unknown consent id: %d", st)
	}
	// the plan limit, and an archived or unknown item cannot be picked
	var many []map[string]any
	for i := 0; i < 13; i++ {
		many = append(many, map[string]any{"item_id": fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)})
	}
	if r := p.call(p.owner, "site.catalog_set", map[string]any{"id": p.site, "items": many}); r.status == 200 {
		t.Fatal("13 items accepted on the included plan")
	}
	p.fail(p.owner, "site.catalog_set", map[string]any{"id": p.site, "items": []map[string]any{{"item_id": "00000000-0000-4000-8000-000000000000"}}}, 400)
	// another workspace reads nothing of this site
	other := p.signup("Other", true)
	if r := p.cmd(p.gosv.URL, other, other.ws, "site.catalog_get", map[string]any{"id": p.site}, nil); r.status == 200 {
		t.Fatal("another workspace read the catalogue")
	}
}

func jsonOf(v any) []byte { b, _ := json.Marshal(v); return b }

func para(text string) map[string]any {
	return map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": text}}}
}
func docOf(blocks ...any) map[string]any { return map[string]any{"type": "doc", "content": blocks} }

func TestSiteBlog(t *testing.T) {
	p := newPubEnv(t)
	save := func(w who, in map[string]any) resp { in["id"] = p.site; return p.call(w, "site.post_save", in) }
	r := adminOK(t, save(p.mia, map[string]any{"title": "Hello World", "body": docOf(para("first"))}))
	post := r["id"].(string)
	eq(t, r["slug"], "hello-world")
	eq(t, save(p.mia, map[string]any{"title": "Hello World", "body": docOf(para("again"))}).json()["slug"], "hello-world-2")
	// grammar: rejected, never stripped
	eq(t, save(p.mia, map[string]any{"title": "x", "body": docOf(map[string]any{"type": "iframe"})}).status, 400)
	eq(t, save(p.mia, map[string]any{"title": "x", "body": docOf(map[string]any{"type": "image", "attrs": map[string]any{"image_id": "deadbeef-dead-4eef-8ead-beefdeadbeef", "alt": ""}})}).status, 400)
	// an update needs the revision; a stale one conflicts
	eq(t, save(p.mia, map[string]any{"post_id": post, "title": "Hello", "body": docOf(para("v2"))}).status, 400)
	eq(t, save(p.mia, map[string]any{"post_id": post, "expected_rev": 5, "title": "Hello", "body": docOf(para("v2"))}).status, 409)
	r = adminOK(t, save(p.mia, map[string]any{"post_id": post, "expected_rev": 0, "title": "Hello World", "body": docOf(para("v2"))}))
	eq(t, r["rev"], float64(1))
	// a member cannot publish; nothing publishes before the site does
	pub := func(w who, rev int, on bool) resp {
		return p.call(w, "site.post_publish", map[string]any{"id": p.site, "post_id": post, "expected_rev": rev, "publish": on})
	}
	eq(t, pub(p.mia, 1, true).status, 403)
	eq(t, pub(p.owner, 1, true).status, 409)
	sub := uniq()
	p.publishOK(sub)
	host := sub + "." + siteDomain
	if st, _ := p.postHit("/api/sites/public_posts", map[string]any{"host": host, "page": 1}); st != 404 {
		t.Fatalf("posts before any is published: %d", st)
	}
	eq(t, pub(p.owner, 0, true).status, 409) // stale rev
	r = adminOK(t, pub(p.owner, 1, true))
	eq(t, r["status"], "published")
	st, list := p.postHit("/api/sites/public_posts", map[string]any{"host": host, "page": 1})
	eq(t, st, 200)
	eq(t, len(list["posts"].([]any)), 1)
	eq(t, list["total"], float64(1))
	if _, has := list["posts"].([]any)[0].(map[string]any)["body"]; has {
		t.Fatal("the list carries bodies")
	}
	st, one := p.postHit("/api/sites/public_post", map[string]any{"host": host, "slug": "hello-world"})
	eq(t, st, 200)
	eq(t, one["post"].(map[string]any)["body"].(map[string]any)["content"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"], "v2")
	if st, _ := p.postHit("/api/sites/public_post", map[string]any{"host": host, "slug": "hello-world-2"}); st != 404 {
		t.Fatalf("a draft was served: %d", st)
	}
	// an edit of a live post waits as pending; the public copy is unchanged until Update post
	r = adminOK(t, save(p.mia, map[string]any{"post_id": post, "expected_rev": 2, "title": "Hello again", "slug": "hello-again", "body": docOf(para("v3"))}))
	if r["pending"] == nil || r["title"] != "Hello again" {
		t.Fatalf("pending: %v", r)
	}
	_, one = p.postHit("/api/sites/public_post", map[string]any{"host": host, "slug": "hello-world"})
	eq(t, one["post"].(map[string]any)["title"], "Hello World")
	r = adminOK(t, pub(p.owner, 3, true))
	eq(t, r["slug"], "hello-again")
	if r["pending"] != nil {
		t.Fatal("pending kept")
	}
	if st, _ := p.postHit("/api/sites/public_post", map[string]any{"host": host, "slug": "hello-world"}); st != 404 {
		t.Fatalf("old slug served: %d", st)
	}
	// a future date is refused; the token is required; a takedown hides the posts
	eq(t, p.call(p.owner, "site.post_publish", map[string]any{"id": p.site, "post_id": post, "expected_rev": 4, "publish": true, "published_at": "2999-01-01T00:00:00Z"}).status, 400)
	if st, _, _ := p.hit("POST", "/api/sites/public_posts", map[string]any{"host": host}, nil); st != 404 {
		t.Fatalf("no token: %d", st)
	}
	// to drafts and back out of public view; delete removes at once
	adminOK(t, pub(p.owner, 4, false))
	if st, _ := p.postHit("/api/sites/public_post", map[string]any{"host": host, "slug": "hello-again"}); st != 404 {
		t.Fatalf("unpublished post served: %d", st)
	}
	eq(t, p.call(p.mia, "site.post_delete", map[string]any{"id": p.site, "post_id": post, "expected_rev": 5}).status, 403)
	adminOK(t, p.call(p.owner, "site.post_delete", map[string]any{"id": p.site, "post_id": post, "expected_rev": 5}))
	eq(t, p.call(p.owner, "site.post_get", map[string]any{"id": p.site, "post_id": post}).status, 404)
	// plan limit on all posts
	adminSQL(p.env, p.owner.ws, `insert into sites.posts (workspace_id, site_id, slug, title, body, created_by)
		select $1::uuid, $2::uuid, 'p' || g, 'P', '{"type":"doc"}', $3::uuid from generate_series(1, 30) g`, p.owner.ws, p.site, p.owner.userID)
	if r := save(p.owner, map[string]any{"title": "one more", "body": docOf(para("x"))}); r.status != command.PlanLimit.Status() {
		t.Fatalf("31st post: %d", r.status)
	}
	eq(t, p.sql("select count(*)::text from sites.posts"), "31") // nothing was added past the limit
}

func TestSitePostDefaultReservedSlug(t *testing.T) {
	p := newPubEnv(t)
	save := func(fields map[string]any) resp {
		fields["id"] = p.site
		return p.call(p.owner, "site.post_save", fields)
	}
	eq(t, adminOK(t, save(map[string]any{"title": "Page"}))["slug"], "page-2")
	eq(t, adminOK(t, save(map[string]any{"title": "Page"}))["slug"], "page-3")
	eq(t, save(map[string]any{"title": "Reserved", "slug": "page"}).status, 400)
}

func TestSitePostImagesAndShareImages(t *testing.T) {
	p := newPubEnv(t)
	sub := uniq()
	up := func() string {
		return adminOK(t, p.upload(p.owner, pngBytes(t, 8, 8)))["image"].(map[string]any)["id"].(string)
	}
	live, draftOnly := up(), up()
	r := adminOK(t, p.call(p.owner, "site.post_save", map[string]any{"id": p.site, "title": "With photo", "body": docOf(map[string]any{"type": "image", "attrs": map[string]any{"image_id": live, "alt": "a"}})}))
	post := r["id"].(string)
	p.publishOK(sub)
	img := func(id string) int {
		st, _, _ := p.hit("GET", "/api/sites/img/"+p.site+"/"+id, nil, nil)
		return st
	}
	eq(t, img(live), 404) // only a draft post uses it
	adminOK(t, p.call(p.owner, "site.post_publish", map[string]any{"id": p.site, "post_id": post, "expected_rev": 0, "publish": true}))
	eq(t, img(live), 200)
	eq(t, img(draftOnly), 404)
	// an edit that swaps the photo waits as pending: the new one is not public yet
	adminOK(t, p.call(p.owner, "site.post_save", map[string]any{"id": p.site, "post_id": post, "expected_rev": 1, "title": "With photo", "body": docOf(map[string]any{"type": "image", "attrs": map[string]any{"image_id": draftOnly, "alt": ""}})}))
	eq(t, img(draftOnly), 404)
	// catalogue photo: public once the snapshot holds it
	it := p.item("img-1", 1000, 0)
	p.addCatalogueBlock()
	adminOK(t, p.call(p.owner, "site.catalog_set", map[string]any{"id": p.site, "items": []map[string]any{{"item_id": it, "image_id": draftOnly}}}))
	eq(t, img(draftOnly), 404)
	adminOK(t, p.publish(p.owner, ""))
	eq(t, img(draftOnly), 200)

	// share images: rendered by the web app at publish through the job; a failed render means no image
	png, _ := base64.StdEncoding.DecodeString(pngBytes(t, 16, 16))
	var calls int
	var last map[string]any
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_og/render" || r.Header.Get("x-sites-token") != siteToken {
			w.WriteHeader(404)
			return
		}
		calls++
		_ = json.NewDecoder(r.Body).Decode(&last)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer web.Close()
	t.Setenv("SITES_WEB_URL", web.URL)
	t.Setenv("PUBLIC_BASE_URL", "http://app.example.test")
	adminOK(t, p.publish(p.owner, ""))
	var payload string
	payload = p.sql("select payload::text from core.jobs where kind = 'site.og' order by id desc limit 1")
	run := func(payload string) error {
		reg := command.NewRegistry(modules.Commands()...)
		ws := p.owner.ws
		return sites.RunOGJob(p.pool, reg)(context.Background(), jobs.Job{Kind: sites.OGJobKind, WorkspaceID: &ws, Payload: json.RawMessage(payload)})
	}
	if err := run(payload); err != nil {
		t.Fatal(err)
	}
	st, m := p.publicGet(sub)
	eq(t, st, 200)
	og := m["page_og_images"].(map[string]any)
	home, _ := og[""].(string)
	if home == "" || calls < 2 { // Home and the published post
		t.Fatalf("no home share image: %v calls %d", og, calls)
	}
	eq(t, last["kind"], "post")
	eq(t, img(home), 200)
	// A post changed while its image was rendering must retain its previous image.
	previous := p.sql("select og_image_id::text from sites.posts where id = $1::uuid", post)
	web.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		adminSQL(p.env, p.owner.ws, "update sites.posts set rev = rev + 1 where id = $1::uuid", post)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	})
	if err := run(`{"site_id":"` + p.site + `","post_id":"` + post + `"}`); err != nil {
		t.Fatal(err)
	}
	eq(t, p.sql("select og_image_id::text from sites.posts where id = $1::uuid", post), previous)
	// a stale job (an older version) renders nothing; a failing web app leaves no image
	n := calls
	if err := run(`{"site_id":"` + p.site + `","version_id":"00000000-0000-4000-8000-000000000000"}`); err != nil || calls != n {
		t.Fatalf("stale job: %v calls %d", err, calls)
	}
	web.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	if err := run(payload); err != nil {
		t.Fatal(err)
	}
	_, m = p.publicGet(sub)
	if len(m["page_og_images"].(map[string]any)) != 0 {
		t.Fatalf("a failed render left an image: %v", m["page_og_images"])
	}
	eq(t, img(home), 404)
	// only the published post keeps its image (a site publish re-renders posts that have none); the page images were pruned
	eq(t, p.sql("select count(*)::text from core.attachments where generated"), "1")
	eq(t, p.sql("select count(*)::text from sites.posts where og_image_id is not null"), "1")
	eq(t, p.sql("select count(*)::text from core.attachments where site_id = $1::uuid and not generated", p.site), "2")
}
