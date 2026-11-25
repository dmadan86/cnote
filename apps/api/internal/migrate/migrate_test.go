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

// A `-- no-transaction` file runs outside a transaction (CREATE INDEX CONCURRENTLY needs that) and is still recorded.
func TestNoTransactionMigration(t *testing.T) {
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
	name := fmt.Sprintf("cnote_migrate_notx_%d", time.Now().UnixNano())
	if _, err := root.Exec(ctx, `create database "`+name+`"`); err != nil {
		t.Fatal(err)
	}
	defer root.Exec(ctx, `drop database "`+name+`" with (force)`)
	url := strings.Replace(admin, "/"+root.Config().Database, "/"+name, 1)
	log := func(string, ...any) {}
	dir := t.TempDir()
	os.WriteFile(dir+"/0001_t.sql", []byte("create table t(a int)"), 0o644)
	os.WriteFile(dir+"/0002_plain_idx.sql", []byte("create index concurrently t_a on t(a)"), 0o644)
	if err := Run(ctx, url, dir, false, log); err == nil {
		t.Fatal("CREATE INDEX CONCURRENTLY inside the runner's transaction must fail")
	}
	os.WriteFile(dir+"/0002_plain_idx.sql", []byte("-- no-transaction\ncreate index concurrently t_a on t(a)"), 0o644)
	if err := Run(ctx, url, dir, false, log); err != nil {
		t.Fatal(err)
	}
	if err := Run(ctx, url, dir, false, log); err != nil {
		t.Fatalf("second run must be a no-op: %v", err)
	}
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	var n int
	if err := c.QueryRow(ctx, "select count(*) from pg_indexes where indexname = 't_a'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("index missing: %v %d", err, n)
	}
}
