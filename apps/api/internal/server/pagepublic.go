package server

import (
	"io"
	"net/http"
)

// pagePublicPrefix is where the public page endpoints live: GET /api/public/page/<token> and
// GET /api/public/page/<token>/files/<id> (docs-parity-b 4.2 and 9.1).
const pagePublicPrefix = "/api/public/page/"

// pagePublic is a step 0 placeholder: every request gets the one 404 of 9.1, so the route exists and nothing is served.
// P2 replaces this whole file with the real handler (limits first, resolve, FOR SHARE, PublicBody).
func (s *Server) pagePublic(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"This page is not available."}}`)
}
