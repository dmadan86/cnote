// Command api is the Go strangler front for the C Note API. See docs/go-migration.md.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/proxy"
	"github.com/cnote/api/internal/server"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if os.Getenv("LOG_LEVEL") == "debug" { // per-request native/proxy lines, for verifying routing
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}
	max, _ := strconv.Atoi(env("DB_POOL_MAX", "10"))
	pool, err := db.Connect(ctx, os.Getenv("DATABASE_URL"), int32(max))
	if err != nil {
		slog.Error("database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	upstream, err := proxy.New(env("RUST_API_URL", "http://localhost:4000"))
	if err != nil {
		slog.Error("RUST_API_URL", "error", err)
		os.Exit(1)
	}
	srv := &http.Server{
		Addr:              ":" + env("PORT", "4100"),
		Handler:           server.New(pool, modules.Registry(), upstream),
		ReadHeaderTimeout: 10 * time.Second, // no Read/WriteTimeout: WebSockets and streams are long-lived
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	slog.Info("listening", "addr", srv.Addr, "upstream", env("RUST_API_URL", "http://localhost:4000"))
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server", "error", err)
		os.Exit(1)
	}
}
