package server_test

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules/sites"
	"github.com/cnote/api/internal/server"
)

// domDNS answers from maps; a name with no entry is "no such record", a name in fail is a lookup failure.
type domDNS struct {
	mu                 sync.Mutex
	txt, host          map[string][]string
	cname              map[string]string
	fail               map[string]error
	txtCalls, hostCall int
}

func newDomDNS() *domDNS {
	return &domDNS{txt: map[string][]string{}, host: map[string][]string{}, cname: map[string]string{}, fail: map[string]error{}}
}

func (f *domDNS) get(name string) (error, bool) {
	if e := f.fail[name]; e != nil {
		return e, true
	}
	return &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}, false
}

func (f *domDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.txt[name]; ok {
		return v, nil
	}
	e, _ := f.get(name)
	return nil, e
}

func (f *domDNS) LookupCNAME(_ context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.cname[name]; ok {
		return v + ".", nil
	}
	if e, bad := f.get(name); bad {
		return "", e
	}
	return name + ".", nil // no CNAME: the resolver answers the name itself
}

func (f *domDNS) LookupHost(_ context.Context, name string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.host[name]; ok {
		return v, nil
	}
	e, _ := f.get(name)
	return nil, e
}

func (f *domDNS) set(fn func()) { f.mu.Lock(); fn(); f.mu.Unlock() }

const apexIP = "203.0.113.10"

type domEnv struct {
	*pubEnv
	sub string
	dns *domDNS
}

func newDomEnv(t *testing.T) *domEnv {
	t.Helper()
	p := newPubEnv(t)
	t.Setenv("SITES_CUSTOM_DOMAINS", "1")
	t.Setenv("SITES_APEX_IP", apexIP+", 2001:db8::10")
	d := &domEnv{p, uniq(), newDomDNS()}
	t.Cleanup(sites.SetResolver(d.dns))
	return d
}

func (d *domEnv) pro(on bool) {
	d.t.Helper()
	a := map[bool]string{true: "pro_on", false: "pro_off"}[on]
	adminOK(d.t, d.platform(d.owner, map[string]any{"workspace_slug": d.sql("select slug from core.workspaces"), "action": a, "reason": "domain test"}))
}

func (d *domEnv) set(host string) resp {
	return d.call(d.owner, "site.domain_set", map[string]any{"id": d.site, "domain": host})
}

func (d *domEnv) setOK(host string) map[string]any {
	d.t.Helper()
	return adminOK(d.t, d.set(host))
}

func (d *domEnv) check(extra ...string) map[string]any {
	d.t.Helper()
	in := map[string]any{"id": d.site}
	if len(extra) > 0 {
		in["domain"] = extra[0]
	}
	return adminOK(d.t, d.call(d.owner, "site.domain_check", in))
}

func (d *domEnv) remove(host string) resp {
	in := map[string]any{"id": d.site}
	if host != "" {
		in["domain"] = host
	}
	return d.call(d.owner, "site.domain_remove", in)
}

// prove puts the TXT record the command asked for into DNS.
func (d *domEnv) prove(host string, set map[string]any) {
	tok := set["domain"].(map[string]any)["txt_token"].(string)
	d.dns.set(func() { d.dns.txt["_cnote-verify."+host] = []string{"cnote-verify=" + tok} })
}

func (d *domEnv) routeWWW(host string) {
	d.dns.set(func() { d.dns.cname[host] = d.sub + "." + siteDomain })
}

func (d *domEnv) ask(host string) bool {
	d.t.Helper()
	ok, err := sites.TLSAsk(context.Background(), d.pool, host)
	if err != nil {
		d.t.Fatal(err)
	}
	return ok
}

func (d *domEnv) lookup(host string) (int, map[string]any) {
	d.t.Helper()
	st, _, b := d.hit("POST", "/api/sites/public_get", map[string]string{"host": host}, map[string]string{"x-sites-token": siteToken, "Content-Type": "application/json"})
	return st, resp{st, b}.json()
}

func (d *domEnv) claim(host, col string) string {
	return d.sql("select "+col+"::text from sites.domain_claims where hostname = $1", host)
}

