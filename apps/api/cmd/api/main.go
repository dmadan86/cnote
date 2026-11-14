// Command api is the Go strangler front for the C Note API. See docs/go-migration.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/migrate"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/modules/approvals"
	"github.com/cnote/api/internal/modules/billing"
	"github.com/cnote/api/internal/modules/files"
	"github.com/cnote/api/internal/modules/recurring"
	"github.com/cnote/api/internal/proxy"
	"github.com/cnote/api/internal/server"
	"github.com/cnote/api/internal/worker"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "migrate" { // `api migrate [-reset]`: apply migrations with DATABASE_ADMIN_URL
		fs := flag.NewFlagSet("migrate", flag.ExitOnError)
		reset := fs.Bool("reset", false, "drop the module schemas first (development only)")
		_ = fs.Parse(os.Args[2:])
		logf := func(f string, a ...any) { slog.Info(fmt.Sprintf(f, a...)) }
		if err := migrate.Run(context.Background(), os.Getenv("DATABASE_ADMIN_URL"), env("MIGRATIONS_DIR", "migrations"), *reset, logf); err != nil {
			slog.Error("migrate", "error", err)
			os.Exit(1)
		}
		return
	}
	// Like `cnote serve --with-worker` in Rust (flag, or env WITH_WORKER): run the job worker in this process.
	withWorker := flag.Bool("with-worker", worker.Boolish(os.Getenv("WITH_WORKER")), "also run the background worker (event dispatch, jobs, sweeps)")
	flag.Parse()
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

	registry := modules.Registry()
	sweepSecs, err := strconv.Atoi(env("RECURRING_SWEEP_SECS", "60"))
	if err != nil || sweepSecs < 0 {
		slog.Error("invalid RECURRING_SWEEP_SECS")
		os.Exit(1)
	}
	recurring.Start(ctx, pool, registry, time.Duration(sweepSecs)*time.Second)
	approvalSecs, err := strconv.Atoi(env("APPROVAL_SWEEP_SECS", "300"))
	if err != nil || approvalSecs < 0 {
		slog.Error("invalid APPROVAL_SWEEP_SECS")
		os.Exit(1)
	}
	if on, err := files.ConfigureFromEnv(); err != nil {
		slog.Error("file storage", "error", err)
		os.Exit(1)
	} else if on {
		slog.Info("file storage: S3")
	}
	approvals.Start(ctx, pool, time.Duration(approvalSecs)*time.Second, billing.TodayIST)
	if *withWorker {
		go worker.New(pool, registry).Run(ctx)
	}
	front := server.New(pool, registry)
	if u := os.Getenv("COLLAB_URL"); u != "" { // the TypeScript collab service
		if front.Collab, err = proxy.New(u); err != nil {
			slog.Error("COLLAB_URL", "error", err)
			os.Exit(1)
		}
		if os.Getenv("COLLAB_INTERNAL_SECRET") == "" { // note.restore renders the live document through the collab service
			slog.Warn("COLLAB_INTERNAL_SECRET is not set: note.restore cannot render live documents")
		}
	}
	srv := &http.Server{
		Addr:              ":" + env("PORT", "4000"),
		Handler:           front,
		ReadHeaderTimeout: 10 * time.Second, // no Read/WriteTimeout: WebSockets and streams are long-lived
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	slog.Info("listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server", "error", err)
		os.Exit(1)
	}
}
