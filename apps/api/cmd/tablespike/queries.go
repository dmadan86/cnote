package main

import (
	"crypto/md5"
	"fmt"
	"strings"
)

func md5Sum(b []byte) [16]byte { return md5.Sum(b) }

type query struct {
	name, sql string
	core      bool
}

const (
	numKey  = "spike.jf(r.data,'f05')"
	dateKey = "spike.jt(r.data,'f07')"
	textKey = "spike.jt(r.data,'f01')"
)

// queries returns the benchmark set for a strategy. Filters use the leakproof helpers so that
// indexes are usable under RLS; S1 uses containment (the only form GIN can serve), S4 reads sort
// keys from the typed side table and joins back to records for the page.
func (s *spike) queries(st string) []query {
	from := fmt.Sprintf("FROM spike.records r WHERE r.table_id='%s' AND r.deleted_at IS NULL", TA)
	eq := "spike.jt(r.data,'f03') = 'Blocked'"
	comb := eq + " AND spike.jf(r.data,'f05') < 20000 AND r.data OPERATOR(spike.@@>) '{\"f04\":[\"vip\"],\"f09\":false}'"
	if st == "S1 gin" {
		eq = `r.data OPERATOR(spike.@@>) '{"f03":"Blocked"}'`
		comb = `r.data OPERATOR(spike.@@>) '{"f03":"Blocked","f04":["vip"],"f09":false}' AND spike.jf(r.data,'f05') < 20000`
	}
	rare := "spike.jt(r.data,'f12') = 'Company 77'" // about 50 rows: fewer than a page, so no early exit
	if st == "S1 gin" {
		rare = `r.data OPERATOR(spike.@@>) '{"f12":"Company 77"}'`
	}
	rrng := "spike.jf(r.data,'f05') BETWEEN 50000 AND 50050" // about 50 rows
	rng := "spike.jf(r.data,'f05') BETWEEN 1000 AND 2000"
	dt := "spike.jt(r.data,'f07') >= '2025-03-01' AND spike.jt(r.data,'f07') < '2025-04-01'"
	multi := `r.data OPERATOR(spike.@@>) '{"f04":["vip"]}'`
	ilike := fmt.Sprintf("spike.jt(r.data,'f01') OPERATOR(spike.~~~) '%%%s%%'", word)
	ilikeRare := "spike.jt(r.data,'f01') OPERATOR(spike.~~~) '%customer 4217%'" // 11 rows
	def := "ORDER BY r.created_at, r.id LIMIT 100"
	page := func(pred, tail string) string {
		if pred != "" {
			pred = " AND " + pred
		}
		return "SELECT r.id, r.data " + from + pred + " " + tail
	}
	sortq := func(key string, off int) string {
		return page("", fmt.Sprintf("ORDER BY %s, r.id LIMIT 100 OFFSET %d", key, off))
	}
	keyset := func(key, lit, id string) string {
		return page(fmt.Sprintf("(%s, r.id) > (%s, '%s')", key, lit, id), fmt.Sprintf("ORDER BY %s, r.id LIMIT 100", key))
	}
	// ids first (narrow index walk), full rows only for the 100 survivors
	twostep := func(key string) string {
		return fmt.Sprintf("SELECT r.id, r.data FROM (SELECT r.id %s ORDER BY %s, r.id LIMIT 100 OFFSET 50000) p JOIN spike.records r ON r.table_id='%s' AND r.id=p.id ORDER BY %s, r.id", from, key, TA, key)
	}
	cnt := func(pred string) string {
		if pred != "" {
			pred = " AND " + pred
		}
		return "SELECT count(*) " + from + pred
	}
	a := s.anch
	q := []query{
		{"eq_select", page(eq, def), true},
		{"eq_rare", page(rare, def), true},
		{"range_rare", page(rrng, def), true},
		{"text_ilike", page(ilike, def), true},
		{"text_ilike_rare", page(ilikeRare, def), true},
		{"num_range", page(rng, def), true},
		{"date_range", page(dt, def), false},
		{"multi_contains", page(multi, def), false},
		{"combined", page(comb, def), true},
		{"filter_sort_num", page(eq, "ORDER BY "+numKey+", r.id LIMIT 100"), false},
		{"sort_num_0", sortq(numKey, 0), false},
		{"sort_num_50k", sortq(numKey, 50000), true},
		{"sort_date_0", sortq(dateKey, 0), false},
		{"sort_date_50k", sortq(dateKey, 50000), false},
		{"sort_num_50k_2step", twostep(numKey), false},
		{"sort_text_50k_2step", twostep(textKey), false},
		{"sort_text_0", sortq(textKey, 0), false},
		{"sort_text_50k", sortq(textKey, 50000), false},
		{"keyset_num_50k", keyset(numKey, a.num, a.nid), false},
		{"keyset_date_50k", keyset(dateKey, a.date, a.did), false},
		{"keyset_text_50k", keyset(textKey, a.text, a.tid), false},
		{"count_eq", cnt(eq), true},
		{"count_all", cnt(""), false},
		{"count_combined", cnt(comb), false},
		{"group_by_select", fmt.Sprintf("SELECT spike.jt(r.data,'f03'), count(*) %s GROUP BY 1", from), false},
		{"link_page", fmt.Sprintf(`WITH p AS (SELECT r.id %s %s)
SELECT p.id, count(l.to_record), array_agg(spike.jt(b.data,'f01') ORDER BY l.to_record)
FROM p LEFT JOIN spike.links l ON l.from_record=p.id AND l.field_id='f20'
LEFT JOIN spike.records b ON b.table_id='%s' AND b.id=l.to_record GROUP BY p.id`, from, def, TB), false},
		{"link_filter", page(fmt.Sprintf("r.id IN (SELECT from_record FROM spike.links WHERE to_record=%s AND field_id='f20')", s.bids[0]), def), false},
		{"link_backcount", fmt.Sprintf("SELECT to_record, count(*) FROM spike.links WHERE field_id='f20' AND to_record IN (%s) GROUP BY 1", strings.Join(s.bids, ",")), false},
	}
	if st != "S4 side" {
		return q
	}
	// S4: typed side table k (n05 float8, d07 date, s03 / t01 text); records only fetched for the page.
	kpage := func(where, order string, off int, lit string) string {
		if lit != "" {
			where += " AND " + lit
		}
		return fmt.Sprintf(`SELECT r.id, r.data FROM (SELECT k.id, k.n05, k.d07, k.t01, k.s03, k.created_at FROM spike.sk k WHERE k.table_id='%s' %s ORDER BY %s LIMIT 100 OFFSET %d) p
JOIN spike.records r ON r.table_id='%s' AND r.id=p.id ORDER BY %s`, TA, where, order, off, TA, strings.ReplaceAll(order, "k.", "p."))
	}
	kc := func(w string) string {
		return fmt.Sprintf("SELECT count(*) FROM spike.sk k WHERE k.table_id='%s' %s", TA, w)
	}
	over := map[string]string{
		"eq_select":       kpage("AND k.s03='Blocked'", "k.created_at, k.id", 0, ""),
		"range_rare":      kpage("AND k.n05 BETWEEN 50000 AND 50050", "k.created_at, k.id", 0, ""),
		"num_range":       kpage("AND k.n05 BETWEEN 1000 AND 2000", "k.created_at, k.id", 0, ""),
		"date_range":      kpage("AND k.d07 >= '2025-03-01' AND k.d07 < '2025-04-01'", "k.created_at, k.id", 0, ""),
		"filter_sort_num": kpage("AND k.s03='Blocked'", "k.n05, k.id", 0, ""),
		"sort_num_0":      kpage("", "k.n05, k.id", 0, ""),
		"sort_num_50k":    kpage("", "k.n05, k.id", 50000, ""),
		"sort_date_0":     kpage("", "k.d07, k.id", 0, ""),
		"sort_date_50k":   kpage("", "k.d07, k.id", 50000, ""),
		"sort_text_0":     kpage("", "k.t01, k.id", 0, ""),
		"sort_text_50k":   kpage("", "k.t01, k.id", 50000, ""),
		"keyset_num_50k":  kpage("", "k.n05, k.id", 0, fmt.Sprintf("(k.n05, k.id) > (%s, '%s')", a.num, a.nid)),
		"keyset_date_50k": kpage("", "k.d07, k.id", 0, fmt.Sprintf("(k.d07, k.id) > (%s, '%s')", a.date, a.did)),
		"keyset_text_50k": kpage("", "k.t01, k.id", 0, fmt.Sprintf("(k.t01, k.id) > (%s, '%s')", a.text, a.tid)),
		"count_eq":        kc("AND k.s03='Blocked'"),
		"count_all":       kc(""),
		"group_by_select": fmt.Sprintf("SELECT k.s03, count(*) FROM spike.sk k WHERE k.table_id='%s' GROUP BY 1", TA),
	}
	for i := range q {
		if v, ok := over[q[i].name]; ok {
			q[i].sql = v
		}
	}
	return q
}