// agePastDue makes the daily sweep pick the claim up again.
func (d *domEnv) agePastDue(host string) {
	adminSQL(d.env, d.owner.ws, "update sites.domain_claims set checked_at = now() - interval '2 days' where hostname = $1", host)
}

func domHost() string { return "www." + uniq() + ".test" }

// 1. The whole life of a hostname: gates, the proof, routing, publish, takedown, Pro lapse, disconnect.
func TestSiteDomainLifecycle(t *testing.T) {
	d := newDomEnv(t)
	host := domHost()

	d.fail(d.owner, "site.domain_set", map[string]any{"id": d.site, "domain": host}, 402) // not Pro
	d.pro(true)
	d.fail(d.owner, "site.domain_set", map[string]any{"id": d.site, "domain": host}, 400) // no address yet: never published
	d.publishOK(d.sub)
	d.fail(d.mia, "site.domain_set", map[string]any{"id": d.site, "domain": host}, 403) // admins only
	if r := d.cmd(d.gosv.URL, d.apiKey(d.owner), d.owner.ws, "site.domain_set", map[string]any{"id": d.site, "domain": host}, nil); r.status != 403 {
		t.Fatalf("api key: %d", r.status) // people only
	}
	d.fail(d.mia, "site.domain_remove", map[string]any{"id": d.site}, 403)
	t.Setenv("SITES_CUSTOM_DOMAINS", "")
	d.fail(d.owner, "site.domain_set", map[string]any{"id": d.site, "domain": host}, 400) // the one feature gate
	d.fail(d.owner, "site.domain_check", map[string]any{"id": d.site}, 400)
	eq(t, d.ask(host), false)
	t.Setenv("SITES_CUSTOM_DOMAINS", "1")

	set := d.setOK(" HTTPS://" + strings.ToUpper(host) + "/about?x=1 ")
	dom := set["domain"].(map[string]any)
	eq(t, dom["hostname"], host)
	eq(t, dom["status"], "pending")
	tok := dom["txt_token"].(string)
	if len(tok) != 32 {
		t.Fatalf("token %q is not 128 bits of hex", tok)
	}
	recs := set["records"].([]any)
	txt, route := recs[0].(map[string]any), recs[1].(map[string]any)
	eq(t, txt["type"], "TXT")
	eq(t, txt["name"], "_cnote-verify."+host)
	eq(t, txt["value"], "cnote-verify="+tok)
	eq(t, route["type"], "CNAME")
	eq(t, route["name"], host)
	eq(t, route["value"], d.sub+"."+siteDomain)
	eq(t, d.setOK(host)["domain"].(map[string]any)["txt_token"], tok) // the same request again keeps its token

	// nothing proves it yet; a wrong token proves nothing
	eq(t, d.check()["verified"], false)
	d.dns.set(func() { d.dns.txt["_cnote-verify."+host] = []string{"cnote-verify=" + strings.Repeat("0", 32)} })
	eq(t, d.check()["verified"], false)
	eq(t, d.ask(host), false)

	d.prove(host, set)
	r := d.check()
	eq(t, r["verified"], true)
	eq(t, r["domain"].(map[string]any)["routing"], false) // proven, not routed
	eq(t, d.ask(host), false)
	if st, _ := d.lookup(host); st != 404 {
		t.Fatalf("not routed yet: %d", st)
	}

	d.routeWWW(host)
	r = d.check(host)
	eq(t, r["domain"].(map[string]any)["routing"], true)
	eq(t, r["records"].([]any)[1].(map[string]any)["ok"], true)
	eq(t, d.ask(host), true)
	eq(t, d.ask(strings.ToUpper(host)+"."), true)
	eq(t, d.ask("other-"+host), false)
	eq(t, d.ask("x."+siteDomain), false)

	st, pub := d.lookup(host)
	eq(t, st, 200)
	eq(t, pub["site_id"], d.site)
	eq(t, pub["canonical_host"], host)
	eq(t, pub["url"], "https://"+host)
	st, pub = d.publicGet(d.sub) // the subdomain names the custom host too
	eq(t, st, 200)
	eq(t, pub["canonical_host"], host)
	if st, _ := d.lookup(strings.ToUpper(host) + "."); st != 200 {
		t.Fatalf("host spelling: %d", st)
	}

	// everything that takes the site off the air stops routing and TLS at once, and comes back with it
	stops := func(why string, off, on func()) {
		t.Helper()
		off()
		if d.ask(host) {
			t.Fatalf("%s: still approved for TLS", why)
		}
		if st, _ := d.lookup(host); st != 404 {
			t.Fatalf("%s: still routed (%d)", why, st)
		}
		on()
		if !d.ask(host) {
			t.Fatalf("%s: not back", why)
		}
		if st, _ := d.lookup(host); st != 200 {
			t.Fatalf("%s: not back (%d)", why, st)
		}
	}
	stops("unpublish", func() { adminOK(t, d.call(d.owner, "site.unpublish", map[string]any{"id": d.site})) }, func() { d.publishOK("") })
	stops("takedown", func() {
		adminOK(t, d.platform(d.owner, map[string]any{"subdomain": d.sub, "action": "takedown", "reason": "phishing report"}))
	}, func() {
		adminOK(t, d.platform(d.owner, map[string]any{"subdomain": d.sub, "action": "restore", "reason": "cleared"}))
	})
	stops("pro lapse", func() { d.pro(false) }, func() { d.pro(true) })
	_, pub = d.publicGet(d.sub)
	eq(t, pub["canonical_host"], host)
	d.pro(false)
	_, pub = d.publicGet(d.sub) // the subdomain serves the site on its own address again
	eq(t, pub["canonical_host"], nil)
	d.pro(true)

	// rename keeps the claim attached; its CNAME has to follow the new address
	old := d.sub
	d.sub = uniq()
	adminOK(t, d.platform(d.owner, map[string]any{"subdomain": old, "action": "rename", "reason": "owner asked", "new_subdomain": d.sub}))
	if st, _ := d.lookup(host); st != 200 {
		t.Fatalf("after rename: %d", st)
	}
	eq(t, d.check()["domain"].(map[string]any)["routing"], false)
	eq(t, d.ask(host), false)
	d.routeWWW(host)
	eq(t, d.check()["domain"].(map[string]any)["routing"], true)
	eq(t, d.ask(host), true)

	// disconnect
	eq(t, d.remove(host).status, 200)
	eq(t, d.ask(host), false)
	if st, _ := d.lookup(host); st != 404 {
		t.Fatalf("removed host: %d", st)
	}
	eq(t, d.remove(host).status, 404)
	_, pub = d.publicGet(d.sub)
	eq(t, pub["canonical_host"], nil)
}

