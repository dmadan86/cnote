// Command tablespike is a THROWAWAY performance spike for the planned "Tables" module
// (see docs/design/tables-spike.md). It builds a scratch database, loads synthetic data,
// benchmarks index strategies under tenant RLS as a non-superuser role and writes a markdown
// results file. It never touches the product schema or migrations.
//
//	set -a; . ./.env; set +a
//	go run ./cmd/tablespike -out /path/to/results.md
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

//go:embed schema.sql
var schemaSQL string

const (
	dbName = "cnote_tables_spike"
	W1     = "00000000-0000-0000-0000-000000000001" // the 100k-record tenant
	W2     = "00000000-0000-0000-0000-000000000002" // noisy neighbour
	W3     = "00000000-0000-0000-0000-000000000003" // small tenant
	TA     = "00000000-0000-0000-0000-0000000000a1" // 100k records, 20 fields
	TB     = "00000000-0000-0000-0000-0000000000b1" // 5k records, link target
	TN     = "00000000-0000-0000-0000-0000000000c1" // 500k noisy records
	T3a    = "00000000-0000-0000-0000-0000000000d1"
	T3b    = "00000000-0000-0000-0000-0000000000d2"
	nA     = 100000
)

var (
	strategies = []string{"S0 none", "S1 gin", "S2 btree", "S3 hybrid", "S4 side"}
	// index set per strategy; "side" also builds the typed side table.
	stratIdx = map[string][]string{
		"S0 none": nil, "S1 gin": {"ix_gin"}, "S2 btree": {"ix_f03", "ix_f05", "ix_f07", "ix_f01", "ix_f12"},
		"S3 hybrid": {"ix_gin", "ix_f03", "ix_f05", "ix_f07", "ix_f01", "ix_f12", "ix_trgm"},
		"S4 side":   {"ix_k03", "ix_k05", "ix_k07", "ix_k01"},
	}
	word = fmt.Sprintf("%x", md5sum("7"))[:6] // matches ~1/150 of names
)

type res struct {
	p50, p95, exec float64 // ms
	plan           string
	rows           int
	err            string
}

type build struct {
	pass, strat, idx string
	ms               float64
	mb               float64
}

type spike struct {
	ctx                context.Context
	c                  *pgx.Conn
	out                strings.Builder
	build              []build
	rd                 map[string]map[string]map[string]res // pass -> strat -> query -> res
	qord               []string
	anch               anchors
	bids               []string
	wlines, sizes, raw []string
}

type anchors struct{ num, date, text, nid, did, tid string }

func main() {
	outPath := flag.String("out", "tablespike-results.md", "markdown results file")
	flag.Parse()
	ctx := context.Background()
	admin := os.Getenv("DATABASE_ADMIN_URL")
	if admin == "" {
		fatal("DATABASE_ADMIN_URL not set")
	}
	root, err := pgx.Connect(ctx, admin)
	check(err, "connect admin")
	_, err = root.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
	check(err, "drop")
	_, err = root.Exec(ctx, "CREATE DATABASE "+dbName)
	check(err, "create db")
	root.Close(ctx)
	scratch := regexp.MustCompile(`(//[^/]*/)[^?]*`).ReplaceAllString(admin, "${1}"+dbName)
	cfg, err := pgx.ParseConfig(scratch)
	check(err, "parse")
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol // literals, no plan-cache effects
	c, err := pgx.ConnectConfig(ctx, cfg)
	check(err, "connect scratch")
	s := &spike{ctx: ctx, c: c, rd: map[string]map[string]map[string]res{}}
	s.mustExec(schemaSQL)

	s.load(W1, TA, nA, true)
	s.load3()
	s.mustExec("VACUUM ANALYZE")
	s.header()
	logf("pass 1: no noisy neighbour")
	s.runPass("pass1", []string{"S0 none", "S3 hybrid"}, true)

	logf("loading noisy neighbour")
	s.load(W2, TN, 500000, false)
	s.mustExec("VACUUM ANALYZE")
	logf("pass 2: with noisy neighbour")
	s.runPass("pass2", strategies, false)
	s.rawDemo()
	s.report()
	check(os.WriteFile(*outPath, []byte(s.out.String()), 0o644), "write")
	c.Close(ctx)
	logf("done -> %s (scratch DB %s left for caller to drop)", *outPath, dbName)
}

func md5sum(s string) [16]byte { return md5Sum([]byte(s)) }

func logf(f string, a ...any) { fmt.Fprintf(os.Stderr, time.Now().Format("15:04:05 ")+f+"\n", a...) }
func fatal(m string)          { fmt.Fprintln(os.Stderr, m); os.Exit(1) }

