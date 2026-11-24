package server_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules/sites"
)

const plain = "text/plain;charset=UTF-8" // what navigator.sendBeacon sends

func freshIP() string { return fmt.Sprintf("198.51.%d.%d", rand.Intn(250), rand.Intn(250)) }

func (p *pubEnv) beacon(ip, ct, body string) int {
	p.t.Helper()
	return hit(p.t, p.api, "POST", "/api/sites/hit/"+p.site, map[string]string{"Content-Type": ct, "X-Forwarded-For": ip}, body).status
}

func (p *pubEnv) beaconJSON(ip, path, ref string) int {
	b, _ := json.Marshal(map[string]string{"path": path, "ref_host": ref})
	return p.beacon(ip, plain, string(b))
}

func (p *pubEnv) n(q string, args ...any) string { return p.sql(q, args...) }

// 1. The beacon counts page views, visits and referrers for a published site and nothing else.
func TestSiteHitCounts(t *testing.T) {
	p := newPubEnv(t)
	sub := uniq()
	ip := freshIP()
	eq(t, p.beaconJSON(ip, "/", "google.com"), 204)
	eq(t, p.n("select count(*)::text from sites.site_stats"), "0") // unpublished: nothing

	p.publishOK(sub)
	eq(t, p.beaconJSON(ip, "/", "WWW.Google.com"), 204)
	eq(t, p.n("select n::text from sites.site_stats where kind = 'page' and key = ''"), "1")
	eq(t, p.n("select n::text from sites.site_stats where kind = 'visit'"), "1")
	eq(t, p.n("select n::text from sites.site_referrers where host = 'www.google.com'"), "1")

	// a page that does not exist is not counted; the published version gets a second page
	eq(t, p.beaconJSON(ip, "/about", ""), 204)
	eq(t, p.n("select count(*)::text from sites.site_stats where key = 'about'"), "0")
	adminSQL(p.env, p.owner.ws, `update sites.site_versions set data = jsonb_set(data, '{pages}', '[{"slug":"about","title":"About","description":"","content":[]}]')
		where id = (select published_version_id from sites.sites)`)
	eq(t, p.beaconJSON(ip, "/about/?utm=1#x", ""), 204)
	eq(t, p.n("select n::text from sites.site_stats where kind = 'page' and key = 'about'"), "1")
	eq(t, p.n("select n::text from sites.site_stats where kind = 'visit'"), "2")

	// the site's own host as referrer is navigation inside the site: a view, no visit, no referrer row
	eq(t, p.beaconJSON(ip, "/", sub+"."+siteDomain), 204)
	eq(t, p.n("select n::text from sites.site_stats where kind = 'page' and key = ''"), "2")
	eq(t, p.n("select n::text from sites.site_stats where kind = 'visit'"), "2")
	// a referrer that is not a plain host (or is over 100 characters) is dropped but the visit still counts
	eq(t, p.beaconJSON(ip, "/", "javascript:alert(1)"), 204)
	eq(t, p.beaconJSON(ip, "/", strings.Repeat("a", 98)+".com"), 204)
	eq(t, p.n("select n::text from sites.site_stats where kind = 'visit'"), "4")
	eq(t, p.n("select count(*)::text from sites.site_referrers"), "1")

	// refusals: wrong type, too big, not JSON, bad path, bad site id
	eq(t, p.beacon(ip, "application/json", `{"path":"/"}`), 415)
	eq(t, p.beacon(ip, plain, `{"path":"/","ref_host":"`+strings.Repeat("a", 500)+`"}`), 413)
	eq(t, p.beacon(ip, plain, "nope"), 400)
	eq(t, p.beaconJSON(ip, "/Bad Slug!", ""), 400)
	eq(t, hit(t, p.api, "POST", "/api/sites/hit/not-an-id", map[string]string{"Content-Type": plain}, `{"path":"/"}`).status, 400)
	eq(t, hit(t, p.api, "POST", "/api/sites/hit/"+command0, map[string]string{"Content-Type": plain, "X-Forwarded-For": ip}, `{"path":"/"}`).status, 204) // unknown site: dropped

	// site.stats: members read it; the days run oldest first for 60 days
	adminSQL(p.env, p.owner.ws, "insert into sites.form_log (workspace_id, site_id, fingerprint, created_at) values ($1::uuid, $2::uuid, 'a', now()), ($1::uuid, $2::uuid, 'b', now() - interval '40 days')", p.owner.ws, p.site)
	st := p.ok(p.mia, "site.stats", map[string]any{"id": p.site})
	days := st["days"].([]any)
	eq(t, len(days), 60)
	last := days[59].(map[string]any)
	eq(t, last["visits"], float64(4))
	eq(t, last["views"], float64(5))
	pages := st["pages"].([]any)
	eq(t, pages[0].(map[string]any)["slug"], "")
	eq(t, pages[0].(map[string]any)["views"], float64(4))
	eq(t, pages[1].(map[string]any)["slug"], "about")
	eq(t, st["referrers"].([]any)[0].(map[string]any)["domain"], "www.google.com")
	eq(t, st["enquiries"], float64(1))
	other := newSiteEnvOn(t, p)
	other.fail(other.owner, "site.stats", map[string]any{"id": p.site}, 404)

	// taken down: the beacon is accepted and ignored
	adminOK(t, p.platform(p.owner, map[string]any{"subdomain": sub, "action": "takedown", "reason": "abuse report"}))
	eq(t, p.beaconJSON(ip, "/", "google.com"), 204)
	eq(t, p.n("select sum(n)::text from sites.site_stats where kind = 'visit'"), "4")
}