// 2. Rejected hostnames (17.2).
func TestSiteDomainRejected(t *testing.T) {
	d := newDomEnv(t)
	d.pro(true)
	d.publishOK(d.sub)
	t.Setenv("PUBLIC_BASE_URL", "https://app.cnote-app.test")
	t.Setenv("APP_HOSTS", "console.cnote-other.test")
	for _, h := range []string{
		"203.0.113.5", "[2001:db8::1]", "2001:db8::1", "localhost", "intranet", "com", "co.uk", "test", // IP literals, one label, public suffixes
		siteDomain, "www." + siteDomain, "a.b." + siteDomain, "sites.example.test.", // SITES_DOMAIN and below
		"cnote-app.test", "app.cnote-app.test", "www.cnote-app.test", "api.app.cnote-app.test", // the app's registrable domain and its descendants
		"console.cnote-other.test", "cnote-other.test", // APP_HOSTS
		"xn--a.example.test", "xn--bcher-kva-.test", "exa mple.test", "-a.test", "a..test", "user@www.example.test", "www.example.test:8443", "", "   ",
	} {
		if r := d.set(h); r.status != 400 {
			t.Errorf("%q: want 400, got %d %s", h, r.status, r.body)
		}
	}
	if n := d.sql("select count(*) from sites.domain_claims"); n != "0" {
		t.Fatalf("rejected names left %s claims", n)
	}
	// punycode that is valid is stored in its ASCII form
	if h := d.setOK("www.bücher" + uniq() + ".test")["domain"].(map[string]any)["hostname"].(string); !strings.HasPrefix(h, "www.xn--bcher") {
		t.Fatalf("punycode: %s", h)
	}
	// the address without www needs SITES_APEX_IP
	t.Setenv("SITES_APEX_IP", "")
	eq(t, d.set(uniq()+".test").status, 400)
	eq(t, d.set(domHost()).status, 200)
}

