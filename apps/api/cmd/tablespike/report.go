package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

func summarisePlan(raw []byte) (string, float64) {
	var p []struct {
		Plan map[string]any `json:"Plan"`
		Exec float64        `json:"Execution Time"`
	}
	if json.Unmarshal(raw, &p) != nil || len(p) == 0 {
		return "?", 0
	}
	seen := map[string]bool{}
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		t, _ := n["Node Type"].(string)
		switch {
		case strings.Contains(t, "Seq Scan"):
			seen["seq"] = true
		case strings.Contains(t, "Index"):
			name, _ := n["Index Name"].(string)
			if strings.Contains(t, "Only") {
				name += "(only)"
			}
			seen[strings.TrimPrefix(name, "ix_")] = true
		case t == "Gather" || t == "Gather Merge":
			seen["par"] = true
		}
		if ch, ok := n["Plans"].([]any); ok {
			for _, c := range ch {
				walk(c.(map[string]any))
			}
		}
	}
	walk(p[0].Plan)
	var k []string
	for x := range seen {
		k = append(k, x)
	}
	sort.Strings(k)
	return strings.Join(k, "+"), p[0].Exec
}

func (s *spike) w(f string, a ...any) { fmt.Fprintf(&s.out, f+"\n", a...) }

func (s *spike) header() {
	s.w("# Tables spike raw results\n")
	s.w("Generated %s by apps/api/cmd/tablespike. Cells: `p50 / p95 ms [plan nodes]`; seq = sequential scan, par = parallel gather,", time.Now().Format("2006-01-02 15:04"))
	s.w("other names are the index used. Times are client-side round trips including draining all rows, inside one tx as role cnote_app (RLS enforced).\n")
}

func (s *spike) table(pass string, strats []string, order []string) {
	s.w("| query | %s |", strings.Join(strats, " | "))
	s.w("|---|%s", strings.Repeat("---|", len(strats)))
	for _, q := range order {
		var cells []string
		any := false
		for _, st := range strats {
			r, ok := s.rd[pass][st][q]
			switch {
			case !ok:
				cells = append(cells, "-")
			case r.err != "":
				any = true
				cells = append(cells, "ERR "+r.err)
			default:
				any = true
				cells = append(cells, fmt.Sprintf("%.1f / %.1f [%s]", r.p50, r.p95, r.plan))
			}
		}
		if any {
			s.w("| %s | %s |", q, strings.Join(cells, " | "))
		}
	}
	s.w("")
}

func (s *spike) report() {
	order := append(append([]string{}, s.qord...), "export_all")
	s.w("## Pass 1: no noisy neighbour (110k + 10k rows in the relation)\n")
	s.table("pass1", []string{"S0 none", "S3 hybrid"}, order)
	s.w("## Pass 2: with the 500k-row neighbour (about 620k rows in the relation)\n")
	s.table("pass2", strategies, order)
	s.w("## Index and side-table builds\n")
	s.w("| pass | strategy | object | build ms | size MB |\n|---|---|---|---|---|")
	for _, b := range s.build {
		s.w("| %s | %s | %s | %.0f | %.1f |", b.pass, b.strat, b.idx, b.ms, b.mb)
	}
	s.w("\n## Writes (pass 2)\n")
	s.w("| strategy | op | p50 ms (warm) | p95 ms (warm) | WAL bytes/row warm | WAL bytes/row after checkpoint | HOT update %% |\n|---|---|---|---|---|---|---|")
	for _, l := range s.wlines {
		s.w("%s", l)
	}
	s.w("\n## Sizes (pass 2, after load)\n")
	for _, l := range s.sizes {
		s.w("- %s", l)
	}
	s.w("\n## RLS and index usability (raw expressions vs leakproof helpers)\n")
	for _, l := range s.raw {
		s.w("%s", l)
	}
}
