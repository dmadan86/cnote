package reports

import (
	"encoding/json"
	"errors"

	"github.com/cnote/api/internal/command"
)

const (
	MaxBatchRuns  = 12
	MaxBatchBytes = 256 << 10 // response size; runs that would pass it come back failed, never truncated
)

// runMany answers a home screen's widgets in one request. Every submitted run is charged to the same bucket as report.run
// (no batch discount) and the batch is refused when the bucket cannot cover all of them. Each run goes through runOne
// in its own savepoint, so a refusal, a bad spec or a timeout in one cannot abort the rest.
func runMany(c *command.Ctx, input json.RawMessage) (any, error) {
	var in struct {
		Runs []struct {
			Key string `json:"key"`
			runIn
		} `json:"runs"`
	}
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	if len(in.Runs) == 0 || len(in.Runs) > MaxBatchRuns {
		return nil, command.ErrInvalid("send 1 to 12 runs")
	}
	type job struct {
		in   runIn
		keys []string
	}
	var jobs []*job
	byBody := map[string]*job{}
	seen := map[string]bool{}
	for _, r := range in.Runs {
		if n := len(r.Key); n < 1 || n > 64 || seen[r.Key] {
			return nil, command.ErrInvalid("each run needs its own key of 1 to 64 characters")
		}
		seen[r.Key] = true
		r.Format = "" // csv is not offered here
		body, _ := json.Marshal(r.runIn)
		j := byBody[string(body)]
		if j == nil {
			j = &job{in: r.runIn}
			byBody[string(body)] = j
			jobs = append(jobs, j)
		}
		j.keys = append(j.keys, r.Key)
	}
	if err := command.CheckRateN(runBucket(c), runBurst, runRefill, len(in.Runs)); err != nil { // one token per submitted run, dedup or not
		return nil, err
	}
	env, err := buildEnv(c)
	if err != nil {
		return nil, err
	}
	results := map[string]any{}
	size := 40 // the {"results":{}} wrapper
	for _, j := range jobs {
		sp, err := c.Tx.Begin(c.Context)
		if err != nil {
			return nil, command.DB(err)
		}
		sub := *c
		sub.Tx = sp
		out, err := runOne(&sub, env, j.in)
		if err == nil {
			err = sp.Commit(c.Context)
		} else {
			_ = sp.Rollback(c.Context)
		}
		entry := map[string]any{"ok": true, "result": out}
		full := map[string]any{"ok": false, "code": "failed", "message": "the batch response is full; run this one on its own"}
		if err != nil {
			entry = failure(err)
		}
		// every key carries its own copy of the entry in the response, so each one is counted
		for _, k := range j.keys {
			b, merr := command.Marshal(entry)
			if merr != nil || size+len(b)+len(k)+8 > MaxBatchBytes {
				entry = full
				b, _ = command.Marshal(entry)
			}
			size += len(b) + len(k) + 8
			results[k] = entry
		}
	}
	return map[string]any{"results": results}, nil
}

// failure maps a run's error to what a widget can show: denied (rights or finance), invalid (bad spec, period or timeout),
// not_found (a saved report that is gone or private to someone else), failed (anything else; no detail leaks).
func failure(err error) map[string]any {
	var ce *command.Error
	if errors.As(err, &ce) {
		switch ce.Code {
		case command.Forbidden:
			return map[string]any{"ok": false, "code": "denied", "message": ce.Message}
		case command.Invalid:
			return map[string]any{"ok": false, "code": "invalid", "message": ce.Message}
		case command.NotFound:
			return map[string]any{"ok": false, "code": "not_found", "message": ce.Message}
		}
	}
	return map[string]any{"ok": false, "code": "failed"}
}
