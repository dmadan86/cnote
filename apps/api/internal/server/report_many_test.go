package server_test

import (
	"strconv"
	"strings"
	"testing"
)

// report.run_many: a batch of report.run calls. Same checks and same rate bucket as report.run, one savepoint per run.

func runs(specs ...any) map[string]any {
	rs := make([]any, len(specs))
	for i, s := range specs {
		run := map[string]any{"key": "k" + strconv.Itoa(i)}
		if m, ok := s.(map[string]any); ok && (m["id"] != nil || m["spec"] != nil) {
			for k, v := range m {
				run[k] = v
			}
		} else {
			run["spec"] = s
		}
		rs[i] = run
	}
	return map[string]any{"runs": rs}
}

func many(t *testing.T, r *rb, w who, in map[string]any) map[string]any {
	t.Helper()
	return r.okAs(w, "report.run_many", in)["results"].(map[string]any)
}

func entry(t *testing.T, res map[string]any, key string) map[string]any {
	t.Helper()
	m, ok := res[key].(map[string]any)
	if !ok {
		t.Fatalf("no result for %s: %v", key, res)
	}
	return m
}

func TestRunManyMatchesRunAndIsolatesFailures(t *testing.T) {
	r := newRB(t)
	r.invoice(r.c1, 0)
	r.ok("deal.create", map[string]any{"space_id": r.crm, "title": "D1", "amount_minor": 10_000, "stage": "proposal"})
	byStage := map[string]any{"dataset": "deals", "group_by": []any{map[string]any{"field": "stage"}}}
	sales := invSpec(map[string]any{"group_by": []any{map[string]any{"field": "status"}}, "measures": []any{map[string]any{"field": "total", "agg": "sum"}}})
	mine := r.okAs(r.member, "report.save", map[string]any{"name": "Mine", "spec": byStage, "visibility": "private"})["report"].(map[string]any)["id"]
	shared := r.ok("report.save", map[string]any{"name": "Shared", "spec": byStage, "visibility": "workspace"})["report"].(map[string]any)["id"]

	in := runs(byStage, sales, map[string]any{"dataset": "nope"}, map[string]any{"id": "00000000-0000-4000-8000-000000000001"}, map[string]any{"id": mine}, map[string]any{"id": shared},
		map[string]any{"dataset": "deals", "filters": []any{map[string]any{"field": "ghost", "op": "eq", "value": 1}}}, map[string]any{})
	// owner: the same answer as report.run, and every kind of failure stays in its own slot
	res := many(t, r, r.owner, in)
	eq(t, entry(t, res, "k0")["ok"], true)
	eq(t, entry(t, res, "k0")["result"], any(r.run(r.owner, map[string]any{"spec": byStage})))
	eq(t, entry(t, res, "k1")["result"], any(r.run(r.owner, map[string]any{"spec": sales})))
	for k, code := range map[string]string{"k2": "invalid", "k3": "not_found", "k4": "not_found", "k6": "invalid", "k7": "invalid"} {
		e := entry(t, res, k)
		eq(t, e["ok"], false)
		eq(t, e["code"], code)
	}
	eq(t, entry(t, res, "k5")["ok"], true) // a workspace-visible saved report by id

	// without finance access only the finance run is denied; the rest of the batch is unharmed
	res = many(t, r, r.noFin, runs(byStage, sales))
	eq(t, entry(t, res, "k0")["ok"], true)
	eq(t, entry(t, res, "k1")["ok"], false)
	eq(t, entry(t, res, "k1")["code"], "denied")
	if _, leak := entry(t, res, "k1")["result"]; leak {
		t.Fatal("denied run carries a result")
	}
	// every position of a finance spec is denied in a batch exactly as in report.run
	for _, s := range map[string]map[string]any{
		"columns":  invSpec(map[string]any{"columns": []string{"number", "total"}}),
		"filter":   invSpec(map[string]any{"filters": []any{map[string]any{"field": "total", "op": "gt", "value": 1}}}),
		"period":   invSpec(map[string]any{"period": map[string]any{"field": "invoice_date", "preset": "this_fy"}}),
		"payments": {"dataset": "payments"},
	} {
		eq(t, entry(t, many(t, r, r.noFin, runs(s)), "k0")["code"], "denied")
	}
	// a saved report's visibility is the caller's: Mia cannot reach the owner's private one by id
	priv := r.ok("report.save", map[string]any{"name": "Owner private", "spec": byStage, "visibility": "private"})["report"].(map[string]any)["id"]
	eq(t, entry(t, many(t, r, r.member, runs(map[string]any{"id": priv})), "k0")["code"], "not_found")

	// a run that times out or breaks leaves the transaction usable for the next one
	res = many(t, r, r.owner, runs(map[string]any{"dataset": "deals", "group_by": []any{map[string]any{"field": "created_on", "bucket": "nope"}}}, byStage))
	eq(t, entry(t, res, "k0")["ok"], false)
	eq(t, entry(t, res, "k1")["ok"], true)

	// guests never run reports
	r.errAs(r.guest, "report.run_many", runs(byStage), 403, "forbidden")
}