// 2. At most 50 distinct referrer hosts a day; the rest count as "other", even when they arrive together.
func TestSiteHitReferrerCap(t *testing.T) {
	p := newPubEnv(t)
	p.publishOK(uniq())
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := p.pool.Exec(context.Background(), "select sites.hit($1::uuid, '', $2, $3)", p.site, fmt.Sprintf("r%d.example.com", i), siteDomain); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	eq(t, p.n("select count(*)::text from sites.site_referrers where host <> 'other'"), "50")
	eq(t, p.n("select n::text from sites.site_referrers where host = 'other'"), "10")
	// a host already counted keeps counting
	if _, err := p.pool.Exec(context.Background(), "select sites.hit($1::uuid, '', 'r0.example.com', $2)", p.site, siteDomain); err != nil {
		t.Fatal(err)
	}
	eq(t, p.n("select n::text from sites.site_referrers where host = 'r0.example.com'"), "2")
	eq(t, p.n("select sum(n)::text from sites.site_stats where kind = 'visit'"), "61")
}

// 3. Limits: 120 an hour per address and 20,000 a minute for the platform; both before any write.
func TestSiteHitLimits(t *testing.T) {
	p := newPubEnv(t)
	p.publishOK(uniq())
	ip := freshIP()
	for i := 0; i < 120; i++ {
		if st := p.beaconJSON(ip, "/", ""); st != 204 {
			t.Fatalf("hit %d: %d", i, st)
		}
	}
	eq(t, p.beaconJSON(ip, "/", ""), 429)
	eq(t, p.n("select sum(n)::text from sites.site_stats where kind = 'page'"), "120")
	eq(t, p.beaconJSON(freshIP(), "/", ""), 204) // another address is fine

	key := "sitehit:all:" + time.Now().UTC().Format("200601021504")
	adminSQL(p.env, p.owner.ws, "insert into core.rate_buckets (key, bucket, n) values ($1, $2, 20000) on conflict (key, bucket) do update set n = 20000", key, db.BucketStart(time.Now(), false))
	t.Cleanup(func() { _, _ = p.pool.Exec(context.Background(), "delete from core.rate_buckets where key = $1", key) })
	eq(t, p.beaconJSON(freshIP(), "/", ""), 429)
	eq(t, p.n("select sum(n)::text from sites.site_stats where kind = 'page'"), "121")
}

// 4. The sweep drops counters older than 13 months and keeps the rest.
func TestSiteStatsSweep(t *testing.T) {
	p := newPubEnv(t)
	p.publishOK(uniq())
	admin, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_ADMIN_URL")) // the app role cannot insert counters
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	for _, q := range []string{
		`insert into sites.site_stats (workspace_id, site_id, day, kind, key, n) select $1::uuid, $2::uuid, d, 'page', '', 1 from (values (current_date - 420), (current_date - 300)) v(d)`,
		`insert into sites.site_referrers (workspace_id, site_id, day, host, n) select $1::uuid, $2::uuid, d, 'a.example.com', 1 from (values (current_date - 420), (current_date - 300)) v(d)`} {
		if _, err := admin.Exec(context.Background(), q, p.owner.ws, p.site); err != nil {
			t.Fatal(err)
		}
	}
	if err := sites.StatsSweep(context.Background(), p.pool); err != nil {
		t.Fatal(err)
	}
	eq(t, p.n("select count(*)::text from sites.site_stats"), "1")
	eq(t, p.n("select count(*)::text from sites.site_referrers"), "1")
}

func (p *pubEnv) export(w who) (int, http.Header, []byte) {
	p.t.Helper()
	req, _ := http.NewRequest("GET", p.api+"/api/w/"+p.owner.ws+"/sites/"+p.site+"/export", nil)
	w.with(req.Header)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, b
}

func zipFiles(t *testing.T, b []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		r, _ := f.Open()
		out[f.Name], _ = io.ReadAll(r)
		r.Close()
	}
	return out
}

