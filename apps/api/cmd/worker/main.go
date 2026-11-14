// Command worker runs only the C Note background worker (event dispatch, jobs, sweeps): the Go counterpart of
// `cnote worker`. It can run next to the Rust worker; both claim jobs with core.claim_jobs (FOR UPDATE SKIP LOCKED).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/worker"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	max := 10
	if v, err := strconv.Atoi(os.Getenv("DB_POOL_MAX")); err == nil && v > 0 {
		max = v
	}
	pool, err := db.Connect(ctx, os.Getenv("DATABASE_URL"), int32(max))
	if err != nil {
		slog.Error("database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	worker.New(pool, modules.Registry()).Run(ctx)
}
