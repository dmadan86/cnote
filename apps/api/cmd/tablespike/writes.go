package main

import (
	"fmt"
	"strings"
	"time"
)

func (s *spike) lsn() string { return s.scalar("SELECT pg_current_wal_insert_lsn()::text") }
func (s *spike) walSince(l string) float64 {
	var f float64
	fmt.Sscan(s.scalar(fmt.Sprintf("SELECT pg_wal_lsn_diff(pg_current_wal_insert_lsn(), '%s')::bigint", l)), &f)
	return f
}
func (s *spike) hot() (upd, hot float64) {
	s.mustExec("SELECT pg_stat_force_next_flush()")
	var a, b float64
	check2(s.c.QueryRow(s.ctx, "SELECT n_tup_upd, n_tup_hot_upd FROM pg_stat_user_tables WHERE relid='spike.records'::regclass").Scan(&a, &b))
	return a, b
}
func check2(err error) {
	if err != nil {
		fatal("query failed: " + shortErr(err))
	}
}

// writeOp runs reps transactions as cnote_app; stmts(i) returns the statements for repetition i.
func (s *spike) writeOp(st, op string, rowsPer, reps int, commit bool, stmts func(i int) []string) {
	var t, wal, cold []float64
	s.mustExec("CHECKPOINT") // first touch of each page after a checkpoint logs a full-page image
	u0, h0 := s.hot()
	for i := 0; i < 2*reps; i++ {
		warm := i >= reps // second pass over the same rows: pages already dirtied, no full-page images
		i := i % reps
		l := s.lsn()
		tx, err := s.c.Begin(s.ctx)
		check2(err)
		_, err = tx.Exec(s.ctx, "SET LOCAL ROLE cnote_app")
		check2(err)
		_, err = tx.Exec(s.ctx, "SELECT set_config('app.workspace_id','"+W1+"',true)")
		check2(err)
		t0 := time.Now()
		for _, q := range stmts(i) {
			_, err = tx.Exec(s.ctx, q)
			check2(err)
		}
		if commit {
			check2(tx.Commit(s.ctx))
		} else {
			check2(tx.Rollback(s.ctx))
		}
		if warm {
			t = append(t, ms(time.Since(t0)))
			wal = append(wal, s.walSince(l)/float64(rowsPer))
		} else {
			cold = append(cold, s.walSince(l)/float64(rowsPer))
		}
	}
	u1, h1 := s.hot()
	hp := "n/a"
	if u1 > u0 {
		hp = fmt.Sprintf("%.0f", 100*(h1-h0)/(u1-u0))
	}
	s.wlines = append(s.wlines, fmt.Sprintf("| %s | %s | %.1f | %.1f | %.0f | %.0f | %s |", st, op, pct(t, .5), pct(t, .95), pct(wal, .5), pct(cold, .5), hp))
}

