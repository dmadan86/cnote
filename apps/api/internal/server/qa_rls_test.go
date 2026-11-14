package server_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"net/http/httptest"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/proxy"
	"github.com/cnote/api/internal/server"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CN-QA-001 section 5.1: DB-001, DB-002, DB-012 (RLS sweep and connection-pool reuse).

// Tables with a workspace_id column that are deliberately global: the OAuth token endpoint and the install-code
// exchange look them up before any tenant is known (migrations/0600_oauth.sql, 0900_marketplace.sql).
var globalByDesign = map[string]bool{
	"core.oauth_codes":       true,
	"core.oauth_grants":      true,
	"core.app_install_codes": true,
}

type tbl struct{ schema, name string }

func (t tbl) q() string      { return pgx.Identifier{t.schema, t.name}.Sanitize() }
func (t tbl) String() string { return t.schema + "." + t.name }

func tenantTables(t *testing.T, e *env) []tbl {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `
select n.nspname, c.relname from pg_attribute a
join pg_class c on c.oid = a.attrelid and c.relkind in ('r','p')
join pg_namespace n on n.oid = c.relnamespace
where a.attname = 'workspace_id' and not a.attisdropped
  and n.nspname not in ('pg_catalog','information_schema') and n.nspname !~ '^pg_'
order by 1,2`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []tbl
	for rows.Next() {
		var x tbl
		if err := rows.Scan(&x.schema, &x.name); err != nil {
			t.Fatal(err)
		}
		out = append(out, x)
	}
	return out
}

func adminPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	u := os.Getenv("DATABASE_ADMIN_URL")
	if u == "" {
		return nil
	}
	p, err := db.Connect(context.Background(), u, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func sqlState(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// inTx runs fn in a transaction that is always rolled back. ws == "" means no tenant GUC at all.
func inTx(t *testing.T, e *env, ws string, fn func(tx pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	var tx pgx.Tx
	var err error
	if ws == "" {
		tx, err = e.pool.Begin(ctx)
	} else {
		tx, err = db.BeginTenant(ctx, e.pool, ws)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	fn(tx)
}

func cnt(t *testing.T, tx pgx.Tx, sql string, args ...any) (n int64, err error) {
	t.Helper()
	err = tx.QueryRow(context.Background(), sql, args...).Scan(&n)
	return
}

// DB-001 + DB-002: every table with a workspace_id column, in every schema.
func TestQARLSSweep(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := adminPool(t)
	a, b := e.signup("RLS-A", true), e.signup("RLS-B", true)
	e.note(a, "rls a note")
	e.note(b, "rls b note")

	tables := tenantTables(t, e)
	if len(tables) < 40 {
		t.Fatalf("discovery found only %d tables with workspace_id: %v", len(tables), tables)
	}

	t.Run("app role is not a superuser, owner or BYPASSRLS", func(t *testing.T) {
		var super, bypass bool
		if err := e.pool.QueryRow(ctx, `select rolsuper, rolbypassrls from pg_roles where rolname = current_user`).Scan(&super, &bypass); err != nil {
			t.Fatal(err)
		}
		if super || bypass {
			t.Fatalf("app role bypasses RLS: super=%v bypassrls=%v", super, bypass)
		}
		var owned int
		if err := e.pool.QueryRow(ctx, `select count(*) from pg_class c join pg_namespace n on n.oid=c.relnamespace
			where c.relowner = (select oid from pg_roles where rolname = current_user) and n.nspname in ('core','crm','billing','docs','projects','ai','importers','hr')`).Scan(&owned); err != nil {
			t.Fatal(err)
		}
		if owned != 0 {
			t.Fatalf("app role owns %d tenant relations; the owner is exempt from RLS", owned)
		}
	})

	if admin != nil {
		for _, w := range []who{a, b} {
			var n int64
			if err := admin.QueryRow(ctx, `select count(*) from core.notes where workspace_id = $1::uuid`, w.ws).Scan(&n); err != nil || n == 0 {
				t.Fatalf("fixture workspace %s has no notes (%v): the sweep would be vacuous", w.ws, err)
			}
		}
	}

	for _, tb := range tables {
		t.Run(tb.String(), func(t *testing.T) {
			if globalByDesign[tb.String()] {
				return
			}
			var rls, force bool
			var pol int
			if err := e.pool.QueryRow(ctx, `select relrowsecurity, relforcerowsecurity, (select count(*) from pg_policy p where p.polrelid = c.oid
				and p.polcmd = '*' and pg_get_expr(p.polqual, p.polrelid) like '%current_workspace()%' and pg_get_expr(p.polwithcheck, p.polrelid) like '%current_workspace()%')
				from pg_class c where c.oid = $1::regclass`, tb.q()).Scan(&rls, &force, &pol); err != nil {
				t.Fatal(err)
			}
			if !rls || pol == 0 {
				t.Fatalf("%s has a workspace_id column but no tenant RLS (enabled=%v, policies=%d). Add it to globalByDesign only if it is read before authentication", tb, rls, pol)
			}

			// DB-001: no tenant GUC -> nothing visible, nothing insertable.
			inTx(t, e, "", func(tx pgx.Tx) {
				if n, err := cnt(t, tx, "select count(*) from "+tb.q()); err != nil || n != 0 {
					t.Fatalf("no tenant: count=%d err=%v", n, err)
				}
			})
			inTx(t, e, "", func(tx pgx.Tx) {
				_, err := tx.Exec(ctx, "insert into "+tb.q()+" (workspace_id) values ($1::uuid)", a.ws)
				if sqlState(err) != "42501" {
					t.Fatalf("no tenant insert must fail with 42501, got %v", err)
				}
			})

			// DB-002: tenant A sees none of B and cannot write B's workspace id.
			inTx(t, e, a.ws, func(tx pgx.Tx) {
				if n, err := cnt(t, tx, "select count(*) from "+tb.q()+" where workspace_id <> $1::uuid", a.ws); err != nil || n != 0 {
					t.Fatalf("tenant A sees %d foreign rows (err %v)", n, err)
				}
				if n, err := cnt(t, tx, "select count(*) from "+tb.q()+" where workspace_id = $1::uuid", b.ws); err != nil || n != 0 {
					t.Fatalf("tenant A sees %d rows of B (err %v)", n, err)
				}
			})
			if admin != nil {
				var want int64
				if err := admin.QueryRow(ctx, "select count(*) from "+tb.q()+" where workspace_id = $1::uuid", a.ws).Scan(&want); err != nil {
					t.Fatal(err)
				}
				inTx(t, e, a.ws, func(tx pgx.Tx) {
					if n, err := cnt(t, tx, "select count(*) from "+tb.q()); err != nil || n < want {
						t.Fatalf("tenant A sees %d rows, owner sees %d for A (err %v)", n, want, err)
					}
				})
			}
			inTx(t, e, a.ws, func(tx pgx.Tx) {
				_, err := tx.Exec(ctx, "insert into "+tb.q()+" (workspace_id) values ($1::uuid)", b.ws)
				if sqlState(err) != "42501" {
					t.Fatalf("tenant A inserting B's workspace id must fail with 42501, got %v", err)
				}
			})
			inTx(t, e, a.ws, func(tx pgx.Tx) { // control: own workspace passes RLS (fails later on NOT NULL/CHECK, or succeeds)
				_, err := tx.Exec(ctx, "insert into "+tb.q()+" (workspace_id) values ($1::uuid)", a.ws)
				if sqlState(err) == "42501" {
					t.Fatalf("tenant A cannot insert its own workspace id: %v", err)
				}
			})
			for _, stmt := range []string{
				"update " + tb.q() + " set workspace_id = workspace_id where workspace_id = $1::uuid",
				"delete from " + tb.q() + " where workspace_id = $1::uuid",
			} {
				inTx(t, e, a.ws, func(tx pgx.Tx) {
					ct, err := tx.Exec(ctx, stmt, b.ws)
					if err != nil && sqlState(err) != "42501" && !appendOnly(err) { // immutable tables: no grant, or an append-only trigger
						t.Fatalf("%s: %v", stmt, err)
					}
					if err == nil && ct.RowsAffected() != 0 {
						t.Fatalf("%s touched %d rows of tenant B", stmt, ct.RowsAffected())
					}
				})
			}
			inTx(t, e, a.ws, func(tx pgx.Tx) { // moving A's own rows to B must be refused (WITH CHECK)
				ct, err := tx.Exec(ctx, "update "+tb.q()+" set workspace_id = $1::uuid where workspace_id = $2::uuid", b.ws, a.ws)
				if err != nil && sqlState(err) != "42501" && !appendOnly(err) {
					t.Fatal(err)
				}
				if err == nil && ct.RowsAffected() != 0 {
					t.Fatalf("moved %d rows into another tenant", ct.RowsAffected())
				}
			})
		})
	}
}

// DB-012: GUCs are transaction-local, so a pooled connection never carries tenant A into the next user.
func TestQAPoolReuseDoesNotLeakTenant(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.signup("Pool-A", true), e.signup("Pool-B", true)
	e.note(a, "pool a")
	e.note(b, "pool b")
	pool, err := db.Connect(ctx, os.Getenv("DATABASE_URL"), 1) // one connection: every use is a reuse
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	clean := func(label string) {
		t.Helper()
		var guc *string
		var visible int64
		if err := pool.QueryRow(ctx, `select nullif(current_setting('app.workspace_id', true), ''), (select count(*) from core.notes)`).Scan(&guc, &visible); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if guc != nil || visible != 0 {
			t.Fatalf("%s: pooled connection kept tenant %v and sees %d notes", label, guc, visible)
		}
	}
	clean("fresh")

	tenant := func(ws string, finish func(pgx.Tx)) {
		t.Helper()
		tx, err := db.BeginTenant(ctx, pool, ws)
		if err != nil {
			t.Fatal(err)
		}
		var cur string
		var foreign int64
		if err := tx.QueryRow(ctx, `select current_setting('app.workspace_id'), (select count(*) from core.notes where workspace_id <> $1::uuid)`, ws).Scan(&cur, &foreign); err != nil || cur != ws || foreign != 0 {
			t.Fatalf("tenant tx for %s: guc=%s foreign=%d err=%v", ws, cur, foreign, err)
		}
		finish(tx)
	}
	tenant(a.ws, func(tx pgx.Tx) { _ = tx.Commit(ctx) })
	clean("after commit of A")
	tenant(a.ws, func(tx pgx.Tx) { _ = tx.Rollback(ctx) })
	clean("after rollback of A")
	tenant(a.ws, func(tx pgx.Tx) { // aborted transaction
		_, _ = tx.Exec(ctx, "select 1/0")
		_ = tx.Rollback(ctx)
	})
	clean("after aborted A")
	tenant(a.ws, func(tx pgx.Tx) { _ = tx.Commit(ctx) })
	tenant(b.ws, func(tx pgx.Tx) { _ = tx.Commit(ctx) }) // B right after A: asserted inside tenant()
	clean("after A then B")

	// Through the HTTP gate with the same single connection: A, B (404 on A's note), a failing command, then clean.
	up, err := proxy.New(e.rust)
	if err != nil {
		t.Fatal(err)
	}
	one := httptest.NewServer(server.New(pool, command.NewRegistry(modules.Commands()...), up))
	defer one.Close()
	an := e.note(a, "gate a")
	for _, w := range []who{a, b, a} {
		e.cmd(one.URL, w, w.ws, "note.get", map[string]any{"id": an}, nil)
	}
	e.cmd(one.URL, a, a.ws, "note.get", map[string]any{"id": "not-a-uuid"}, nil)
	for _, w := range []who{a, b} {
		inTx(t, e, "", func(tx pgx.Tx) {
			if n, err := cnt(t, tx, "select count(*) from core.notes"); err != nil || n != 0 {
				t.Fatalf("after command for %s: untenanted connection sees %d notes (%v)", w.ws, n, err)
			}
		})
	}

	t.Run("concurrent tenants on a small pool", func(t *testing.T) {
		small, err := db.Connect(ctx, os.Getenv("DATABASE_URL"), 2)
		if err != nil {
			t.Fatal(err)
		}
		defer small.Close()
		var wg sync.WaitGroup
		errs := make(chan error, 200)
		for i := range 120 {
			ws := []string{a.ws, b.ws}[i%2]
			wg.Add(1)
			go func() {
				defer wg.Done()
				tx, err := db.BeginTenant(ctx, small, ws)
				if err != nil {
					errs <- err
					return
				}
				var foreign, own int64
				err = tx.QueryRow(ctx, `select (select count(*) from core.notes where workspace_id <> $1::uuid), (select count(*) from core.notes)`, ws).Scan(&foreign, &own)
				_ = tx.Rollback(ctx) // release the connection before the untenanted use below (pool of 2)
				if err != nil {
					errs <- err
					return
				}
				if foreign != 0 || own == 0 {
					errs <- fmt.Errorf("tenant %s: foreign=%d own=%d", ws, foreign, own)
				}
				if i%3 == 0 { // a plain (untenanted) use of the pool in between
					var n int64
					if err := small.QueryRow(ctx, "select count(*) from core.notes").Scan(&n); err != nil || n != 0 {
						errs <- fmt.Errorf("untenanted pool query saw %d notes (%v)", n, err)
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
	})
}

func appendOnly(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "P0001" && strings.Contains(pe.Message, "append-only")
}
