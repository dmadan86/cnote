package server

import (
	"context"
	"errors"
	"github.com/cnote/api/internal/modules/sites"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"net/http"
	"time"
)

func (s *Server) siteHit(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}
func (s *Server) siteExport(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
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
