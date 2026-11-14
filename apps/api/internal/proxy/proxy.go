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
	return &httputil.ReverseProxy{
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
	}, nil
}