// 3. Takeover attempts: a name another site holds, pending claims, a copied proof, lost claims.
func TestSiteDomainTakeover(t *testing.T) {
	a := newDomEnv(t)
	b := newDomEnv(t)
	b.dns = a.dns // one DNS for both owners
	t.Cleanup(sites.SetResolver(a.dns))
	t.Setenv("CNOTE_PLATFORM_ADMIN_USER_IDS", a.owner.userID+","+b.owner.userID)
	a.pro(true)
	a.publishOK(a.sub)
	b.pro(true)
	b.publishOK(b.sub)
	host := domHost()

	set := a.setOK(host)
	// B cannot take a name A is proving, and B's copy of A's token proves nothing for B
	eq(t, b.set(host).status, 409)
	eq(t, b.set(strings.ToUpper(host)).status, 409)
	eq(t, b.call(b.owner, "site.domain_check", map[string]any{"id": b.site}).status, 404)
	eq(t, b.remove(host).status, 404) // and cannot remove it
	eq(t, a.claim(host, "status"), "pending")
	a.prove(host, set)
	a.routeWWW(host)
	eq(t, a.check()["verified"], true)

	// a verified claim holds for good; B has the same DNS answer (A's own record) and still gets nothing
	eq(t, b.set(host).status, 409)
	eq(t, a.ask(host), true)

	// a pending claim that expires can be taken by whoever can prove the name now
	h2 := domHost()
	setA := a.setOK(h2)
	eq(t, b.set(h2).status, 409)
	adminSQL(a.env, a.owner.ws, "update sites.domain_claims set expires_at = now() - interval '1 minute' where hostname = $1", h2)
	eq(t, a.call(a.owner, "site.domain_check", map[string]any{"id": a.site, "domain": h2}).status, 404) // an expired request is gone
	setB := b.setOK(h2)
	if setB["domain"].(map[string]any)["txt_token"] == setA["domain"].(map[string]any)["txt_token"] {
		t.Fatal("a new claim must get a new token")
	}
	a.prove(h2, setA) // A's old token no longer matters
	eq(t, b.check()["verified"], false)
	b.prove(h2, setB)
	eq(t, b.check()["verified"], true)
	eq(t, a.set(h2).status, 409)

	// a lost claim frees the name
	adminSQL(b.env, b.owner.ws, "update sites.domain_claims set status = 'lost' where hostname = $1", h2)
	eq(t, a.setOK(h2)["domain"].(map[string]any)["status"], "pending")
	eq(t, b.sql("select count(*) from sites.domain_claims"), "0")

	// the web proxy only sees B's pages for B's hosts
	if st, pub := b.lookup(host); st != 200 || pub["site_id"] != a.site {
		t.Fatalf("host %s resolved to %d %v, not A's site", host, st, pub["site_id"])
	}
}

