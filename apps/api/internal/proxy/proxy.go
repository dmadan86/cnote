// Package proxy forwards requests unchanged to the Rust server (the strangler). httputil.ReverseProxy
// already handles WebSocket upgrades (/collab/...) and streaming; we keep the original Host (the Rust
// server derives its public origin from it), cookies and paths, and add no X-Forwarded headers.
package proxy

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
)

func New(target string) (http.Handler, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	switch u.Scheme { // COLLAB_URL may be written as a WebSocket URL
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(u)
			r.Out.Host = r.In.Host
			r.Out.URL.RawPath = r.In.URL.RawPath // SetURL joins paths; keep the escaped form byte-identical
			r.Out.URL.Path = r.In.URL.Path
		},
		FlushInterval: -1, // stream (SSE, MCP) without buffering
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("upstream error", "path", r.URL.Path, "error", err)
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { rp.ServeHTTP(&noSniff{ResponseWriter: w}, r) }), nil
}

// noSniff keeps a response without a Content-Type without one: otherwise net/http sniffs "text/plain" into Rust's
// header-less plain-text answers (rmcp's 4xx) and the proxy stops being byte-for-byte. Unwrap keeps flushing and
// connection hijacking (SSE, WebSocket upgrades) working.
type noSniff struct {
	http.ResponseWriter
	wrote bool
}

func (n *noSniff) WriteHeader(code int) {
	if !n.wrote && code >= 200 {
		n.wrote = true
		if _, ok := n.Header()["Content-Type"]; !ok {
			n.Header()["Content-Type"] = nil
		}
	}
	n.ResponseWriter.WriteHeader(code)
}

func (n *noSniff) Write(b []byte) (int, error) {
	if !n.wrote {
		n.WriteHeader(http.StatusOK)
	}
	return n.ResponseWriter.Write(b)
}

func (n *noSniff) Unwrap() http.ResponseWriter { return n.ResponseWriter }