// check never prints the underlying error for connection steps: it can carry the DSN.
func check(err error, what string) {
	if err != nil {
		fatal(what + " failed: " + shortErr(err))
	}
}
func shortErr(err error) string {
	m := err.Error()
	if len(m) > 200 {
		m = m[:200]
	}
	return m
}

func (s *spike) mustExec(sql string) {
	if _, err := s.c.Exec(s.ctx, sql); err != nil {
		fatal("exec failed: " + shortErr(err) + " :: " + firstLine(sql))
	}
}
func firstLine(s string) string {
	if i := strings.IndexByte(strings.TrimSpace(s), '\n'); i > 0 {
		return strings.TrimSpace(s)[:i]
	}
	return strings.TrimSpace(s)
}
func (s *spike) scalar(sql string) string {
	var v string
	if err := s.c.QueryRow(s.ctx, sql).Scan(&v); err != nil {
		fatal("scalar failed: " + shortErr(err) + " :: " + firstLine(sql))
	}
	return v
}

// ---- data load -------------------------------------------------------------------------------

const genSQL = `INSERT INTO spike.records (workspace_id, table_id, id, data, created_at, updated_at)
SELECT '%[1]s', '%[2]s', gen_random_uuid(), jsonb_build_object(
 'f01', 'Customer '||g||' '||substr(md5((g%%150)::text),1,6),
 'f02', substr(md5(g::text)||md5((g+1)::text), 1, 40+(g%%60)),
 'f03', (ARRAY['Open','In progress','Blocked','Waiting','Done','Won','Lost','Archived'])[1+floor(random()*8)::int],
 'f04', COALESCE((SELECT jsonb_agg((ARRAY['vip','new','lead','b2b','b2c','retail','wholesale','export','local','repeat','churn','promo'])[1+abs(hashint8(g*10+i))%%12]) FROM generate_series(1, abs(hashint8(g))%%4) i), '[]'::jsonb),
 'f05', round((random()*100000)::numeric,2),
 'f06', (random()*10000000)::bigint,
 'f07', (date '2024-01-01' + (random()*1095)::int)::text,
 'f08', to_char(timestamptz '2026-01-01' + g*interval '1 minute','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
 'f09', random()<0.3,
 'f10', '+91'||(9000000000+(random()*999999999)::bigint)::text,
 'f11', 'user'||g||'@'||(ARRAY['gmail.com','acme.in','example.com'])[1+g%%3],
 'f12', 'Company '||(g%%2000),
 'f13', 'City '||(1+(random()*29)::int),
 'f14', (random()*500)::int,
 'f15', (ARRAY['Low','Medium','High','Urgent'])[1+(random()*3)::int],
 'f16', 1+(random()*4)::int,
 'f17', 'https://example.com/p/'||g,
 'f18', 'owner'||(1+(random()*9)::int),
 'f19', 'SKU-'||lpad(g::text,7,'0')),
 timestamptz '2026-01-01' + g*interval '1 minute', timestamptz '2026-01-01' + g*interval '1 minute'
FROM generate_series(1,%[3]d) g`

func (s *spike) load(ws, tbl string, n int, withLinks bool) {
	t0 := time.Now()
	s.mustExec(fmt.Sprintf(genSQL, ws, tbl, n))
	if withLinks {
		s.mustExec(fmt.Sprintf(`INSERT INTO spike.records (workspace_id, table_id, id, data, created_at, updated_at)
SELECT '%s','%s',gen_random_uuid(), jsonb_build_object('f01','Product '||g,'f02','Item '||md5(g::text)), timestamptz '2026-01-01'+g*interval '1 minute', now() FROM generate_series(1,5000) g`, ws, TB))
		s.mustExec(fmt.Sprintf(`WITH a AS (SELECT id, row_number() OVER (ORDER BY created_at) rn FROM spike.records WHERE table_id='%s'),
 b AS (SELECT id, row_number() OVER (ORDER BY created_at) rn FROM spike.records WHERE table_id='%s')
INSERT INTO spike.links SELECT '%s', a.id, 'f20', b.id FROM a CROSS JOIN LATERAL generate_series(1, (a.rn %% 4)::int) k
JOIN b ON b.rn = 1 + abs(hashint8(a.rn*10+k)) %% 5000 ON CONFLICT DO NOTHING`, tbl, TB, ws))
	}
	logf("loaded %d rows into %s in %.1fs", n, tbl[len(tbl)-2:], time.Since(t0).Seconds())
}

