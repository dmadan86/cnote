package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/proxy"
	"github.com/cnote/api/internal/server"
)

func TestCollabPathRoutesToCollabURL(t *testing.T) {
	tag := func(name string) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, name+" "+r.URL.RequestURI()+" host="+r.Host)
		}))
		t.Cleanup(s.Close)
		return s
	}
	rust, collab := tag("rust"), tag("collab")
	up, _ := proxy.New(rust.URL)
	// a ws:// COLLAB_URL is accepted
	cp, err := proxy.New(strings.Replace(collab.URL, "http://", "ws://", 1))
	if err != nil {
		t.Fatal(err)
	}
	get := func(s *server.Server, path string) string {
		fs := httptest.NewServer(s)
		defer fs.Close()
		resp, err := http.Get(fs.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	off := server.New(nil, command.NewRegistry(), up)
	on := server.New(nil, command.NewRegistry(), up)
	on.Collab = cp

	if got := get(off, "/collab/w/n?epoch=2"); !strings.HasPrefix(got, "rust /collab/w/n?epoch=2") {
		t.Errorf("COLLAB_URL unset must keep Rust: %q", got)
	}
	if got := get(on, "/collab/w/n?epoch=2"); !strings.HasPrefix(got, "collab /collab/w/n?epoch=2") {
		t.Errorf("collab path must go to the collab service: %q", got)
	}
	if got := get(on, "/healthz"); got != "ok" {
		t.Errorf("healthz: %q", got)
	}
	if got := get(on, "/api/notes/ping"); !strings.HasPrefix(got, "rust /api/notes/ping") {
		t.Errorf("other paths stay on Rust: %q", got)
	}
}
