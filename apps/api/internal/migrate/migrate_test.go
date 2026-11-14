package migrate

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Applies the real migrations to a throwaway database, then checks idempotency and checksum refusal.
func TestRun(t *testing.T) {
	admin := os.Getenv("DATABASE_ADMIN_URL")
	if admin == "" {
		t.Skip("DATABASE_ADMIN_URL not set")
	}
	ctx := context.Background()
	root, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close(ctx)
	name := fmt.Sprintf("cnote_migrate_test_%d", time.Now().UnixNano())
	if _, err := root.Exec(ctx, `create database "`+name+`"`); err != nil {
		t.Fatal(err)
	}
	defer root.Exec(ctx, `drop database "`+name+`" with (force)`)
	cfg, _ := pgx.ParseConfig(admin)
	cfg.Database = name
	url := strings.Replace(admin, "/"+root.Config().Database, "/"+name, 1)
	log := func(string, ...any) {}
	if err := Run(ctx, url, "../../../../migrations", false, log); err != nil {
		t.Fatal(err)
	}
	if err := Run(ctx, url, "../../../../migrations", false, log); err != nil {
		t.Fatalf("second run must be a no-op: %v", err)
	}
	dir := t.TempDir()
	os.WriteFile(dir+"/0001_x.sql", []byte("select 1"), 0o644)
	if err := Run(ctx, url, dir, false, log); err == nil {
		t.Fatal("changed/missing migrations must be refused")
	}
}