// 5. The download: admins only, people only, published only, one at a time; the zip holds site.json and the version's photos.
func TestSiteExport(t *testing.T) {
	p := newPubEnv(t)
	sub := uniq()
	if st, _, _ := p.export(p.owner); st != 409 {
		t.Fatalf("unpublished: %d", st)
	}
	used := adminOK(t, p.upload(p.owner, pngBytes(t, 40, 30)))["image"].(map[string]any)["id"].(string)
	page := adminOK(t, p.upload(p.owner, pngBytes(t, 20, 20)))["image"].(map[string]any)["id"].(string)
	unused := adminOK(t, p.upload(p.owner, pngBytes(t, 10, 10)))["image"].(map[string]any)["id"].(string)
	hero := blockOf(p.getData(), "Hero")
	p.ok(p.owner, "site.edit", map[string]any{"id": p.site, "expected_version_id": p.draft(), "ops": []map[string]any{{"tool": "update_block_props", "input": map[string]any{"id": hero, "props": map[string]any{"image": used}}}}})
	p.publishOK(sub)
	adminSQL(p.env, p.owner.ws, `update sites.site_versions set data = jsonb_set(data, '{pages}', jsonb_build_array(jsonb_build_object('slug','about','title','About','description','',
		'content', jsonb_build_array(jsonb_build_object('type','Hero','props', jsonb_build_object('id', gen_random_uuid()::text, 'image', $1::text))))))
		where id = (select published_version_id from sites.sites)`, page)

	if st, _, _ := p.export(p.mia); st != 403 {
		t.Fatalf("member: %d", st)
	}
	if st, _, _ := p.export(who{}); st != 401 {
		t.Fatalf("no session: %d", st)
	}
	if st, _, _ := p.export(p.apiKey(p.owner)); st != 403 {
		t.Fatalf("api key: %d", st)
	}
	st, h, body := p.export(p.owner)
	eq(t, st, 200)
	eq(t, h.Get("Content-Type"), "application/zip")
	if !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") {
		t.Fatalf("disposition: %q", h.Get("Content-Disposition"))
	}
	files := zipFiles(t, body)
	eq(t, len(files), 3)
	if len(files["images/"+used+".png"]) == 0 || len(files["images/"+page+".png"]) == 0 || files["images/"+unused+".png"] != nil {
		t.Fatalf("images: %v", files)
	}
	var doc struct {
		VersionID string `json:"published_version_id"`
		Data      struct {
			Content []any `json:"content"`
			Pages   []any `json:"pages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(files["site.json"], &doc); err != nil {
		t.Fatal(err)
	}
	eq(t, doc.VersionID, p.get2()["published_version_id"])
	if len(doc.Data.Content) == 0 || len(doc.Data.Pages) != 1 {
		t.Fatalf("site.json: %s", files["site.json"])
	}

	// one export at a time: while another holds the site's lock this one is a 409
	conn, err := p.pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(context.Background(), "select pg_advisory_lock(hashtextextended($1, 9))", "siteexport:"+p.site); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := p.export(p.owner); st != 409 {
		t.Fatalf("concurrent: %d", st)
	}
	if _, err := conn.Exec(context.Background(), "select pg_advisory_unlock(hashtextextended($1, 9))", "siteexport:"+p.site); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := p.export(p.owner); st != 200 {
		t.Fatalf("after release: %d", st)
	}
}

// 6. At most 40 photos and 20 MB.
func TestSiteExportLimits(t *testing.T) {
	p := newPubEnv(t)
	p.publishOK(uniq())
	put := func(count, size int) {
		adminSQL(p.env, p.owner.ws, "delete from core.attachments where site_id = $1::uuid", p.site)
		adminSQL(p.env, p.owner.ws, `insert into core.attachments (workspace_id, site_id, filename, content_type, size_bytes, storage_key, data)
			select $1::uuid, $2::uuid, 'x.png', 'image/png', $4::int, 'pg', repeat('a', $4::int)::bytea from generate_series(1, $3)`, p.owner.ws, p.site, count, size)
		adminSQL(p.env, p.owner.ws, `update sites.site_versions set data = jsonb_set(data, '{pages}', jsonb_build_array(jsonb_build_object('slug','big','title','Big','description','',
			'content', (select jsonb_agg(jsonb_build_object('type','Hero','props', jsonb_build_object('id', gen_random_uuid()::text, 'image', a.id::text))) from core.attachments a where a.site_id = $1::uuid))))
			where id = (select published_version_id from sites.sites)`, p.site)
	}
	put(41, 10)
	if st, _, b := p.export(p.owner); st != 400 {
		t.Fatalf("41 photos: %d %s", st, b)
	}
	put(3, 7<<20)
	if st, _, b := p.export(p.owner); st != 400 {
		t.Fatalf("21 MB: %d %.200s", st, b)
	}
	put(40, 100)
	if st, _, b := p.export(p.owner); st != 200 {
		t.Fatalf("40 photos: %d %s", st, b)
	}
}
