// Package migrate applies migrations/*.sql, byte-compatible with sqlx's _sqlx_migrations table
// (version, description with spaces, sha384 checksum of the file bytes), so databases created by the
// retired Rust binary keep working. Every file runs in its own transaction.
package migrate

import (
	"bytes"
	"context"
	"crypto/sha512"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type file struct {
	version int64
	desc    string
	sql     string
	sum     []byte
}

func load(dir string) ([]file, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return nil, err
	}
	var out []file
	for _, p := range paths {
		stem, _, _ := strings.Cut(filepath.Base(p), ".")
		num, desc, ok := strings.Cut(stem, "_")
		v, err := strconv.ParseInt(num, 10, 64)
		if !ok || err != nil {
			return nil, fmt.Errorf("migration %s: name must be <version>_<description>.sql", p)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		if bytes.HasPrefix(b, []byte("-- no-transaction")) {
			return nil, fmt.Errorf("migration %s: no-transaction migrations are not supported", p)
		}
		sum := sha512.Sum384(b)
		out = append(out, file{v, strings.ReplaceAll(desc, "_", " "), string(b), sum[:]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// Run applies pending migrations with the owner connection (DATABASE_ADMIN_URL). It is idempotent.
// reset drops the module schemas and the migrations table first (development only).
func Run(ctx context.Context, adminURL, dir string, reset bool, logf func(string, ...any)) error {
	files, err := load(dir)
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	var dbName string
	if err := conn.QueryRow(ctx, "select current_database()").Scan(&dbName); err != nil {
		return err
	}
	// the lock id sqlx uses, so a Go and a Rust migrator can never run at once
	lock := int64(0x3d32ad9e) * int64(crc32.ChecksumIEEE([]byte(dbName)))
	if _, err := conn.Exec(ctx, "select pg_advisory_lock($1)", lock); err != nil {
		return err
	}
	defer conn.Exec(context.WithoutCancel(ctx), "select pg_advisory_unlock($1)", lock) //nolint:errcheck
	if reset {
		if _, err := conn.Exec(ctx, "DROP SCHEMA IF EXISTS core, projects, docs, crm, billing, ai, importers CASCADE; DROP TABLE IF EXISTS public._sqlx_migrations;"); err != nil {
			return err
		}
	}
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS _sqlx_migrations (
    version BIGINT PRIMARY KEY, description TEXT NOT NULL, installed_on TIMESTAMPTZ NOT NULL DEFAULT now(),
    success BOOLEAN NOT NULL, checksum BYTEA NOT NULL, execution_time BIGINT NOT NULL)`); err != nil {
		return err
	}
	rows, err := conn.Query(ctx, "select version, success, checksum from _sqlx_migrations order by version")
	if err != nil {
		return err
	}
	applied := map[int64][]byte{}
	for rows.Next() {
		var v int64
		var ok bool
		var sum []byte
		if err := rows.Scan(&v, &ok, &sum); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("migration %d is partially applied (dirty); fix the database by hand", v)
		}
		applied[v] = sum
	}
	if err := rows.Err(); err != nil {
		return err
	}
	local := map[int64]bool{}
	for _, f := range files {
		local[f.version] = true
		if sum, ok := applied[f.version]; ok && !bytes.Equal(sum, f.sum) {
			return fmt.Errorf("migration %d was applied but its file has changed (checksum mismatch)", f.version)
		}
	}
	for v := range applied {
		if !local[v] {
			return fmt.Errorf("migration %d is applied but missing from %s", v, dir)
		}
	}
	n := 0
	for _, f := range files {
		if _, ok := applied[f.version]; ok {
			continue
		}
		start := time.Now()
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, f.sql); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %d %s: %w", f.version, f.desc, err)
		}
		if _, err := tx.Exec(ctx, "insert into _sqlx_migrations(version, description, success, checksum, execution_time) values ($1,$2,true,$3,$4)",
			f.version, f.desc, f.sum, time.Since(start).Nanoseconds()); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		n++
		logf("applied migration %d %s", f.version, f.desc)
	}
	logf("migrations up to date (%d applied now)", n)
	return nil
}
