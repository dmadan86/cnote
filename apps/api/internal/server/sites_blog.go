package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/sites"
)

// sitePublicPosts serves the web server's reads of published posts (server token, like public_get):
// {host, page?, limit?} answers one page of summaries, {host, slug} one post with its body. A site that is not live,
// an unknown slug and a page past the end are all 404.
func (s *Server) sitePublicPosts(w http.ResponseWriter, r *http.Request, one bool) {
	want := os.Getenv("SITES_SERVER_TOKEN")
	a, b := sha256.Sum256([]byte(r.Header.Get("x-sites-token"))), sha256.Sum256([]byte(want))
	if want == "" || subtle.ConstantTimeCompare(a[:], b[:]) != 1 {
		notFound(w)
		return
	}
	var in struct {
		Host  string `json:"host"`
		Slug  string `json:"slug"`
		Page  int    `json:"page"`
		Limit int    `json:"limit"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 2048)).Decode(&in) != nil {
		notFound(w)
		return
	}
	pub, err := sites.LookupHost(r.Context(), s.Pool, in.Host)
	if err != nil {
		notFound(w)
		return
	}
	var out map[string]any
	if one {
		out, err = sites.PublicPost(r.Context(), s.Pool, pub.SiteID, in.Slug)
		if err == nil {
			out = map[string]any{"post": out}
		}
	} else {
		out, err = sites.PublicPosts(r.Context(), s.Pool, pub.SiteID, in.Page, in.Limit)
	}
	if err != nil {
		notFound(w)
		return
	}
	body, _ := command.Marshal(out)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, body)
}
