package sites

import (
	"context"
	"net"
	"testing"
)

type stubDNS struct {
	txt, host map[string][]string
	cname     map[string]string
	err       error
}

func (s stubDNS) LookupTXT(_ context.Context, n string) ([]string, error) { return s.txt[n], s.err }
func (s stubDNS) LookupCNAME(_ context.Context, n string) (string, error) {
	return s.cname[n], s.err
}
func (s stubDNS) LookupHost(_ context.Context, n string) ([]string, error) { return s.host[n], s.err }

func TestLookupClaim(t *testing.T) {
	t.Setenv("SITES_DOMAIN", "sites.example.test")
	t.Setenv("SITES_APEX_IP", "203.0.113.10, 2001:db8::10")
	t.Setenv("PUBLIC_BASE_URL", "")
	t.Setenv("SITES_TEST_DNS", "")
	www := claim{Hostname: "www.shop.test", Token: "abc"}
	apex := claim{Hostname: "shop.test", Token: "abc"}
	good := "cnote-verify=abc"
	notFound := &net.DNSError{IsNotFound: true}
	for _, c := range []struct {
		name              string
		cl                claim
		dns               stubDNS
		txtOK, routeOK    bool
		txtKnown, rtKnown bool
	}{
		{"www proven and routed", www, stubDNS{txt: map[string][]string{"_cnote-verify.www.shop.test": {"x", " " + good + " "}}, cname: map[string]string{"www.shop.test": "Acme.sites.example.test."}}, true, true, true, true},
		{"txt of another claim", www, stubDNS{txt: map[string][]string{"_cnote-verify.www.shop.test": {"cnote-verify=zzz"}}}, false, false, true, true},
		{"cname to another site", www, stubDNS{txt: map[string][]string{"_cnote-verify.www.shop.test": {good}}, cname: map[string]string{"www.shop.test": "other.sites.example.test"}}, true, false, true, true},
		{"no cname, only an address", www, stubDNS{cname: map[string]string{"www.shop.test": "www.shop.test"}}, false, false, true, true},
		{"apex on our addresses", apex, stubDNS{txt: map[string][]string{"_cnote-verify.shop.test": {good}}, host: map[string][]string{"shop.test": {"203.0.113.10", "2001:db8::10"}}}, true, true, true, true},
		{"apex with a stranger", apex, stubDNS{host: map[string][]string{"shop.test": {"203.0.113.10", "203.0.113.11"}}}, false, false, true, true},
		{"apex v4 in v6 form", apex, stubDNS{host: map[string][]string{"shop.test": {"::ffff:203.0.113.10"}}}, false, true, true, true},
		{"timeout concludes nothing", www, stubDNS{err: &net.DNSError{IsTimeout: true}}, false, false, false, false},
		{"nxdomain concludes absence", www, stubDNS{err: notFound}, false, false, true, true},
	} {
		got := lookup(context.Background(), c.dns, c.cl, "acme")
		if got.txtOK != c.txtOK || got.routeOK != c.routeOK || got.txtKnown != c.txtKnown || got.routeKnown != c.rtKnown {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

func TestApexAndTestDNS(t *testing.T) {
	for h, want := range map[string]bool{"shop.test": true, "www.shop.test": false, "shop.co.uk": true, "www.shop.co.uk": false} {
		if isApex(h) != want {
			t.Errorf("isApex(%s) = %v", h, !want)
		}
	}
	t.Setenv("SITES_TEST_DNS", "cnote-e2e.test")
	t.Setenv("PUBLIC_BASE_URL", "http://localhost:3000")
	if !testDNS("www.cnote-e2e.test") || !testDNS("cnote-e2e.test") || testDNS("xcnote-e2e.test") || testDNS("www.shop.test") {
		t.Fatal("SITES_TEST_DNS matches the suffix only")
	}
	t.Setenv("PUBLIC_BASE_URL", "https://app.cnote.test")
	if testDNS("www.cnote-e2e.test") {
		t.Fatal("SITES_TEST_DNS must be ignored on https")
	}
}

func TestCleanHost(t *testing.T) {
	for in, want := range map[string]string{" https://Www.Shop.test/a?b#c ": "Www.Shop.test", "shop.test": "shop.test", "http://shop.test?x": "shop.test"} {
		if got := cleanHost(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