// 4. 5 hostnames per site per 30 days, kept after removal; 120 checks per hour; 50 verifications per hour platform-wide.
func TestSiteDomainLimits(t *testing.T) {
	d := newDomEnv(t)
	d.pro(true)
	d.publishOK(d.sub)
	var hosts []string
	for i := 0; i < 5; i++ {
		h := domHost()
		hosts = append(hosts, h)
		d.setOK(h)
	}
	eq(t, d.set(domHost()).status, 429)
	eq(t, d.set(hosts[0]).status, 200) // asking again for what is already pending is not a new hostname
	eq(t, d.remove("").status, 200)
	eq(t, d.sql("select count(*) from sites.domain_claims"), "0")
	eq(t, d.set(domHost()).status, 429) // removing does not give the slots back
	if _, err := adminPool(t).Exec(context.Background(), "update sites.domain_additions set created_at = now() - interval '31 days' where hostname = $1", hosts[0]); err != nil {
		t.Fatal(err)
	}
	eq(t, d.set(domHost()).status, 200)

	// checks: 120 an hour per site
	key := "sitedomain:check:" + d.site
	if _, err := d.pool.Exec(context.Background(), "insert into core.rate_buckets (key, bucket, n) values ($1, $2, 120)", key, db.BucketStart(time.Now(), false)); err != nil {
		t.Fatal(err)
	}
	eq(t, d.call(d.owner, "site.domain_check", map[string]any{"id": d.site}).status, 429)

	// verifications: a full hour makes a proven hostname wait, and it is verified once the hour has room
	if _, err := adminPool(t).Exec(context.Background(), "delete from sites.domain_additions where site_id = $1::uuid", d.site); err != nil {
		t.Fatal(err)
	}
	h := domHost()
	set := d.setOK(h)
	d.prove(h, set)
	bucket := func(n int) {
		if _, err := d.pool.Exec(context.Background(), `insert into core.rate_buckets (key, bucket, n) values ('sitedomain:verified', date_trunc('hour', now()), $1)
			on conflict (key, bucket) do update set n = $1`, n); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { bucket(0) })
	adminSQL(d.env, d.owner.ws, "delete from core.rate_buckets where key = $1", key)
	bucket(50)
	r := d.check(h)
	eq(t, r["verified"], false)
	eq(t, r["waiting"], true)
	eq(t, d.claim(h, "status"), "pending")
	bucket(49)
	r = d.check(h)
	eq(t, r["verified"], true)
	eq(t, r["waiting"], false)
	eq(t, d.sql("select n from core.rate_buckets where key = 'sitedomain:verified' and bucket = date_trunc('hour', now())"), "50")
	eq(t, d.check(h)["verified"], true) // checking a verified hostname again takes no slot
	eq(t, d.sql("select n from core.rate_buckets where key = 'sitedomain:verified' and bucket = date_trunc('hour', now())"), "50")
}

// 5. The apex: its A record must be exactly our addresses.
func TestSiteDomainApex(t *testing.T) {
	d := newDomEnv(t)
	d.pro(true)
	d.publishOK(d.sub)
	apex := uniq() + ".test"
	set := d.setOK(apex)
	route := set["records"].([]any)[1].(map[string]any)
	eq(t, route["type"], "A")
	eq(t, route["name"], apex)
	eq(t, route["value"], apexIP+", 2001:db8::10")
	d.prove(apex, set)
	for name, tc := range map[string]struct {
		ips []string
		ok  bool
	}{"none": {nil, false}, "ours": {[]string{apexIP}, true}, "both": {[]string{apexIP, "2001:db8::10"}, true}, "one of ours and a stranger": {[]string{apexIP, "198.51.100.7"}, false}, "a stranger": {[]string{"198.51.100.7"}, false}} {
		d.dns.set(func() {
			delete(d.dns.host, apex)
			if tc.ips != nil {
				d.dns.host[apex] = tc.ips
			}
		})
		r := d.check(apex)
		if got := r["domain"].(map[string]any)["routing"]; got != tc.ok {
			t.Errorf("%s: routing=%v want %v", name, got, tc.ok)
		}
		eq(t, d.ask(apex), tc.ok)
	}
}

