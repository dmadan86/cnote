package sites

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCanonicalURLASCIIHost(t *testing.T) {
	for _, tt := range []struct{ raw, want string }{
		{"https://BÜCHER.example/Pfad?q=1#f", "https://xn--bcher-kva.example/Pfad"},
		{"https://xn--bcher-kva.example", "https://xn--bcher-kva.example/"},
		{"http://example.com:80/a", "http://example.com/a"},
		{"https://example.com:8443", "https://example.com:8443/"},
		{"http://[2001:db8::1]/x", "http://[2001:db8::1]/x"},
	} {
		got, err := CanonicalURL(tt.raw)
		if err != nil || got != tt.want {
			t.Errorf("CanonicalURL(%q) = %q, %v want %q", tt.raw, got, err, tt.want)
		}
	}
	for _, bad := range []string{"ftp://example.com", "https://user@example.com", "javascript:alert(1)", "/relative"} {
		if _, err := CanonicalURL(bad); err == nil {
			t.Errorf("CanonicalURL(%q) accepted", bad)
		}
	}
}

func TestPickLinksOrder(t *testing.T) {
	now := time.Now()
	links := []string{"https://b.example/", "https://a.example/", "https://old.example/", "https://older.example/", "https://fresh.example/"}
	expires := map[string]time.Time{
		"https://old.example/": now.Add(-time.Hour), "https://older.example/": now.Add(-time.Hour), "https://fresh.example/": now.Add(time.Hour),
	}
	checked := map[string]time.Time{"https://old.example/": now.Add(-2 * time.Hour), "https://older.example/": now.Add(-30 * time.Hour)}
	due, rest := wrPickLinks(links, expires, checked, now)
	want := []string{"https://a.example/", "https://b.example/", "https://older.example/", "https://old.example/"}
	if strings.Join(due, " ") != strings.Join(want, " ") || len(rest) != 0 {
		t.Fatalf("new links first then oldest checked; a link with a valid answer is skipped: %v / %v", due, rest)
	}
	var many []string
	for i := 0; i < 250; i++ {
		many = append(many, "https://x"+strings.Repeat("a", i%7)+string(rune('a'+i/26))+string(rune('a'+i%26))+".example/")
	}
	due, rest = wrPickLinks(many, map[string]time.Time{}, nil, now)
	if len(due)+len(rest) != len(dedupe(many)) || len(due) != 200 {
		t.Fatalf("200 per scan, the rest listed: %d + %d", len(due), len(rest))
	}
}

func dedupe(l []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range l {
		m[x] = true
	}
	return m
}

func TestWebRiskLookupKeyInHeaderOnly(t *testing.T) {
	var gotKey, gotQuery string
	exp := time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339Nano)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotQuery = r.Header.Get("X-Goog-Api-Key"), r.URL.RawQuery
		if r.URL.Query().Get("uri") == "https://bad.example/x" {
			_, _ = w.Write([]byte(`{"threat":{"threatTypes":["SOCIAL_ENGINEERING"],"expireTime":"` + exp + `"}}`))
			return
		}
		if r.URL.Query().Get("uri") == "https://down.example/" {
			http.Error(w, "secret-key-echo", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	old, oldURL := WebRiskHTTP, WebRiskURL
	WebRiskHTTP, WebRiskURL = srv.Client(), srv.URL
	defer func() { WebRiskHTTP, WebRiskURL = old, oldURL }()
	t.Setenv("WEBRISK_API_KEY", "k-123456")

	r, err := wrLookup(context.Background(), "https://bad.example/x")
	if err != nil || r.Threat != "social_engineering" || time.Until(r.Expires) > 31*time.Minute || time.Until(r.Expires) < 29*time.Minute {
		t.Fatalf("threat with the provider's cache duration: %+v %v", r, err)
	}
	if gotKey != "k-123456" || strings.Contains(gotQuery, "k-123456") {
		t.Fatalf("the key goes in a header, never the query: %q %q", gotKey, gotQuery)
	}
	if c, err := wrLookup(context.Background(), "https://fine.example/"); err != nil || c.Threat != "" || time.Until(c.Expires) < 23*time.Hour {
		t.Fatalf("clean: %+v %v", c, err)
	}
	if _, err := wrLookup(context.Background(), "https://down.example/"); err == nil || strings.Contains(err.Error(), "secret-key-echo") || strings.Contains(err.Error(), "k-123456") {
		t.Fatalf("an error says what failed, never the key or the body: %v", err)
	}
}
