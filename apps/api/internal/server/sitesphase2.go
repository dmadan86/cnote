package server

import (
	"context"
	"errors"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/sites"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// siteHit is POST /api/sites/hit/{site_id} (design 17.8): the visit beacon. Limits run before any write; once they
// pass, a storage error is dropped silently because a lost count must never show to a visitor.
func (s *Server) siteHit(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/sites/hit/")
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "text/plain" {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 513))
	if err != nil || len(body) > 512 {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	slug, ref, ok := sites.ParseHit(body)
	if !ok || !wsRe.MatchString(id) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := sites.HitAllowed(r.Context(), s.Pool, sites.ClientIP(r, sites.TrustedProxies())); err != nil {
		if errors.Is(err, sites.ErrHitLimited) {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		slog.Warn("site hit limit", "error", err)
	} else if err := sites.RecordHit(r.Context(), s.Pool, id, slug, ref); err != nil {
		slog.Warn("site hit", "error", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

var exportRe = regexp.MustCompile(`^/api/w/([^/]+)/sites/([^/]+)/export$`)

// siteExport is GET /api/w/{ws}/sites/{id}/export (design 17.9): the zip of the published version, as a download.
func (s *Server) siteExport(w http.ResponseWriter, r *http.Request) {
	m := exportRe.FindStringSubmatch(r.URL.EscapedPath())
	if !wsRe.MatchString(m[1]) || !wsRe.MatchString(m[2]) {
		notFound(w)
		return
	}
	p, err := s.Auth.PrincipalFor(r.Context(), r.Header, m[1])
	var zipped []byte
	if err == nil {
		err = s.Registry.WithTx(r.Context(), s.Pool, p, func(c *command.Ctx) (e error) {
			zipped, e = sites.BuildExport(c, m[2])
			return e
		})
	}
	if err != nil {
		s.siteFail(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", `attachment; filename="website-content.zip"`)
	h.Set("Content-Length", strconv.Itoa(len(zipped)))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-store")
	_, _ = w.Write(zipped)
}
func StartSitesAsk(ctx context.Context, pool *pgxpool.Pool, addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/tls-ask", func(w http.ResponseWriter, r *http.Request) {
		ok, err := sites.TLSAsk(r.Context(), pool, r.URL.Query().Get("domain"))
		if err != nil || !ok {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("sites ask listener", "error", err)
		}
	}()
}