func (s *spike) load3() {
	s.load(W3, T3a, 5000, false)
	s.load(W3, T3b, 5000, false)
}

// ---- strategies ------------------------------------------------------------------------------

func idxDDL(name string) string {
	p := fmt.Sprintf("WHERE table_id='%s' AND deleted_at IS NULL", TA)
	switch name {
	case "ix_gin":
		return "CREATE INDEX ix_gin ON spike.records USING gin (data spike.jsonb_lp_ops) " + p
	case "ix_f03":
		return "CREATE INDEX ix_f03 ON spike.records (spike.jt(data,'f03'), id) " + p
	case "ix_f05":
		return "CREATE INDEX ix_f05 ON spike.records (spike.jf(data,'f05'), id) " + p
	case "ix_f07":
		return "CREATE INDEX ix_f07 ON spike.records (spike.jt(data,'f07'), id) " + p
	case "ix_f01":
		return "CREATE INDEX ix_f01 ON spike.records (spike.jt(data,'f01'), id) " + p
	case "ix_f12":
		return "CREATE INDEX ix_f12 ON spike.records (spike.jt(data,'f12'), id) " + p
	case "ix_trgm":
		return "CREATE INDEX ix_trgm ON spike.records USING gin (spike.jt(data,'f01') spike.trgm_lp_ops) " + p
	case "ix_k03":
		return "CREATE INDEX ix_k03 ON spike.sk (s03, id)"
	case "ix_k05":
		return "CREATE INDEX ix_k05 ON spike.sk (n05, id)"
	case "ix_k07":
		return "CREATE INDEX ix_k07 ON spike.sk (d07, id)"
	case "ix_k01":
		return "CREATE INDEX ix_k01 ON spike.sk (t01, id)"
	}
	panic(name)
}