func TestRunManyInputLimits(t *testing.T) {
	r := newRB(t)
	spec := map[string]any{"dataset": "deals"}
	n := func(k int) map[string]any {
		rs := make([]any, k)
		for i := range rs {
			rs[i] = map[string]any{"key": "k" + strconv.Itoa(i), "spec": map[string]any{"dataset": "deals", "limit": i + 1}}
		}
		return map[string]any{"runs": rs}
	}
	r.errAs(r.owner, "report.run_many", map[string]any{}, 400, "invalid")
	r.errAs(r.owner, "report.run_many", map[string]any{"runs": []any{}}, 400, "invalid")
	r.errAs(r.owner, "report.run_many", n(13), 400, "invalid")
	eq(t, len(many(t, r, r.owner, n(12))), 12)
	r.errAs(r.owner, "report.run_many", map[string]any{"runs": []any{map[string]any{"spec": spec}}}, 400, "invalid")
	r.errAs(r.owner, "report.run_many", map[string]any{"runs": []any{map[string]any{"key": "a", "spec": spec}, map[string]any{"key": "a", "spec": spec}}}, 400, "invalid")
	r.errAs(r.owner, "report.run_many", map[string]any{"runs": []any{map[string]any{"key": strings.Repeat("k", 65), "spec": spec}}}, 400, "invalid")
}

func TestRunManyChargesLikeRunAndRefusesWhatItCannotCover(t *testing.T) {
	r := newRB(t)
	distinct := func(k, off int) map[string]any {
		rs := make([]any, k)
		for i := range rs {
			rs[i] = map[string]any{"key": "k" + strconv.Itoa(i), "spec": map[string]any{"dataset": "deals", "limit": off + i + 1}}
		}
		return map[string]any{"runs": rs}
	}
	w := r.member                                                         // own bucket: workspace and person
	many(t, r, w, distinct(12, 0))                                        // 20 -> 8 tokens
	r.errAs(w, "report.run_many", distinct(12, 100), 429, "rate_limited") // cannot cover 12: refused whole, nothing taken
	many(t, r, w, distinct(8, 200))                                       // the 8 left are still there (the refusal charged nothing)
	r.errAs(w, "report.run_many", distinct(12, 300), 429, "rate_limited")
	// report.run draws on the same bucket: after 20 spent, the refill alone (2 a second) leaves at most a few
	spent := 0
	for i := 0; i < 8; i++ {
		if r.call(w, "report.run", map[string]any{"spec": map[string]any{"dataset": "deals"}}).status == 429 {
			spent++
		}
	}
	if spent == 0 {
		t.Fatal("report.run and report.run_many must share one bucket")
	}

	// identical runs execute once but every submitted run costs a token (no batch discount): 12 fit, the next 12 do not
	same := make([]any, 12)
	for i := range same {
		same[i] = map[string]any{"key": "s" + strconv.Itoa(i), "spec": map[string]any{"dataset": "tasks"}}
	}
	res := many(t, r, r.noFin, map[string]any{"runs": same})
	eq(t, len(res), 12)
	eq(t, entry(t, res, "s11")["ok"], true)
	r.errAs(r.noFin, "report.run_many", map[string]any{"runs": same}, 429, "rate_limited")
	// someone else's bucket is untouched by all of the above
	many(t, r, r.owner, distinct(12, 0))
}

func TestRunManyResponseIsCapped(t *testing.T) {
	r := newRB(t)
	title := strings.Repeat("x", 200)
	for i := 0; i < 120; i++ {
		r.ok("deal.create", map[string]any{"space_id": r.crm, "title": title + strconv.Itoa(i), "amount_minor": 100})
	}
	rs := make([]any, 12)
	for i := range rs {
		rs[i] = map[string]any{"key": "k" + strconv.Itoa(i), "spec": map[string]any{"dataset": "deals", "columns": []string{"title", "stage", "amount"}, "limit": 120 - i}}
	}
	raw := r.call(r.owner, "report.run_many", map[string]any{"runs": rs})
	if raw.status != 200 {
		t.Fatalf("%d %s", raw.status, raw.body)
	}
	if len(raw.body) > 270<<10 {
		t.Fatalf("response is %d bytes", len(raw.body))
	}
	ok, full := 0, 0
	for _, v := range raw.json()["results"].(map[string]any) {
		e := v.(map[string]any)
		if e["ok"] == true {
			ok++
		} else if e["code"] == "failed" {
			full++
		}
	}
	if ok == 0 || full == 0 || ok+full != 12 {
		t.Fatalf("ok %d, cut %d: want some of each and 12 in all", ok, full)
	}
}

// Identical runs run once but each key gets its own copy in the response: every copy counts toward the cap.
func TestRunManyCapCountsDuplicateKeys(t *testing.T) {
	r := newRB(t)
	title := strings.Repeat("x", 200)
	for i := 0; i < 120; i++ {
		r.ok("deal.create", map[string]any{"space_id": r.crm, "title": title + strconv.Itoa(i), "amount_minor": 100})
	}
	rs := make([]any, 12)
	for i := range rs {
		rs[i] = map[string]any{"key": "k" + strconv.Itoa(i), "spec": map[string]any{"dataset": "deals", "columns": []string{"title", "stage", "amount"}, "limit": 120}}
	}
	raw := r.call(r.owner, "report.run_many", map[string]any{"runs": rs})
	if raw.status != 200 {
		t.Fatalf("%d %s", raw.status, raw.body)
	}
	if len(raw.body) > 256<<10 {
		t.Fatalf("response is %d bytes, over the 256 KB cap", len(raw.body))
	}
	ok, full := 0, 0
	for _, v := range raw.json()["results"].(map[string]any) {
		e := v.(map[string]any)
		if e["ok"] == true {
			ok++
		} else if e["code"] == "failed" && e["message"] != nil {
			full++
		}
	}
	if ok == 0 || full == 0 || ok+full != 12 {
		t.Fatalf("ok %d, cut %d: want some of each and 12 in all", ok, full)
	}
}