// 6. The daily re-check: two failed days lose the name; a lookup that failed for other reasons changes nothing.
func TestSiteDomainSweep(t *testing.T) {
	d := newDomEnv(t)
	d.pro(true)
	d.publishOK(d.sub)
	host := domHost()
	set := d.setOK(host)
	d.prove(host, set)
	d.routeWWW(host)
	eq(t, d.check()["verified"], true)
	ctx := context.Background()
	sweep := func() {
		t.Helper()
		d.agePastDue(host)
		if err := sites.DomainSweep(ctx, d.pool); err != nil {
			t.Fatal(err)
		}
	}
	// not due: a sweep right after a check does nothing, even with the record gone
	d.dns.set(func() { delete(d.dns.txt, "_cnote-verify."+host) })
	if err := sites.DomainSweep(ctx, d.pool); err != nil {
		t.Fatal(err)
	}
	eq(t, d.claim(host, "failures"), "0")
	// a lookup that times out is not a failure
	d.dns.set(func() { d.dns.fail["_cnote-verify."+host] = &net.DNSError{Err: "i/o timeout", IsTimeout: true} })
	sweep()
	eq(t, d.claim(host, "failures"), "0")
	eq(t, d.ask(host), true)
	// the record is gone: the first day is a warning, the second loses the name
	d.dns.set(func() { delete(d.dns.fail, "_cnote-verify."+host) })
	sweep()
	eq(t, d.claim(host, "failures"), "1")
	eq(t, d.claim(host, "status"), "verified")
	eq(t, d.ask(host), true)
	// the record comes back: the count resets
	d.prove(host, set)
	sweep()
	eq(t, d.claim(host, "failures"), "0")
	d.dns.set(func() { delete(d.dns.txt, "_cnote-verify."+host) })
	sweep()
	sweep()
	eq(t, d.claim(host, "status"), "lost")
	eq(t, d.ask(host), false)
	if st, _ := d.lookup(host); st != 404 {
		t.Fatalf("lost host still routed: %d", st)
	}
	// a lost claim is not swept again, and proving it again verifies it
	d.prove(host, set)
	d.agePastDue(host)
	eq(t, d.check()["verified"], true)
	eq(t, d.ask(host), true)

	// the routing record disappears: not routed from that sweep on, still the owner's
	d.dns.set(func() { delete(d.dns.cname, host) })
	sweep()
	eq(t, d.claim(host, "status"), "verified")
	eq(t, d.claim(host, "routing_configured"), "false")
	eq(t, d.ask(host), false)

	// expired pending claims are pruned
	h2 := domHost()
	d.setOK(h2)
	adminSQL(d.env, d.owner.ws, "update sites.domain_claims set expires_at = now() - interval '1 day' where hostname = $1", h2)
	if err := sites.DomainSweep(ctx, d.pool); err != nil {
		t.Fatal(err)
	}
	eq(t, d.sql("select count(*) from sites.domain_claims where hostname = $1", h2), "0")
	t.Setenv("SITES_CUSTOM_DOMAINS", "")
	if err := sites.DomainSweep(ctx, d.pool); err != nil {
		t.Fatal(err)
	}
}

// 7. The ask endpoint is a plain HTTP listener: 200 for a live name, 404 for anything else, no other route.
func TestSiteDomainAskListener(t *testing.T) {
	d := newDomEnv(t)
	d.pro(true)
	d.publishOK(d.sub)
	host := domHost()
	set := d.setOK(host)
	d.prove(host, set)
	d.routeWWW(host)
	d.check()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server.StartSitesAsk(ctx, d.pool, addr)
	get := func(path string) int {
		for i := 0; i < 50; i++ {
			res, err := http.Get("http://" + addr + path)
			if err == nil {
				_ = res.Body.Close()
				return res.StatusCode
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("listener %s did not start", addr)
		return 0
	}
	eq(t, get("/internal/tls-ask?domain="+host), 200)
	eq(t, get("/internal/tls-ask?domain=nope-"+host), 404)
	eq(t, get("/internal/tls-ask?domain="), 404)
	eq(t, get("/internal/tls-ask"), 404)
	eq(t, get("/api/sites/public_get"), 404)
	adminOK(t, d.call(d.owner, "site.unpublish", map[string]any{"id": d.site}))
	eq(t, get("/internal/tls-ask?domain="+host), 404)
	// the public API server never serves it
	if st, _, _ := d.hit("GET", "/internal/tls-ask?domain="+host, nil, nil); st == 200 {
		t.Fatalf("tls-ask answered on the public API: %d", st)
	}
}
