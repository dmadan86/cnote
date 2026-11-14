package proxy_test

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cnote/api/internal/proxy"
)

func front(t *testing.T, upstream http.Handler) *httptest.Server {
	t.Helper()
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	h, err := proxy.New(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	fs := httptest.NewServer(h)
	t.Cleanup(fs.Close)
	return fs
}

func TestForwardsRequestUnchanged(t *testing.T) {
	var got *http.Request
	fs := front(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.Header().Add("Set-Cookie", "a=1; Path=/")
		w.Header().Add("Set-Cookie", "b=2; Path=/")
		w.WriteHeader(418)
		_, _ = io.WriteString(w, "body")
	}))
	req, _ := http.NewRequest("PUT", fs.URL+"/api/x%2Fy//z?q=1&q=2", strings.NewReader("payload"))
	req.Header.Set("Cookie", "cn_session=tok")
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 418 || string(b) != "body" || len(resp.Header.Values("Set-Cookie")) != 2 {
		t.Fatalf("response not preserved: %d %q %v", resp.StatusCode, b, resp.Header)
	}
	if got.Method != "PUT" || got.URL.EscapedPath() != "/api/x%2Fy//z" || got.URL.RawQuery != "q=1&q=2" {
		t.Errorf("request line changed: %s %s?%s", got.Method, got.URL.EscapedPath(), got.URL.RawQuery)
	}
	if got.Host != strings.TrimPrefix(fs.URL, "http://") {
		t.Errorf("Host not preserved: %s", got.Host)
	}
	if got.Header.Get("Cookie") != "cn_session=tok" || got.Header.Get("X-Forwarded-For") != "" {
		t.Errorf("headers: %v", got.Header)
	}
}

func TestWebSocketUpgradePassthrough(t *testing.T) {
	fs := front(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" || r.URL.Path != "/collab/room" {
			http.Error(w, "bad", 400)
			return
		}
		conn, rw, _ := w.(http.Hijacker).Hijack()
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		line, _ := rw.ReadString('\n') // echo one line back, uppercased: proves bytes flow both ways
		_, _ = rw.WriteString(strings.ToUpper(line))
		_ = rw.Flush()
	}))
	c, err := net.Dial("tcp", strings.TrimPrefix(fs.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = io.WriteString(c, "GET /collab/room HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	br := bufio.NewReader(c)
	status, _ := br.ReadString('\n')
	if !strings.Contains(status, "101") {
		t.Fatalf("status %q", status)
	}
	for l, _ := br.ReadString('\n'); l != "\r\n"; l, _ = br.ReadString('\n') {
	}
	_, _ = io.WriteString(c, "hello\n")
	if echo, _ := br.ReadString('\n'); echo != "HELLO\n" {
		t.Fatalf("echo %q", echo)
	}
}

func TestStreamsWithoutBuffering(t *testing.T) {
	release := make(chan struct{})
	fs := front(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: 1\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	resp, err := http.Get(fs.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 9)
	if _, err := io.ReadFull(resp.Body, buf); err != nil || string(buf) != "data: 1\n\n" { // arrives while upstream is still open
		t.Fatalf("first event not streamed: %q %v", buf, err)
	}
	close(release)
}
