package sites

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type cfFixture struct {
	Installation string   `json:"installation_id"`
	Get          []cfCase `json:"get_responses"`
	List         []cfCase `json:"list_responses"`
	Create       []cfCase `json:"create_responses"`
	Delete       []cfCase `json:"delete_responses"`
	Errors       []cfCase `json:"error_responses"`
}

type cfCase struct {
	ID         string          `json:"id"`
	HTTPStatus int             `json:"http_status"`
	RetryAfter string          `json:"retry_after"`
	Body       json.RawMessage `json:"body"`
	Routing    bool            `json:"expected_routing"`
	Verdict    string          `json:"expected_verdict"`
	ExpectedID string          `json:"expected_id"`
}

func loadCF(t *testing.T) cfFixture {
	t.Helper()
	b, err := os.ReadFile("testdata/cloudflare.json")
	if err != nil {
		t.Fatal(err)
	}
	var f cfFixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func (c cfCase) serve(w http.ResponseWriter) {
	if c.RetryAfter != "" {
		w.Header().Set("Retry-After", c.RetryAfter)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(c.HTTPStatus)
	_, _ = w.Write(c.Body)
}

// cfServer answers every request with the next case and records what it was asked.
type cfServer struct {
	mu     sync.Mutex
	cases  []cfCase
	reqs   []*http.Request
	bodies []string
	*httptest.Server
}

func newCFServer(t *testing.T, cases ...cfCase) *cfServer {
	s := &cfServer{cases: cases}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		b, _ := io.ReadAll(r.Body)
		s.reqs, s.bodies = append(s.reqs, r), append(s.bodies, string(b))
		s.cases[min(len(s.reqs), len(s.cases))-1].serve(w) // the last case repeats
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *cfServer) client(slept *[]time.Duration) *CloudflareClient {
	return &CloudflareClient{BaseURL: s.URL, Token: "secret-token-value", ZoneID: "zone1", HTTP: s.Client(),
		Sleep: func(d time.Duration) {
			if slept != nil {
				*slept = append(*slept, d)
			}
		}}
}

func pick(t *testing.T, cs []cfCase, id string) cfCase {
	t.Helper()
	for _, c := range cs {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no fixture %q", id)
	return cfCase{}
}

func TestCFJudgeFixtures(t *testing.T) {
	f := loadCF(t)
	want := map[string]int{"ok": cfOK, "pending": cfPending, "failure": cfFailure, "lost": cfLost}
	seen := map[string]bool{}
	for _, c := range f.Get {
		if c.HTTPStatus != 200 {
			continue
		}
		srv := newCFServer(t, c)
		h, err := srv.client(nil).Get(context.Background(), "x")
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		routing, verdict := cfJudge(h.Status, h.SSL.Status)
		if routing != c.Routing || verdict != want[c.Verdict] {
			t.Errorf("%s (%s/%s): routing %v verdict %d, want %v %s", c.ID, h.Status, h.SSL.Status, routing, verdict, c.Routing, c.Verdict)
		}
		seen[c.Verdict] = true
		if !h.ours(f.Installation) || h.ours("other") || h.ours("") {
			t.Errorf("%s: ownership by custom metadata", c.ID)
		}
	}
	if len(seen) != 4 {
		t.Fatalf("fixtures cover %v", seen)
	}
	if routing, v := cfJudge("active", "active"); !routing || v != cfOK { // the one routed state
		t.Fatal("active/active")
	}
	if routing, _ := cfJudge("active", ""); routing {
		t.Fatal("a hostname without a certificate status is not routed")
	}
}

func TestCFClientRequests(t *testing.T) {
	f := loadCF(t)
	ctx := context.Background()

	// create: auth header, body, and the answer
	srv := newCFServer(t, pick(t, f.Create, "created"))
	h, err := srv.client(nil).Create(ctx, "shop.example.com", "inst-test")
	if err != nil || h.ID != "cfid910" {
		t.Fatalf("create: %+v %v", h, err)
	}
	r := srv.reqs[0]
	if r.Method != "POST" || r.URL.Path != "/zones/zone1/custom_hostnames" || r.Header.Get("Authorization") != "Bearer secret-token-value" {
		t.Fatalf("create request: %s %s %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
	}
	var sent struct {
		Hostname string
		SSL      struct{ Method, Type string }
		Meta     map[string]string `json:"custom_metadata"`
	}
	if err := json.Unmarshal([]byte(srv.bodies[0]), &sent); err != nil || sent.Hostname != "shop.example.com" || sent.SSL.Method != "http" || sent.SSL.Type != "dv" || sent.Meta[cfMetaKey] != "inst-test" {
		t.Fatalf("create body: %s", srv.bodies[0])
	}

	// find: follows the pages, matches the name exactly, filters by hostname
	srv = newCFServer(t, pick(t, f.List, "page-1"), pick(t, f.List, "page-2"))
	h, err = srv.client(nil).Find(ctx, "Shop.Example.com")
	if err != nil || h == nil || h.ID != "cfid903" || len(srv.reqs) != 2 {
		t.Fatalf("find: %+v %v after %d requests", h, err, len(srv.reqs))
	}
	if q := srv.reqs[1].URL.Query(); q.Get("hostname") != "Shop.Example.com" || q.Get("page") != "2" {
		t.Fatalf("find query: %v", q)
	}
	srv = newCFServer(t, pick(t, f.List, "empty"))
	if h, err = srv.client(nil).Find(ctx, "shop.example.com"); h != nil || err != nil {
		t.Fatalf("find none: %+v %v", h, err)
	}
	srv = newCFServer(t, pick(t, f.List, "foreign"))
	if h, _ = srv.client(nil).Find(ctx, "shop.example.com"); h == nil || h.ours("inst-test") {
		t.Fatalf("a hostname without our id is not ours: %+v", h)
	}
	srv = newCFServer(t, pick(t, f.List, "count"))
	if n, err := srv.client(nil).Count(ctx); err != nil || n != 86 {
		t.Fatalf("count: %d %v", n, err)
	}

	// delete: a missing hostname is deleted
	srv = newCFServer(t, pick(t, f.Delete, "already-gone"))
	if err := srv.client(nil).Delete(ctx, "cfid910"); err != nil || srv.reqs[0].Method != "DELETE" {
		t.Fatalf("delete gone: %v", err)
	}
	srv = newCFServer(t, pick(t, f.Delete, "deleted"))
	if err := srv.client(nil).Delete(ctx, "cfid910"); err != nil {
		t.Fatal(err)
	}

	// 5xx: three tries with backoff, then the error
	var slept []time.Duration
	srv = newCFServer(t, pick(t, f.Errors, "server"))
	_, err = srv.client(&slept).Get(ctx, "x")
	var ce *CFError
	if !errors.As(err, &ce) || ce.HTTP != 500 || len(srv.reqs) != 3 || len(slept) != 2 || slept[0] != time.Second || slept[1] != 2*time.Second {
		t.Fatalf("5xx: %v after %d requests, slept %v", err, len(srv.reqs), slept)
	}
	// a success after a failure
	srv = newCFServer(t, pick(t, f.Errors, "server"), pick(t, f.Get, "active-active"))
	if h, err := srv.client(&slept).Get(ctx, "x"); err != nil || h.Status != "active" {
		t.Fatalf("retry then ok: %v", err)
	}
	// 429: no retry, the wait comes back, and it stops a sweep
	srv = newCFServer(t, pick(t, f.Errors, "rate-limited"))
	_, err = srv.client(&slept).Get(ctx, "x")
	if !errors.As(err, &ce) || ce.RetryAfter != 5*time.Minute || len(srv.reqs) != 1 || !ce.fatal() {
		t.Fatalf("429: %v after %d requests", err, len(srv.reqs))
	}
	// 401 stops a sweep too; 404 does not; neither error names the token
	for id, fatal := range map[string]bool{"auth": true, "zone": true} {
		srv = newCFServer(t, pick(t, f.Errors, id))
		_, err = srv.client(nil).Get(ctx, "x")
		if !errors.As(err, &ce) || ce.fatal() != fatal || len(srv.reqs) != 1 || strings.Contains(err.Error(), "secret-token-value") {
			t.Fatalf("%s: %v", id, err)
		}
	}
	srv = newCFServer(t, pick(t, f.Get, "not-found"))
	if _, err = srv.client(nil).Get(ctx, "x"); !isNotFound(err) {
		t.Fatalf("404: %v", err)
	}
	// an error from the transport does not carry the token or the zone either
	srv = newCFServer(t)
	c := srv.client(&slept)
	srv.Close()
	if _, err = c.Get(ctx, "x"); err == nil || strings.Contains(err.Error(), "secret-token-value") || strings.Contains(err.Error(), "zone1") {
		t.Fatalf("transport error: %v", err)
	}
	if s := fmt.Sprintf("%v %+v %#v", c, c, c); strings.Contains(s, "secret-token-value") {
		t.Fatalf("printing the client leaks the token: %s", s)
	}
}

func TestCFEnv(t *testing.T) {
	t.Setenv("SITES_DOMAIN", "sites.example.test")
	t.Setenv("SITES_DOMAIN_PROVIDER", "")
	t.Setenv("SITES_CF_CNAME_TARGET", "customers.example.test")
	if CloudflareOn() || routeTarget("shop") != "shop.sites.example.test" { // off: nothing changes
		t.Fatal("provider must be off by default")
	}
	t.Setenv("SITES_DOMAIN_PROVIDER", " Cloudflare ")
	if !CloudflareOn() || routeTarget("shop") != "customers.example.test" {
		t.Fatal("provider on")
	}
	t.Setenv("SITES_CF_CNAME_TARGET", "")
	if routeTarget("shop") != "shop.sites.example.test" {
		t.Fatal("default target")
	}
	for in, want := range map[string]int{"": 90, "x": 90, "0": 90, "-3": 90, "12": 12} {
		t.Setenv("SITES_CF_MAX_HOSTNAMES", in)
		if cfMax() != want {
			t.Errorf("max %q: %d", in, cfMax())
		}
	}
	// without a token, a zone and an installation id the provider is not ready
	t.Setenv("SITES_CF_API_TOKEN", "")
	t.Setenv("SITES_CF_ZONE_ID", "")
	t.Setenv("SITES_CF_INSTALLATION_ID", "")
	if cfReady() {
		t.Fatal("ready without config")
	}
	t.Setenv("SITES_CF_API_TOKEN", "t")
	t.Setenv("SITES_CF_ZONE_ID", "z")
	if cfReady() {
		t.Fatal("ready without installation id")
	}
	t.Setenv("SITES_CF_INSTALLATION_ID", "i")
	if !cfReady() {
		t.Fatal("not ready with config")
	}
}
