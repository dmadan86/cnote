package netguard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestRejectsInternalAndInsecureEndpoints(t *testing.T) {
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "")
	for _, bad := range []string{
		"http://api.example.com", "https://127.0.0.1/v1", "https://169.254.169.254/latest", "https://10.0.0.5",
		"https://[::1]/v1", "https://user:pw@api.openai.com", "https://localhost:8080",
	} {
		if err := CheckEndpoint(context.Background(), bad); err == nil {
			t.Errorf("%s should be rejected", bad)
		}
	}
	if err := CheckEndpoint(context.Background(), "https://8.8.8.8/x"); err != nil {
		t.Errorf("public literal rejected: %v", err)
	}
}

func TestPrivateAllowedKeepsCredentialAndSchemeChecks(t *testing.T) {
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "true")
	ctx := context.Background()
	if CheckEndpoint(ctx, "http://127.0.0.1:1/x") != nil {
		t.Error("private http should pass")
	}
	if CheckEndpoint(ctx, "ftp://127.0.0.1/x") == nil || CheckEndpoint(ctx, "http://u:p@127.0.0.1/") == nil {
		t.Error("scheme and credentials must still be refused")
	}
}

func TestClassifiesAddresses(t *testing.T) {
	for ip, want := range map[string]bool{
		"8.8.8.8": true, "192.168.1.1": false, "100.64.0.1": false, "100.128.0.1": true, "::ffff:10.0.0.1": false,
		"fd00::1": false, "fe80::1": false, "::1": false, "::": false, "2606:4700::1111": true, "0.1.2.3": false,
		"198.18.0.1": false, "198.20.0.1": true, "255.255.255.255": false, "203.0.113.9": false, "172.16.0.1": false,
		"172.32.0.1": true, "169.254.1.1": false, "127.0.0.1": false, "::ffff:8.8.8.8": true,
	} {
		if got := IsPublic(netip.MustParseAddr(ip)); got != want {
			t.Errorf("IsPublic(%s)=%v want %v", ip, got, want)
		}
	}
}

func TestClientRefusesLoopbackDialAndRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "")
	if res, err := Client().Get(srv.URL); err == nil {
		res.Body.Close()
		t.Fatal("dial to loopback must be refused when private endpoints are not allowed")
	}
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "1")
	res, err := Client().Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("redirect was followed: %d", res.StatusCode)
	}
}