func (s *spike) apply(pass, strat string) {
	s.mustExec(`DO $$ DECLARE r record; BEGIN
 FOR r IN SELECT indexname FROM pg_indexes WHERE schemaname='spike' AND indexname LIKE 'ix\_%' AND indexname NOT IN ('ix_base_created','ix_links_rev') LOOP
  EXECUTE 'DROP INDEX spike.'||r.indexname; END LOOP; END $$;
 DROP TABLE IF EXISTS spike.sk`)
	if strat == "S4 side" {
		t0 := time.Now()
		s.mustExec(`CREATE TABLE spike.sk (workspace_id uuid NOT NULL, table_id uuid NOT NULL, id uuid NOT NULL, created_at timestamptz NOT NULL,
 s03 text, n05 float8, d07 date, t01 text, PRIMARY KEY (workspace_id, table_id, id));
 INSERT INTO spike.sk SELECT workspace_id, table_id, id, created_at, spike.jt(data,'f03'), spike.jf(data,'f05'), (data->>'f07')::date, spike.jt(data,'f01')
  FROM spike.records WHERE table_id='` + TA + `' AND deleted_at IS NULL;
 SELECT core.enable_tenant_rls('spike.sk'); GRANT SELECT, INSERT, UPDATE, DELETE ON spike.sk TO cnote_app`)
		s.build = append(s.build, build{pass, strat, "sk table + pk (populate)", ms(time.Since(t0)), mb(s.scalar("SELECT pg_total_relation_size('spike.sk')"))})
	}
	for _, n := range stratIdx[strat] {
		t0 := time.Now()
		s.mustExec(idxDDL(n))
		d := time.Since(t0)
		s.build = append(s.build, build{pass, strat, n, ms(d), mb(s.scalar("SELECT pg_relation_size('spike." + n + "')"))})
	}
	s.mustExec("VACUUM ANALYZE") // expression-index stats only exist after ANALYZE
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
func mb(v string) float64 {
	var f float64
	fmt.Sscan(v, &f)
	return f / 1048576
}

// ---- running queries -------------------------------------------------------------------------

// runq runs sql in a tx as role (non-superuser cnote_app = RLS enforced; "" = superuser) and drains rows.
func (s *spike) runq(role, ws, sql string) (rows, bytes int, d time.Duration, err error) {
	tx, err := s.c.Begin(s.ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(s.ctx)
	if role != "" {
		if _, err = tx.Exec(s.ctx, "SET LOCAL ROLE "+role); err != nil {
			return
		}
	}
	if _, err = tx.Exec(s.ctx, "SELECT set_config('app.workspace_id','"+ws+"',true)"); err != nil {
		return
	}
	t0 := time.Now()
	r, err := tx.Query(s.ctx, sql)
	if err != nil {
		return
	}
	for r.Next() {
		rows++
		for _, v := range r.RawValues() {
			bytes += len(v)
		}
	}
	r.Close()
	d = time.Since(t0)
	err = r.Err()
	return
}

func pct(v []float64, p float64) float64 {
	sort.Float64s(v)
	i := int(p*float64(len(v))+0.999999) - 1
	if i < 0 {
		i = 0
	}
	return v[i]
}

func (s *spike) bench(role, ws, sql string) res {
	rows, _, d0, err := s.runq(role, ws, sql) // warm-up, also decides repetitions
	if err != nil {
		return res{err: shortErr(err)}
	}
	reps := 15
	if d0 > 1500*time.Millisecond {
		reps = 5
	}
	var t []float64
	for i := 0; i < reps; i++ {
		_, _, d, _ := s.runq(role, ws, sql)
		t = append(t, ms(d))
	}
	r := res{p50: pct(t, .5), p95: pct(t, .95), rows: rows}
	r.plan, r.exec = s.explain(role, ws, sql)
	return r
}

func (s *spike) explain(role, ws, sql string) (string, float64) {
	tx, err := s.c.Begin(s.ctx)
	if err != nil {
		return "?", 0
	}
	defer tx.Rollback(s.ctx)
	if role != "" {
		tx.Exec(s.ctx, "SET LOCAL ROLE "+role)
	}
	tx.Exec(s.ctx, "SELECT set_config('app.workspace_id','"+ws+"',true)")
	var raw []byte
	if err := tx.QueryRow(s.ctx, "EXPLAIN (ANALYZE, FORMAT JSON) "+sql).Scan(&raw); err != nil {
		return "?", 0
	}
	return summarisePlan(raw)
}

// ---- main benchmark loop ---------------------------------------------------------------------

func (s *spike) runPass(pass string, strats []string, coreOnly bool) {
	s.rd[pass] = map[string]map[string]res{}
	s.anchors()
	for _, st := range strats {
		logf("%s %s: applying", pass, st)
		s.apply(pass, st)
		m := map[string]res{}
		s.rd[pass][st] = m
		for _, q := range s.queries(st) {
			if coreOnly && !q.core {
				continue
			}
			if pass == "pass2" && st == strats[0] {
				s.qord = append(s.qord, q.name)
			}
			m[q.name] = s.bench("cnote_app", W1, q.sql)
			logf("  %-18s p50 %8.1f p95 %8.1f %s %s", q.name, m[q.name].p50, m[q.name].p95, m[q.name].plan, m[q.name].err)
		}
		if !coreOnly {
			m["export_all"] = s.export()
			s.writes(st)
			s.mustExec("VACUUM ANALYZE")
		}
	}
}

func (s *spike) export() res {
	sql := fmt.Sprintf("SELECT r.id, r.data FROM spike.records r WHERE r.table_id='%s' AND r.deleted_at IS NULL ORDER BY r.created_at, r.id", TA)
	var t []float64
	rows, by := 0, 0
	for i := 0; i < 5; i++ {
		var d time.Duration
		var err error
		rows, by, d, err = s.runq("cnote_app", W1, sql)
		if err != nil {
			return res{err: shortErr(err)}
		}
		t = append(t, ms(d))
	}
	return res{p50: pct(t, .5), p95: pct(t, .95), rows: rows, plan: fmt.Sprintf("%.0f MB streamed", float64(by)/1048576)}
}

func (s *spike) anchors() {
	get := func(litExpr, key string) (string, string) {
		var lit, id string
		err := s.c.QueryRow(s.ctx, fmt.Sprintf("SELECT %s, id FROM spike.records r WHERE table_id='%s' AND deleted_at IS NULL AND workspace_id='%s' ORDER BY %s, id OFFSET 49999 LIMIT 1", litExpr, TA, W1, key)).Scan(&lit, &id)
		check(err, "anchor")
		return lit, id
	}
	s.anch.num, s.anch.nid = get("spike.jf(data,'f05')::text", "spike.jf(data,'f05')")
	s.anch.date, s.anch.did = get("quote_literal(spike.jt(data,'f07'))", "spike.jt(data,'f07')")
	s.anch.text, s.anch.tid = get("quote_literal(spike.jt(data,'f01'))", "spike.jt(data,'f01')")
	rows, err := s.c.Query(s.ctx, fmt.Sprintf("SELECT id::text FROM spike.records WHERE table_id='%s' ORDER BY created_at LIMIT 100", TB))
	check(err, "bids")
	s.bids = nil
	for rows.Next() {
		var id string
		rows.Scan(&id)
		s.bids = append(s.bids, "'"+id+"'")
	}
	rows.Close()
}