func (s *spike) writes(st string) {
	s.sizes = append(s.sizes, fmt.Sprintf("%s: records heap %.0f MB, table incl. TOAST %.0f MB, all indexes on records %.0f MB",
		st, mb(s.scalar("SELECT pg_relation_size('spike.records')")), mb(s.scalar("SELECT pg_table_size('spike.records')")), mb(s.scalar("SELECT pg_indexes_size('spike.records')"))))
	var ids []string
	rows, err := s.c.Query(s.ctx, fmt.Sprintf("SELECT id::text FROM spike.records WHERE table_id='%s' AND deleted_at IS NULL AND spike.jt(data,'f03') IN ('Open','In progress') AND workspace_id='%s' ORDER BY created_at LIMIT 7000", TA, W1))
	check2(err)
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	arr := func(a []string) string { return "ARRAY['" + strings.Join(a, "','") + "']::uuid[]" }
	side := func(where string) []string {
		if st != "S4 side" {
			return nil
		}
		return []string{fmt.Sprintf(`UPDATE spike.sk k SET s03=spike.jt(r.data,'f03'), n05=spike.jf(r.data,'f05') FROM spike.records r WHERE r.table_id='%s' AND r.id=k.id AND k.table_id='%s' AND k.%s`, TA, TA, where)}
	}
	f02 := func(where string) string {
		return fmt.Sprintf("UPDATE spike.records SET data=jsonb_set(data,'{f02}',to_jsonb(md5(random()::text))), updated_at=now() WHERE table_id='%s' AND %s", TA, where)
	}
	f035 := func(where string) []string {
		return append([]string{fmt.Sprintf(`UPDATE spike.records SET data = data || jsonb_build_object('f03', CASE WHEN data->>'f03'='Open' THEN 'In progress' ELSE 'Open' END, 'f05', (data->>'f05')::numeric + 1), updated_at=now() WHERE table_id='%s' AND %s`, TA, where)}, side(where)...)
	}
	s.writeOp(st, "update 1 record, unindexed field (f02)", 1, 100, true, func(i int) []string { return []string{f02("id='" + ids[i] + "'")} })
	s.writeOp(st, "update 1 record, select+number (f03,f05)", 1, 100, true, func(i int) []string { return f035("id='" + ids[100+i] + "'") })
	s.writeOp(st, "bulk update 1000, select+number", 1000, 5, true, func(i int) []string {
		return f035("id = ANY(" + arr(ids[1000+i*1000:2000+i*1000]) + ")")
	})
	ins := fmt.Sprintf(`INSERT INTO spike.records (workspace_id, table_id, id, data, created_at, updated_at, deleted_at)
SELECT workspace_id, table_id, gen_random_uuid(), data, now(), now(), NULL FROM spike.records WHERE table_id='%s' AND deleted_at IS NULL AND workspace_id='%s' LIMIT 1000`, TA, W1)
	if st == "S4 side" {
		ins = `WITH ins AS (` + ins + ` RETURNING *) INSERT INTO spike.sk SELECT workspace_id, table_id, id, created_at, spike.jt(data,'f03'), spike.jf(data,'f05'), (data->>'f07')::date, spike.jt(data,'f01') FROM ins`
	}
	s.writeOp(st, "bulk insert 1000 (rolled back)", 1000, 5, false, func(int) []string { return []string{ins} })
}

// rawDemo shows what RLS does to naive expression indexes: same data, same indexes, same query,
// run as the non-superuser app role vs a superuser (which bypasses RLS).
func (s *spike) rawDemo() {
	s.apply("raw", "S0 none")
	p := fmt.Sprintf("WHERE table_id='%s' AND deleted_at IS NULL", TA)
	s.mustExec("CREATE INDEX ix_raw03 ON spike.records ((data->>'f03'), id) " + p)
	s.mustExec("CREATE INDEX ix_raw12 ON spike.records ((data->>'f12'), id) " + p)
	s.mustExec("CREATE INDEX ix_raw05 ON spike.records (((data->>'f05')::numeric), id) " + p)
	s.mustExec("ANALYZE spike.records")
	from := fmt.Sprintf("FROM spike.records r WHERE r.workspace_id='%s' AND r.table_id='%s' AND r.deleted_at IS NULL", W1, TA)
	qs := [][2]string{
		{"eq_rare (data->>'f12' = ...)", "SELECT r.id, r.data " + from + " AND r.data->>'f12'='Company 77' ORDER BY r.created_at, r.id LIMIT 100"},
		{"range_rare ((data->>'f05')::numeric)", "SELECT r.id, r.data " + from + " AND (r.data->>'f05')::numeric BETWEEN 50000 AND 50050 ORDER BY r.created_at, r.id LIMIT 100"},
		{"sort_num_0", "SELECT r.id, r.data " + from + " ORDER BY (r.data->>'f05')::numeric, r.id LIMIT 100"},
	}
	s.raw = append(s.raw, "Raw expression indexes (`(data->>'f03')`, `((data->>'f05')::numeric)`) created, same query as cnote_app (RLS) and as superuser (bypasses RLS).\n", "| query | cnote_app p50 / p95 ms [plan] | superuser p50 / p95 ms [plan] |", "|---|---|---|")
	for _, q := range qs {
		a := s.bench("cnote_app", W1, q[1])
		b := s.bench("", W1, q[1])
		s.raw = append(s.raw, fmt.Sprintf("| %s | %.1f / %.1f [%s] | %.1f / %.1f [%s] |", q[0], a.p50, a.p95, a.plan, b.p50, b.p95, b.plan))
	}
}
