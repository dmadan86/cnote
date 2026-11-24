package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/modules/sites"
)

// fakeCF is a Cloudflare zone in memory: the custom hostnames API as far as the provider uses it.
type fakeCF struct {
	mu                          sync.Mutex
	hosts                       map[string]*fakeHost // by id
	next                        int
	calls, creates, deletes     int
	failDelete, rateLimit       bool
	badAuth                     int
	srv                         *httptest.Server
	lastCreate, lastCreateToken string
	createDelay                 time.Duration // a create takes this long (before it takes the lock, so other calls overlap it)
	onCreate                    func()        // runs while the create is in flight
}

type fakeHost struct {
	ID, Hostname, Status, SSL string
	Meta                      map[string]any
}

func (f *fakeCF) json(h *fakeHost) map[string]any {
	return map[string]any{"id": h.ID, "hostname": h.Hostname, "status": h.Status, "ssl": map[string]any{"status": h.SSL, "method": "http", "type": "dv"},
		"custom_metadata": h.Meta, "verification_errors": []string{}, "created_at": "2026-11-24T09:00:00Z"}
}

func reply(w http.ResponseWriter, code int, result any, info map[string]any) {
	body := map[string]any{"result": result, "success": code/100 == 2, "errors": []any{}, "messages": []any{}}
	if code/100 != 2 {
		body["errors"] = []any{map[string]any{"code": 1000 + code, "message": "fake error " + strconv.Itoa(code)}}
	}
	if info != nil {
		body["result_info"] = info
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func newFakeCF(t *testing.T) *fakeCF {
	f := &fakeCF{hosts: map[string]*fakeHost{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && f.createDelay > 0 {
			time.Sleep(f.createDelay)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls++
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			f.badAuth++
			reply(w, 401, nil, nil)
			return
		}
		if f.rateLimit {
			w.Header().Set("Retry-After", "300")
			reply(w, 429, nil, nil)
			return
		}
		base := "/zones/zone-1/custom_hostnames"
		if r.URL.Path != base && !strings.HasPrefix(r.URL.Path, base+"/") {
			reply(w, 404, nil, nil)
			return
		}
		id := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, base), "/")
		switch {
		case r.Method == "POST" && id == "":
			var in struct {
				Hostname string
				Meta     map[string]any `json:"custom_metadata"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			if f.onCreate != nil {
				f.onCreate()
			}
			for _, h := range f.hosts {
				if h.Hostname == in.Hostname {
					reply(w, 409, nil, nil)
					return
				}
			}
			f.next++
			h := &fakeHost{ID: fmt.Sprintf("cf%04d", f.next), Hostname: in.Hostname, Status: "pending", SSL: "initializing", Meta: in.Meta}
			f.hosts[h.ID] = h
			f.creates++
			f.lastCreate = in.Hostname
			reply(w, 200, f.json(h), nil)
		case r.Method == "GET" && id == "":
			var out []map[string]any
			for _, h := range f.hosts {
				if q := r.URL.Query().Get("hostname"); q == "" || q == h.Hostname {
					out = append(out, f.json(h))
				}
			}
			if out == nil {
				out = []map[string]any{}
			}
			reply(w, 200, out, map[string]any{"page": 1, "per_page": 50, "total_pages": 1, "count": len(out), "total_count": len(out)})
		case r.Method == "GET":
			if h := f.hosts[id]; h != nil {
				reply(w, 200, f.json(h), nil)
			} else {
				reply(w, 404, nil, nil)
			}
		case r.Method == "DELETE":
			if f.failDelete {
				reply(w, 500, nil, nil)
				return
			}
			if f.hosts[id] == nil {
				reply(w, 404, nil, nil)
				return
			}
			delete(f.hosts, id)
			f.deletes++
			reply(w, 200, map[string]any{"id": id}, nil)
		default:
			reply(w, 405, nil, nil)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeCF) byName(host string) *fakeHost {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.hosts {
		if h.Hostname == host {
			return h
		}
	}
	return nil
}

func (f *fakeCF) set(host, status, ssl string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.hosts {
		if h.Hostname == host {
			h.Status, h.SSL = status, ssl
			return
		}
	}
	panic("no host " + host)
}

func (f *fakeCF) stat() (calls, creates, deletes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.creates, f.deletes
}

type cfEnv struct {
	*domEnv
	cf *fakeCF
}

// newCFEnv turns the provider on against a fake zone and clears what earlier tests left in the shared database: the sweep plans
// every workspace, so their verified claims and reconciliation rows would otherwise reach this zone.
func newCFEnv(t *testing.T) *cfEnv {
	d := newDomEnv(t)
	f := newFakeCF(t)
	t.Setenv("SITES_DOMAIN_PROVIDER", "cloudflare")
	t.Setenv("SITES_CF_API_TOKEN", "tok-1")
	t.Setenv("SITES_CF_ZONE_ID", "zone-1")
	t.Setenv("SITES_CF_INSTALLATION_ID", "inst-1")
	t.Setenv("SITES_CF_CNAME_TARGET", "customers.example.test")
	t.Setenv("SITES_CF_MAX_HOSTNAMES", "")
	t.Setenv("SITES_CF_PAUSED", "")
	t.Cleanup(sites.SetCloudflareClient(&sites.CloudflareClient{BaseURL: f.srv.URL, Token: "tok-1", ZoneID: "zone-1", HTTP: f.srv.Client(), Sleep: func(time.Duration) {}}))
	admin, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_ADMIN_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(context.Background(), `delete from sites.cloudflare_reconciliation;
		update sites.domain_claims set status = 'lost', routing_configured = false where status <> 'lost';
		delete from core.rate_buckets where key in ('sitecf:alert', 'sitedomain:verified')`); err != nil {
		t.Fatal(err)
	}
	e := &cfEnv{d, f}
	e.pro(true)
	e.publishOK(e.sub)
	return e
}

func (e *cfEnv) sweep() {
	e.t.Helper()
	if err := sites.CloudflareSweep(context.Background(), e.pool); err != nil {
		e.t.Fatalf("sweep: %v", err)
	}
}

// due makes the row's turn come now.
func (e *cfEnv) due(host string) {
	adminSQL(e.env, e.owner.ws, "update sites.cloudflare_reconciliation set next_attempt_at = now() - interval '1 second' where hostname = $1", host)
}

func (e *cfEnv) rows(host string) string {
	return e.sql("select count(*)::text from sites.cloudflare_reconciliation where hostname = $1", host)
}

func (e *cfEnv) row(host, col string) string {
	return e.sql("select coalesce("+col+"::text, '-') from sites.cloudflare_reconciliation where hostname = $1", host)
}

// connect takes a new hostname as far as "proven": claimed, TXT in place, checked.
func (e *cfEnv) connect() string {
	host := domHost()
	set := e.setOK(host)
	e.prove(host, set)
	eq(e.t, e.check(host)["verified"], true)
	return host
}

// live takes it all the way: created at the provider, active there, routed here.
func (e *cfEnv) live() string {
	host := e.connect()
	e.sweep()
	e.cf.set(host, "active", "active")
	e.due(host)
	e.sweep()
	eq(e.t, e.claim(host, "routing_configured"), "true")
	return host
}

// 1. Off unless SITES_DOMAIN_PROVIDER=cloudflare: no sweep work, no calls, DNS decides routing as before.
func TestSiteCloudflareOffByDefault(t *testing.T) {
	e := newCFEnv(t)
	t.Setenv("SITES_DOMAIN_PROVIDER", "")
	host := e.connect()
	e.routeWWW(host)
	eq(t, e.check(host)["domain"].(map[string]any)["routing"], true) // the DNS check, not the provider
	e.sweep()
	if calls, _, _ := e.cf.stat(); calls != 0 || e.rows(host) != "0" {
		t.Fatalf("off, yet %d calls and %s rows", calls, e.rows(host))
	}
	t.Setenv("SITES_DOMAIN_PROVIDER", "cloudflare")
	t.Setenv("SITES_CUSTOM_DOMAINS", "")
	e.sweep() // custom domains off: nothing either
	if calls, _, _ := e.cf.stat(); calls != 0 {
		t.Fatalf("custom domains off, yet %d calls", calls)
	}
}

// 2. The whole life: claim, proof, create, wait, active, strikes, lost, delete.
func TestSiteCloudflareLifecycle(t *testing.T) {
	e := newCFEnv(t)
	if r := e.set("example" + uniq() + ".test"); r.status != 400 { // apex refused in this mode, even with SITES_APEX_IP set
		t.Fatalf("apex: %d %s", r.status, r.body)
	}
	host := domHost()
	set := e.setOK(host)
	route := set["records"].([]any)[1].(map[string]any)
	eq(t, route["value"], "customers.example.test") // the proxied CNAME target of the SaaS zone
	e.prove(host, set)
	e.routeWWW(host) // DNS says routed; the provider has the say
	r := e.check(host)
	eq(t, r["verified"], true)
	eq(t, r["domain"].(map[string]any)["routing"], false)
	eq(t, e.ask(host), false)
	if e.rows(host) != "0" {
		t.Fatal("rows are planned by the sweep")
	}

	e.sweep()
	eq(t, e.rows(host), "1")
	if _, creates, _ := e.cf.stat(); creates != 1 {
		t.Fatalf("creates: %d", creates)
	}
	h := e.cf.byName(host)
	if h == nil || h.Meta["cnote_installation"] != "inst-1" {
		t.Fatalf("created without the installation id: %+v", h)
	}
	eq(t, e.row(host, "provider_id"), h.ID)
	eq(t, e.row(host, "status"), "pending")
	eq(t, e.claim(host, "routing_configured"), "false")
	eq(t, e.check(host)["domain"].(map[string]any)["routing"], false) // still pending at the provider
	eq(t, e.ask(host), false)
	e.sweep() // nothing due: no more calls
	if _, creates, _ := e.cf.stat(); creates != 1 {
		t.Fatal("created twice")
	}

	// the certificate is not ready yet: a pending state is not a failure
	e.cf.set(host, "pending", "pending_validation")
	e.due(host)
	e.sweep()
	eq(t, e.claim(host, "failures"), "0")
	eq(t, e.claim(host, "routing_configured"), "false")

	// active and certified: routed
	e.cf.set(host, "active", "active")
	e.due(host)
	e.sweep()
	eq(t, e.claim(host, "routing_configured"), "true")
	eq(t, e.ask(host), true)
	if st, pub := e.lookup(host); st != 200 || pub["site_id"] != e.site {
		t.Fatalf("lookup: %d", st)
	}
	eq(t, e.check(host)["domain"].(map[string]any)["routing"], true)

	// an expired certificate: one strike, not routed; the second strike loses the claim
	e.cf.set(host, "active", "expired")
	e.due(host)
	e.sweep()
	eq(t, e.claim(host, "failures"), "1")
	eq(t, e.claim(host, "routing_configured"), "false")
	eq(t, e.claim(host, "status"), "verified")
	eq(t, e.ask(host), false)
	e.cf.set(host, "active", "active") // it recovers: the strikes reset
	e.due(host)
	e.sweep()
	eq(t, e.claim(host, "failures"), "0")
	eq(t, e.claim(host, "routing_configured"), "true")
	e.cf.set(host, "moved", "active")
	e.due(host)
	e.sweep()
	e.due(host)
	e.sweep()
	eq(t, e.claim(host, "status"), "lost")
	eq(t, e.ask(host), false)

	// a lost claim is no longer wanted: the next sweep deletes the hostname at the provider and drops the row
	e.sweep()
	if _, _, deletes := e.cf.stat(); deletes != 1 || e.cf.byName(host) != nil || e.rows(host) != "0" {
		t.Fatalf("deletes %d rows %s", deletes, e.rows(host))
	}
}

// 3. Blocked by Cloudflare: lost at once.
func TestSiteCloudflareBlocked(t *testing.T) {
	e := newCFEnv(t)
	host := e.live()
	e.cf.set(host, "blocked", "active")
	e.due(host)
	e.sweep()
	eq(t, e.claim(host, "status"), "lost")
	eq(t, e.ask(host), false)
}

// 4. Create is idempotent, and a hostname that is not ours is never touched.
func TestSiteCloudflareIdempotentAndForeign(t *testing.T) {
	e := newCFEnv(t)
	host := e.connect()
	e.sweep()
	if _, creates, _ := e.cf.stat(); creates != 1 {
		t.Fatal("create")
	}
	// the answer to the create was lost: no provider id recorded; the next run finds the hostname by name
	adminSQL(e.env, e.owner.ws, "update sites.cloudflare_reconciliation set provider_id = null, status = 'pending' where hostname = $1", host)
	e.due(host)
	e.sweep()
	if _, creates, _ := e.cf.stat(); creates != 1 {
		t.Fatalf("created again: %d", creates)
	}
	eq(t, e.row(host, "provider_id"), e.cf.byName(host).ID)

	// a hostname the zone already holds without our id is left alone, and so is its delete
	other := e.connect()
	e.cf.mu.Lock()
	e.cf.next++
	e.cf.hosts["foreign"] = &fakeHost{ID: "foreign", Hostname: other, Status: "active", SSL: "active"}
	e.cf.mu.Unlock()
	e.sweep()
	eq(t, e.row(other, "status"), "conflict")
	eq(t, e.row(other, "provider_id"), "-")
	eq(t, e.claim(other, "routing_configured"), "false")
	if _, creates, _ := e.cf.stat(); creates != 1 {
		t.Fatal("created over a foreign hostname")
	}
	// one with a recorded id whose metadata was stripped is not deleted either
	adminSQL(e.env, e.owner.ws, "update sites.cloudflare_reconciliation set provider_id = 'foreign', status = 'active' where hostname = $1", other)
	adminOK(t, e.call(e.owner, "site.unpublish", map[string]any{"id": e.site}))
	e.sweep()
	e.sweep()
	if _, _, deletes := e.cf.stat(); deletes != 1 { // only the one that carries our id
		t.Fatalf("deletes: %d", deletes)
	}
	if e.cf.byName(other) == nil || e.rows(other) != "0" {
		t.Fatal("the foreign hostname must stay, our row must go")
	}
}

// 5. Unpublish, takedown, Pro lapse and disconnect: routing stops at once, the provider hostname goes on the next sweep, and a
// failed delete stays a tombstone until it is confirmed.
func TestSiteCloudflareTeardown(t *testing.T) {
	e := newCFEnv(t)
	cases := []struct {
		name    string
		off, on func(host string)
	}{
		{"unpublish", func(string) { adminOK(t, e.call(e.owner, "site.unpublish", map[string]any{"id": e.site})) }, func(string) { e.publishOK("") }},
		{"takedown", func(string) {
			adminOK(t, e.platform(e.owner, map[string]any{"subdomain": e.sub, "action": "takedown", "reason": "phishing report"}))
		}, func(string) {
			adminOK(t, e.platform(e.owner, map[string]any{"subdomain": e.sub, "action": "restore", "reason": "cleared"}))
		}},
		{"pro lapse", func(string) { e.pro(false) }, func(string) { e.pro(true) }},
		{"disconnect", func(host string) { eq(t, e.remove(host).status, 200) }, nil},
	}
	for _, c := range cases {
		host := e.live()
		eq(t, e.ask(host), true)
		_, _, before := e.cf.stat()
		c.off(host)
		if e.ask(host) { // stops at once, before any sweep
			t.Fatalf("%s: still approved", c.name)
		}
		if st, _ := e.lookup(host); st != 404 {
			t.Fatalf("%s: still routed (%d)", c.name, st)
		}
		if e.cf.byName(host) == nil {
			t.Fatalf("%s: deleted before the sweep", c.name)
		}
		e.cf.mu.Lock()
		e.cf.failDelete = true
		e.cf.mu.Unlock()
		if err := sites.CloudflareSweep(context.Background(), e.pool); err == nil || strings.Contains(err.Error(), "tok-1") {
			t.Fatalf("%s: a failed delete must be reported without the token: %v", c.name, err)
		}
		eq(t, e.rows(host), "1") // tombstone
		eq(t, e.row(host, "desired_state"), "absent")
		eq(t, e.row(host, "tombstone"), "true")
		e.cf.mu.Lock()
		e.cf.failDelete = false
		e.cf.mu.Unlock()
		e.due(host)
		e.sweep()
		if _, _, after := e.cf.stat(); after != before+1 || e.cf.byName(host) != nil || e.rows(host) != "0" {
			t.Fatalf("%s: deletes %d->%d rows %s", c.name, before, after, e.rows(host))
		}
		if c.on != nil {
			c.on(host)
			// back on the air: the hostname is created again and routed once the provider says so
			e.sweep()
			eq(t, e.ask(host), false)
			e.cf.set(host, "active", "active")
			e.due(host)
			e.sweep()
			eq(t, e.ask(host), true)
			eq(t, e.remove(host).status, 200)
			e.sweep()
			e.sweep()
		}
	}
}

// 6. Budgets: five per workspace, the 80% alert, the stop switch.
func TestSiteCloudflareBudgets(t *testing.T) {
	e := newCFEnv(t)
	var alerts []string
	old := sites.CloudflareAlert
	sites.CloudflareAlert = func(_ context.Context, used, max int) { alerts = append(alerts, fmt.Sprintf("%d/%d", used, max)) }
	t.Cleanup(func() { sites.CloudflareAlert = old })

	t.Setenv("SITES_CF_MAX_HOSTNAMES", "5")
	for i := 0; i < 3; i++ {
		e.connect()
	}
	e.sweep()
	eq(t, len(alerts), 0) // 3 of 5
	e.connect()
	e.sweep()
	eq(t, alerts, []string{"4/5"}) // 80%
	e.sweep()
	eq(t, len(alerts), 1) // once a day

	e.connect() // the fifth claim of this workspace; the sixth is refused
	if r := e.set(domHost()); r.status != 402 {
		t.Fatalf("sixth hostname: %d %s", r.status, r.body)
	}

	// the stop switch: nothing is sent, nothing is accepted
	t.Setenv("SITES_CF_PAUSED", "1")
	calls, _, _ := e.cf.stat()
	e.sweep()
	if c2, _, _ := e.cf.stat(); c2 != calls {
		t.Fatal("paused, yet the provider was called")
	}
	if r := e.set(domHost()); r.status != 400 {
		t.Fatalf("paused: %d %s", r.status, r.body)
	}
}

// 6b. The platform total: no new claim past it, and the reconciler never creates past it, counting hostnames the zone holds
// that are not ours.
func TestSiteCloudflareTotalBudget(t *testing.T) {
	e := newCFEnv(t)
	t.Setenv("SITES_CF_MAX_HOSTNAMES", "1")
	first := e.connect()
	e.sweep()
	eq(t, e.row(first, "provider_id"), e.cf.byName(first).ID)
	if r := e.set(domHost()); r.status != 429 {
		t.Fatalf("past the budget: %d %s", r.status, r.body)
	}

	t.Setenv("SITES_CF_MAX_HOSTNAMES", "3")
	e.cf.mu.Lock()
	for _, n := range []string{"x1", "x2"} { // two more in the zone, not ours
		e.cf.hosts[n] = &fakeHost{ID: n, Hostname: n + ".example.test", Status: "active", SSL: "active"}
	}
	e.cf.mu.Unlock()
	second := e.connect()
	e.sweep()
	eq(t, e.row(second, "status"), "budget")
	eq(t, e.row(second, "provider_id"), "-")
	if _, creates, _ := e.cf.stat(); creates != 1 {
		t.Fatalf("created past the budget: %d", creates)
	}
	e.cf.mu.Lock()
	delete(e.cf.hosts, "x2")
	e.cf.mu.Unlock()
	adminSQL(e.env, e.owner.ws, "update sites.cloudflare_reconciliation set next_attempt_at = now() where hostname = $1", second)
	e.sweep()
	if e.cf.byName(second) == nil {
		t.Fatal("not created once there is room")
	}
}

// 7. A 429 or a bad token stops the sweep and backs the row off.
func TestSiteCloudflareRateLimitAndAuth(t *testing.T) {
	e := newCFEnv(t)
	host := e.live()
	e.cf.mu.Lock()
	e.cf.rateLimit = true
	e.cf.mu.Unlock()
	e.due(host)
	if err := sites.CloudflareSweep(context.Background(), e.pool); err == nil {
		t.Fatal("429 must be reported")
	}
	eq(t, e.sql("select (next_attempt_at > now() + interval '4 minutes')::text from sites.cloudflare_reconciliation where hostname = $1", host), "true")
	eq(t, e.claim(host, "routing_configured"), "true") // a provider that cannot answer proves nothing

	e.cf.mu.Lock()
	e.cf.rateLimit = false
	e.cf.mu.Unlock()
	t.Cleanup(sites.SetCloudflareClient(&sites.CloudflareClient{BaseURL: e.cf.srv.URL, Token: "wrong-token", ZoneID: "zone-1", HTTP: e.cf.srv.Client()}))
	e.due(host)
	err := sites.CloudflareSweep(context.Background(), e.pool)
	if err == nil || strings.Contains(err.Error(), "wrong-token") || strings.Contains(e.row(host, "last_error"), "wrong-token") {
		t.Fatalf("bad token: %v", err)
	}
	eq(t, e.claim(host, "routing_configured"), "true")
}

// 8. The app role cannot run the planner (it crosses workspaces and takes any limit); without the admin connection the sweep does nothing.
func TestSiteCloudflarePlannerNotForAppRole(t *testing.T) {
	e := newCFEnv(t)
	host := e.connect()
	if _, err := e.pool.Exec(context.Background(), "select sites.cf_plan(0)"); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("the app role ran cf_plan: %v", err)
	}
	eq(t, e.rows(host), "0")
	t.Setenv("DATABASE_ADMIN_URL", "")
	e.sweep()
	if calls, _, _ := e.cf.stat(); calls != 0 || e.rows(host) != "0" {
		t.Fatalf("no admin connection, yet %d calls and %s rows", calls, e.rows(host))
	}
}

// 9. Two workers, one slot left: exactly one create (the slot is reserved before the provider is called).
func TestSiteCloudflareLastSlotReserved(t *testing.T) {
	e := newCFEnv(t)
	t.Setenv("SITES_CF_MAX_HOSTNAMES", "1")
	e.cf.createDelay = 300 * time.Millisecond
	a, b := e.connect(), e.connect()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = sites.CloudflareSweep(context.Background(), e.pool) }()
	}
	wg.Wait()
	if _, creates, _ := e.cf.stat(); creates != 1 {
		t.Fatalf("one slot, %d creates", creates)
	}
	eq(t, e.sql("select count(*)::text from sites.cloudflare_reconciliation where reserved_at is not null"), "0") // released
	eq(t, e.sql("select sites.cf_used()::text"), "1")
	_ = a
	_ = b
}

// 10. A result for a hostname that stopped being wanted while the provider call was in flight is dropped.
func TestSiteCloudflareStaleResultDropped(t *testing.T) {
	e := newCFEnv(t)
	host := e.connect()
	e.cf.onCreate = func() {
		adminOK(t, e.call(e.owner, "site.unpublish", map[string]any{"id": e.site}))
		admin, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_ADMIN_URL"))
		if err == nil {
			defer admin.Close()
			_, err = admin.Exec(context.Background(), "select sites.cf_plan(5)")
		}
		if err != nil {
			t.Error(err)
		}
	}
	e.sweep()
	e.cf.onCreate = nil
	eq(t, e.row(host, "desired_state"), "absent")
	eq(t, e.row(host, "provider_id"), "-") // the stale "created" was not stored
	eq(t, e.claim(host, "routing_configured"), "false")
	eq(t, e.sql("select count(*)::text from sites.cloudflare_reconciliation where reserved_at is not null"), "0")
	e.due(host)
	e.sweep() // the tombstone finds and deletes what the in-flight create made
	if e.cf.byName(host) != nil || e.rows(host) != "0" {
		t.Fatalf("hostname left at the provider or row kept: %s", e.rows(host))
	}
}
