package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/modules/sites"
)

// webRiskStub answers Web Risk lookups: a URL containing "bad." is a threat for 30 minutes; every call is counted.
type webRiskStub struct {
	mu    sync.Mutex
	calls []string
	key   string
}

func newWebRiskStub(t *testing.T) *webRiskStub {
	t.Helper()
	st := &webRiskStub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := r.URL.Query().Get("uri")
		st.mu.Lock()
		st.calls = append(st.calls, u)
		st.key = r.Header.Get("X-Goog-Api-Key")
		st.mu.Unlock()
		if strings.Contains(u, "//bad.") {
			_, _ = w.Write([]byte(`{"threat":{"threatTypes":["MALWARE"],"expireTime":"` + time.Now().Add(30*time.Minute).UTC().Format(time.RFC3339Nano) + `"}}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	old, oldURL := sites.WebRiskHTTP, sites.WebRiskURL
	sites.WebRiskHTTP, sites.WebRiskURL = srv.Client(), srv.URL
	t.Cleanup(func() { srv.Close(); sites.WebRiskHTTP, sites.WebRiskURL = old, oldURL })
	t.Setenv("WEBRISK_API_KEY", "wr-key-for-tests")
	return st
}

func (s *webRiskStub) n() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.calls) }

type abuseEnv struct {
	*pubEnv
	sub string
	wr  *webRiskStub
}

func newAbuseEnv(t *testing.T) *abuseEnv {
	t.Helper()
	t.Setenv("SITES_ABUSE_SCAN", "1")
	t.Setenv("SITES_WEBRISK_DAILY", "100000")
	t.Setenv("SITES_WEBRISK_MONTHLY", "10000000")
	p := newPubEnv(t)
	a := &abuseEnv{pubEnv: p, sub: uniq(), wr: newWebRiskStub(t)}
	return a
}

func (a *abuseEnv) setHero(props map[string]any) {
	a.t.Helper()
	hero := blockOf(a.getData(), "Hero")
	a.ok(a.owner, "site.edit", map[string]any{"id": a.site, "expected_version_id": a.draft(),
		"ops": []map[string]any{{"tool": "update_block_props", "input": map[string]any{"id": hero, "props": props}}}})
}

func (a *abuseEnv) scan() bool {
	a.t.Helper()
	ok, err := sites.ScanSite(context.Background(), a.pool, a.owner.ws, a.site)
	if err != nil {
		a.t.Fatal(err)
	}
	return ok
}

// admin runs SQL over the owner connection (the app role cannot write what these tests need to age).
func (a *abuseEnv) admin(q string, args ...any) {
	a.t.Helper()
	c, err := pgx.Connect(context.Background(), os.Getenv("DATABASE_ADMIN_URL"))
	if err != nil {
		a.t.Fatal(err)
	}
	defer c.Close(context.Background())
	if _, err := c.Exec(context.Background(), q, args...); err != nil {
		a.t.Fatal(err)
	}
}

func (a *abuseEnv) list(status string) []any {
	a.t.Helper()
	in := map[string]any{}
	if status != "" {
		in["status"] = status
	}
	return a.ok(a.owner, "site.abuse_list", in)["cases"].([]any)
}

// openFor counts the open cases of this test's site (the queue is platform-wide).
func (a *abuseEnv) openFor() int {
	n := 0
	for _, c := range a.list("open") {
		if c.(map[string]any)["site_id"] == a.site {
			n++
		}
	}
	return n
}

func (a *abuseEnv) caseID() string {
	a.t.Helper()
	for _, c := range a.list("") {
		if m := c.(map[string]any); m["site_id"] == a.site {
			return m["id"].(string)
		}
	}
	a.t.Fatal("no case for the site")
	return ""
}

func (a *abuseEnv) act(id, action, note string) resp {
	return a.call(a.owner, "site.abuse_act", map[string]any{"id": id, "action": action, "note": note})
}

func (a *abuseEnv) blocked() []any {
	_, m := a.publicGet(a.sub)
	return m["blocked_links"].([]any)
}

// A phishing-looking site is flagged and a Web Risk match hides the link; nothing is taken down by itself.
func TestSiteAbuseScanFlagsAndHidesLinks(t *testing.T) {
	a := newAbuseEnv(t)
	a.setHero(map[string]any{"title": "Official bank login", "subtitle": "Enter your password and OTP to verify your account.", "cta_label": "Open", "cta_href": "https://bad.example/login?x=1"})
	a.publishOK(a.sub)
	if st, m := a.publicGet(a.sub); st != 200 || len(m["blocked_links"].([]any)) != 0 {
		t.Fatalf("before the scan nothing is hidden: %d %v", st, m["blocked_links"])
	}
	if !a.scan() {
		t.Fatal("scan stored nothing")
	}
	eq(t, a.wr.key, "wr-key-for-tests")
	if b := a.blocked(); len(b) != 1 || b[0] != "https://bad.example/login" {
		t.Fatalf("the canonical URL is hidden on the public read: %v", b)
	}
	if st, _ := a.publicGet(a.sub); st != 200 {
		t.Fatal("a hidden link is not a takedown")
	}
	cs := a.list("open")
	if a.openFor() != 1 || cs[0].(map[string]any)["score"].(float64) < 100 {
		t.Fatalf("one open case, at the top of the queue: %v", cs)
	}
	// who may review
	a.fail(a.mia, "site.abuse_list", map[string]any{}, 403)
	id := a.caseID()
	a.fail(a.mia, "site.abuse_get", map[string]any{"id": id}, 403)
	a.fail(a.mia, "site.abuse_act", map[string]any{"id": id, "action": "takedown", "note": "nope"}, 403)

	got := a.ok(a.owner, "site.abuse_get", map[string]any{"id": id})
	var rules []string
	for _, s := range got["signals"].([]any) {
		rules = append(rules, s.(map[string]any)["rule"].(string))
	}
	if !strings.Contains(strings.Join(rules, ","), "web_risk") || !strings.Contains(strings.Join(rules, ","), "R1") {
		t.Fatalf("signals: %v", rules)
	}
	eq(t, got["status"], "open")

	// the owner banner
	banner := a.get(a.owner, a.site)["abuse"].(map[string]any)
	eq(t, banner["case_id"], id)
	eq(t, banner["blocked_links"].([]any)[0], "https://bad.example/login")

	// a scan of an unchanged site asks Web Risk again only after the cache duration
	calls := a.wr.n()
	a.scan()
	eq(t, a.wr.n(), calls)
	a.admin("update sites.abuse_links set expires_at = now() - interval '1 minute'")
	a.scan()
	if a.wr.n() != calls+1 {
		t.Fatalf("an expired answer is asked again: %d -> %d", calls, a.wr.n())
	}
}

// Reviewer decisions, the owner's appeal and who sees what.
func TestSiteAbuseReviewAndAppeal(t *testing.T) {
	a := newAbuseEnv(t)
	a.setHero(map[string]any{"title": "<img src=x onerror=alert(1)> login", "subtitle": "Enter your password and your OTP now. Guaranteed returns, double your money.", "cta_label": "Go", "cta_href": "https://bit.ly/abc"})
	a.publishOK(a.sub)
	a.scan()
	id := a.caseID()

	// evidence is plain text, with an escaped copy
	for _, s := range a.ok(a.owner, "site.abuse_get", map[string]any{"id": id})["signals"].([]any) {
		m := s.(map[string]any)
		if strings.Contains(m["snippet_escaped"].(string), "<") {
			t.Fatalf("escaped evidence still has markup: %v", m)
		}
	}

	// dismiss is tied to the published version: a rescan of the same version leaves it closed, a new version is looked at again
	a.fail(a.owner, "site.abuse_act", map[string]any{"id": id, "action": "dismiss", "note": "x"}, 400)
	eq(t, a.act(id, "dismiss", "looks like a real bank branch").status, 200)
	eq(t, a.ok(a.owner, "site.abuse_get", map[string]any{"id": id})["status"], "dismissed")
	if a.scan() {
		t.Fatal("a dismissed version is not scanned again")
	}
	eq(t, a.openFor(), 0)
	a.setHero(map[string]any{"title": "Bank login page"})
	a.publishOK("")
	a.scan()
	eq(t, a.caseID(), id) // the same case is reopened
	eq(t, a.ok(a.owner, "site.abuse_get", map[string]any{"id": id})["status"], "open")
	eq(t, a.act(id, "dismiss", "again").status, 200)
	eq(t, a.act(id, "dismiss", "twice").status, 409)

	// takedown, owner told in the app and by mail, one appeal per takedown
	a.setHero(map[string]any{"title": "Bank login page 2"})
	a.publishOK("")
	a.scan()
	eq(t, a.act(id, "restore", "not down").status, 409) // not taken down
	adminOK(t, a.act(id, "takedown", "phishing: fake bank page"))
	if st, _ := a.publicGet(a.sub); st != 404 {
		t.Fatalf("taken down: %d", st)
	}
	eq(t, a.get(a.owner, a.site)["site"].(map[string]any)["taken_down"].(map[string]any)["reason"], "phishing: fake bank page")
	eq(t, a.sql("select count(*)::text from core.notifications where kind = 'site_abuse' and workspace_id = $1::uuid", a.owner.ws), "1")
	eq(t, a.sql("select count(*)::text from core.jobs where kind = 'mail.send' and payload->>'subject' = 'Your website was taken offline' and workspace_id = $1::uuid", a.owner.ws), "1")
	if a.scan() {
		t.Fatal("a site that is down is not scanned")
	}
	banner := a.get(a.owner, a.site)["abuse"].(map[string]any)
	eq(t, banner["appealed"], false)
	a.fail(a.mia, "site.abuse_appeal", map[string]any{"id": a.site, "note": "please restore"}, 403) // members cannot appeal
	adminOK(t, a.call(a.owner, "site.abuse_appeal", map[string]any{"id": a.site, "note": "We are a real shop, please look again."}))
	a.fail(a.owner, "site.abuse_appeal", map[string]any{"id": a.site, "note": "once more"}, 409)
	eq(t, a.get(a.owner, a.site)["abuse"].(map[string]any)["appealed"], true)
	got := a.ok(a.owner, "site.abuse_get", map[string]any{"id": id})
	eq(t, got["appeal"].(map[string]any)["note"], "We are a real shop, please look again.")
	eq(t, len(got["history"].([]any)), 3) // dismiss, dismiss, takedown
}

// Restore goes through the platform function, tells the owner, and stays quiet for that version.
func TestSiteAbuseRestore(t *testing.T) {
	a := newAbuseEnv(t)
	a.setHero(map[string]any{"title": "Win", "subtitle": "Guaranteed returns on usdt. Double your money. Daily profit.", "cta_label": "Go", "cta_href": "https://bit.ly/q"})
	a.publishOK(a.sub)
	a.scan()
	id := a.caseID()
	adminOK(t, a.act(id, "takedown", "investment scam"))
	adminOK(t, a.act(id, "restore", "appeal accepted"))
	if st, _ := a.publicGet(a.sub); st != 200 {
		t.Fatalf("restored: %d", st)
	}
	eq(t, a.ok(a.owner, "site.abuse_get", map[string]any{"id": id})["status"], "restored")
	if a.scan() {
		t.Fatal("a restored version is not flagged again")
	}
	eq(t, a.sql("select count(*)::text from core.notifications where kind = 'site_abuse' and workspace_id = $1::uuid", a.owner.ws), "2")
}

// Visitor reports: validation, honeypot, the per-address and platform limits, and at most 20 points.
func TestSiteAbuseReports(t *testing.T) {
	a := newAbuseEnv(t)
	a.publishOK(a.sub)
	report := func(ip string, body map[string]any) int {
		st, _, _ := a.hit("POST", "/api/sites/report/"+a.site, body, map[string]string{"Content-Type": "application/json", "X-Forwarded-For": ip})
		return st
	}
	good := map[string]any{"reason": "phishing", "details": "asks for my OTP", "url": "https://x.example"}
	eq(t, report("10.0.0.1", map[string]any{"reason": "nonsense"}), 400)
	eq(t, report("10.0.0.1", map[string]any{"reason": "scam", "company_website": "http://spam"}), 200)
	eq(t, a.sql("select count(*)::text from sites.abuse_reports where site_id = $1::uuid", a.site), "0") // honeypot stores nothing
	for i := 0; i < 3; i++ {
		eq(t, report("10.0.0.1", good), 200)
	}
	eq(t, report("10.0.0.1", good), 429) // 3 per address per site per day
	eq(t, report("10.0.0.2", good), 200)
	eq(t, a.sql("select count(distinct ip_hash)::text from sites.abuse_reports where site_id = $1::uuid", a.site), "2")
	if st, _, _ := a.hit("POST", "/api/sites/report/00000000-0000-0000-0000-000000000000", good, nil); st != 404 {
		t.Fatalf("unknown site: %d", st)
	}
	// reports alone never reach the threshold; they add at most 20 to what the rules found
	a.scan()
	eq(t, a.openFor(), 0)
	a.setHero(map[string]any{"title": "Bank login", "subtitle": "Enter your password to verify your account", "cta_label": "Go", "cta_href": "https://bit.ly/aa"})
	a.publishOK("")
	for i := 3; i < 8; i++ {
		report("10.0.0."+string(rune('0'+i)), good)
	}
	a.scan()
	var c map[string]any
	for _, x := range a.list("open") {
		if x.(map[string]any)["site_id"] == a.site {
			c = x.(map[string]any)
		}
	}
	scan := a.sql("select scan_score::text from sites.abuse_cases where site_id = $1::uuid", a.site)
	if int(c["score"].(float64)) != atoi(scan)+20 {
		t.Fatalf("reports add at most 20: score %v scan %s", c["score"], scan)
	}
	got := a.ok(a.owner, "site.abuse_get", map[string]any{"id": c["id"]})
	last := got["signals"].([]any)[len(got["signals"].([]any))-1].(map[string]any)
	eq(t, last["reported"], true) // reports come after verified evidence and are flagged

	// off unless switched on
	t.Setenv("SITES_ABUSE_SCAN", "")
	eq(t, report("10.0.0.9", good), 404)
	a.fail(a.owner, "site.abuse_list", map[string]any{}, 404)
	_, m := a.publicGet(a.sub)
	eq(t, len(m["blocked_links"].([]any)), 0)
	if err := sites.AbuseSweep(context.Background(), a.pool); err != nil {
		t.Fatal(err)
	}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// The per-scan and daily caps, the unchecked list, and the sweep's choice of sites and its 180-day retention.
func TestSiteAbuseCapsSweepRetention(t *testing.T) {
	a := newAbuseEnv(t)
	a.setHero(map[string]any{"title": "Links", "subtitle": "Login with your password https://one.example/x and https://two.example/y", "cta_label": "Go", "cta_href": "https://bad.example/a"})
	a.publishOK(a.sub)
	// the daily cap lets one lookup through: the other two links are listed as not checked
	t.Setenv("SITES_WEBRISK_DAILY", "1")
	a.admin("delete from core.rate_buckets where key = 'webrisk:day'")
	a.scan()
	eq(t, a.wr.n(), 1)
	eq(t, a.sql("select jsonb_array_length(unchecked)::text from sites.abuse_cases where site_id = $1::uuid", a.site), "2")
	eq(t, a.ok(a.owner, "site.abuse_get", map[string]any{"id": a.caseID()})["not_checked"].([]any)[0], "https://one.example/x")
	t.Setenv("SITES_WEBRISK_DAILY", "100000")
	a.admin("delete from sites.abuse_cases where site_id = $1::uuid", a.site)

	// the sweep scans a never-scanned site, skips a scanned one, and rescans after a new publish
	a.admin("delete from sites.abuse_scans where site_id = $1::uuid", a.site)
	if err := sites.AbuseSweep(context.Background(), a.pool); err != nil {
		t.Fatal(err)
	}
	eq(t, a.sql("select count(*)::text from sites.abuse_scans where site_id = $1::uuid", a.site), "1")
	before := a.sql("select scanned_at::text from sites.abuse_scans where site_id = $1::uuid", a.site)
	if err := sites.AbuseSweep(context.Background(), a.pool); err != nil {
		t.Fatal(err)
	}
	eq(t, a.sql("select scanned_at::text from sites.abuse_scans where site_id = $1::uuid", a.site), before)
	a.setHero(map[string]any{"title": "Links 2"})
	a.publishOK("")
	if err := sites.AbuseSweep(context.Background(), a.pool); err != nil {
		t.Fatal(err)
	}
	eq(t, a.sql("select (published_version_id = (select published_version_id from sites.sites where id = $1::uuid))::text from sites.abuse_scans where site_id = $1::uuid", a.site), "true")

	// evidence older than 180 days goes; the case row stays
	id := a.caseID()
	a.admin("update sites.abuse_cases set status = 'dismissed', updated_at = now() - interval '200 days' where id = $1::uuid", id)
	a.admin("insert into sites.abuse_reports (workspace_id, site_id, case_id, ip_hash, reason, created_at) values ($1::uuid, $2::uuid, $3::uuid, 'h', 'old', now() - interval '200 days')", a.owner.ws, a.site, id)
	a.admin("delete from sites.abuse_scans where site_id = $1::uuid", a.site)
	if err := sites.AbuseSweep(context.Background(), a.pool); err != nil {
		t.Fatal(err)
	}
	eq(t, a.sql("select count(*)::text from sites.abuse_reports where site_id = $1::uuid and reason = 'old'", a.site), "0")
	eq(t, a.sql("select count(*)::text from sites.abuse_signals where case_id = $1::uuid and created_at < now() - interval '100 days'", id), "0")
}

// The report limits live in the function: direct calls past them are refused (3 per address per site per day, 500 per hour).
func TestSiteAbuseReportLimitsInSQL(t *testing.T) {
	a := newAbuseEnv(t)
	a.publishOK(a.sub)
	call := func(ip string) string {
		var r string
		if err := a.pool.QueryRow(context.Background(), "select sites.abuse_report_add($1::uuid, $2, 'x')", a.site, ip).Scan(&r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	for i := 0; i < 3; i++ {
		eq(t, call("h1"), "ok")
	}
	eq(t, call("h1"), "rate_limited")
	eq(t, call("h2"), "ok")
	var nf string
	if err := a.pool.QueryRow(context.Background(), "select sites.abuse_report_add('00000000-0000-0000-0000-000000000000', 'h3', 'x')").Scan(&nf); err != nil {
		t.Fatal(err)
	}
	eq(t, nf, "not_found")
	a.admin(`insert into sites.abuse_reports (workspace_id, site_id, ip_hash, reason)
		select workspace_id, site_id, 'bulk' || g, 'x' from sites.abuse_reports, generate_series(1, 500) g where site_id = $1::uuid limit 500`, a.site)
	eq(t, call("h4"), "rate_limited") // platform-wide, whatever the address
	a.admin("delete from sites.abuse_reports where site_id = $1::uuid", a.site)
}

// A published-post set that changes without the revision sum changing is still noticed: unpublish one post, publish another.
func TestSiteAbusePostSetFingerprint(t *testing.T) {
	a := newAbuseEnv(t)
	post := func(title string) string {
		r := a.ok(a.owner, "site.post_save", map[string]any{"id": a.site, "title": title, "body": docOf(para(title))})
		a.ok(a.owner, "site.post_publish", map[string]any{"id": a.site, "post_id": r["id"], "expected_rev": r["rev"], "publish": true})
		return r["id"].(string)
	}
	a.publishOK(a.sub)
	p1 := post("First")
	a.scan()
	stored := a.sql("select post_rev from sites.abuse_scans where site_id = $1::uuid", a.site)
	if len(stored) != 64 {
		t.Fatalf("a SHA-256 hex fingerprint: %q", stored)
	}
	// a second post, then swap: p1 draft, p2 published with p1's revision
	p2 := post("Second")
	a.admin("update sites.posts set status = 'draft' where id = $1::uuid", p1)
	a.admin("update sites.posts set rev = (select rev from sites.posts where id = $2::uuid) where id = $1::uuid", p2, p1)
	a.scan()
	a.admin("update sites.posts set status = 'published' where id = $1::uuid", p1)
	a.admin("update sites.posts set status = 'draft' where id = $1::uuid", p2)
	a.admin("update sites.abuse_scans set scanned_at = now() - interval '1 minute' where site_id = $1::uuid", a.site)
	before := a.sql("select post_rev from sites.abuse_scans where site_id = $1::uuid", a.site)
	if err := sites.AbuseSweep(context.Background(), a.pool); err != nil {
		t.Fatal(err)
	}
	after := a.sql("select post_rev from sites.abuse_scans where site_id = $1::uuid", a.site)
	if before == after || after != stored {
		t.Fatalf("the sweep did not rescan the swapped set: stored %s before %s after %s", stored, before, after)
	}
}
