// Command loadtest drives a realistic mix (note edits, command calls, invoice creation, search) at a fixed
// arrival rate against a running C Note API (the Go front) and fails the run when a threshold is broken.
// CN-QA-001 performance section; single machine, standard library only. See docs/go-migration.md.
//
//	loadtest -api http://localhost:4670 -rate 100 -duration 60s -p95 250ms -p99 600ms -max-errors 0.01
//
// Users and workspaces are created through the API (signup); passwords are random per run.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	mrand "math/rand/v2"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cnote/api/internal/modules/billing"
)

type tenant struct {
	ws, cookie, space, contact string
	notes                      []string
}

type sample struct {
	d  time.Duration
	ok bool
}

var client = &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 256}}

func rnd() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func post(api, path, cookie string, body any) ([]byte, *http.Response, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", api+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.Header.Set("Cookie", "cn_session="+cookie)
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return out, res, nil
}

func cmd(api string, t *tenant, name string, in any) (map[string]any, error) {
	out, res, err := post(api, "/api/w/"+t.ws+"/commands/"+name, t.cookie, in)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %d %s", name, res.StatusCode, strings.TrimSpace(string(out)))
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m) // list results are not objects; callers that need a field check it
	return m, nil
}

func setup(api string, noteCount int) (*tenant, error) {
	out, res, err := post(api, "/api/auth/signup", "", map[string]any{"email": "load-" + rnd() + "@example.test", "name": "Load", "password": rnd() + rnd(), "workspace_name": "Load WS"})
	if err != nil {
		return nil, err
	}
	var su struct {
		WorkspaceID string `json:"workspace_id"`
	}
	_ = json.Unmarshal(out, &su)
	t := &tenant{ws: su.WorkspaceID}
	for _, c := range res.Cookies() {
		if c.Name == "cn_session" {
			t.cookie = c.Value
		}
	}
	if res.StatusCode != 200 || t.cookie == "" || t.ws == "" {
		return nil, fmt.Errorf("signup: %d %s", res.StatusCode, out)
	}
	if _, err = cmd(api, t, "workspace.update", map[string]any{"settings": map[string]any{"gstin": billing.CompleteGSTIN("29ABCDE1234F1Z"), "state_code": "29"}}); err != nil {
		return nil, err
	}
	sp, err := cmd(api, t, "space.create", map[string]any{"key": "LOAD", "name": "Load"})
	if err != nil {
		return nil, err
	}
	t.space, _ = sp["id"].(string)
	ct, err := cmd(api, t, "contact.create", map[string]any{"space_id": t.space, "name": "Buyer", "kind": "company", "gstin": billing.CompleteGSTIN("29AABCT1332L1Z")})
	if err != nil {
		return nil, err
	}
	t.contact, _ = ct["id"].(string)
	for i := range noteCount {
		n, err := cmd(api, t, "note.create", map[string]any{"space_id": t.space, "title": fmt.Sprintf("Load note %d", i), "content_md": "seed zebra quarterly report"})
		if err != nil {
			return nil, err
		}
		t.notes = append(t.notes, n["id"].(string))
	}
	return t, nil
}

var ops = map[string]func(api string, t *tenant) error{
	"edit": func(api string, t *tenant) error {
		_, err := cmd(api, t, "note.update", map[string]any{"id": t.notes[mrand.IntN(len(t.notes))], "content_md": "edit " + rnd() + " zebra quarterly report"})
		return err
	},
	"command": func(api string, t *tenant) error {
		if mrand.IntN(2) == 0 {
			_, err := cmd(api, t, "space.list", map[string]any{})
			return err
		}
		_, err := cmd(api, t, "note.get", map[string]any{"id": t.notes[mrand.IntN(len(t.notes))]})
		return err
	},
	"invoice": func(api string, t *tenant) error {
		_, err := cmd(api, t, "invoice.create_draft", map[string]any{"space_id": t.space, "contact_id": t.contact, "invoice_date": time.Now().UTC().Format("2006-01-02"),
			"lines": []any{map[string]any{"description": "Consulting", "hsn_sac": "998314", "quantity": 2, "unit_price_minor": 40_000}}})
		return err
	},
	"search": func(api string, t *tenant) error {
		_, err := cmd(api, t, "search.notes", map[string]any{"q": "zebra"})
		return err
	},
}

func parseMix(s string) (names []string, cum []int, err error) {
	total := 0
	for _, kv := range strings.Split(s, ",") {
		var name string
		var w int
		if _, e := fmt.Sscanf(strings.Replace(kv, "=", " ", 1), "%s %d", &name, &w); e != nil || ops[name] == nil || w <= 0 {
			return nil, nil, fmt.Errorf("bad mix entry %q (ops: edit, command, invoice, search)", kv)
		}
		total += w
		names, cum = append(names, name), append(cum, total)
	}
	return
}

func pct(s []time.Duration, p float64) time.Duration {
	if len(s) == 0 {
		return 0
	}
	return s[min(len(s)-1, int(math.Ceil(p*float64(len(s))))-1)]
}

func main() {
	api := flag.String("api", "http://localhost:4670", "base URL of the Go API")
	rate := flag.Float64("rate", 50, "target arrival rate, requests per second (open loop)")
	dur := flag.Duration("duration", 30*time.Second, "measured duration")
	warmup := flag.Duration("warmup", 3*time.Second, "extra unmeasured run-in first (cold pools and caches)")
	mixS := flag.String("mix", "edit=50,command=25,search=15,invoice=10", "operation weights")
	users := flag.Int("users", 0, "workspaces to spread load over (default rate/10; the fair-use limit is per seat)")
	inflight := flag.Int("max-inflight", 512, "arrivals beyond this many in-flight requests count as errors (the server is not keeping up)")
	p50t := flag.Duration("p50", 0, "fail when any operation's p50 exceeds this (0 = no check)")
	p95t := flag.Duration("p95", 300*time.Millisecond, "fail when any operation's p95 exceeds this")
	p99t := flag.Duration("p99", time.Second, "fail when any operation's p99 exceeds this")
	maxErr := flag.Float64("max-errors", 0.01, "fail when the overall error rate exceeds this fraction")
	flag.Parse()
	*api = strings.TrimRight(*api, "/")
	names, cum, err := parseMix(*mixS)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if *users <= 0 {
		*users = max(1, int(math.Ceil(*rate/10)))
	}
	if *rate <= 0 || *dur <= 0 {
		fmt.Fprintln(os.Stderr, "rate and duration must be positive")
		os.Exit(2)
	}

	tenants := make([]*tenant, *users)
	for i := range tenants {
		if tenants[i], err = setup(*api, 20); err != nil {
			fmt.Fprintln(os.Stderr, "setup:", err)
			os.Exit(2)
		}
	}

	var (
		mu      sync.Mutex
		samples = map[string][]sample{}
		wg      sync.WaitGroup
		active  atomic.Int64
		dropped atomic.Int64
		sample1 sync.Map // first error text per operation
	)
	fire := func(op string, t *tenant, measured bool) {
		defer wg.Done()
		defer active.Add(-1)
		start := time.Now()
		err := ops[op](*api, t)
		d := time.Since(start)
		if !measured {
			return
		}
		if err != nil {
			sample1.LoadOrStore(op, err.Error())
		}
		mu.Lock()
		samples[op] = append(samples[op], sample{d, err == nil})
		mu.Unlock()
	}
	begin := time.Now()
	for i := 0; ; i++ {
		due := begin.Add(time.Duration(float64(i) / *rate * float64(time.Second)))
		if due.Sub(begin) >= *warmup+*dur {
			break
		}
		time.Sleep(time.Until(due))
		r := mrand.IntN(cum[len(cum)-1])
		op := names[sort.SearchInts(cum, r+1)]
		if active.Load() >= int64(*inflight) {
			if due.Sub(begin) >= *warmup {
				dropped.Add(1)
			}
			continue
		}
		active.Add(1)
		wg.Add(1)
		go fire(op, tenants[i%len(tenants)], due.Sub(begin) >= *warmup)
	}
	wg.Wait()
	elapsed := time.Since(begin) - *warmup

	fails := []string{}
	var total, bad int
	fmt.Printf("target %.0f req/s for %s over %d workspaces (mix %s)\n", *rate, *dur, *users, *mixS)
	fmt.Printf("%-8s %7s %6s %9s %9s %9s %9s\n", "op", "count", "err%", "p50", "p95", "p99", "max")
	for _, op := range names {
		ss := samples[op]
		if len(ss) == 0 {
			continue
		}
		ds := make([]time.Duration, 0, len(ss))
		nbad := 0
		for _, s := range ss {
			if s.ok {
				ds = append(ds, s.d)
			} else {
				nbad++
			}
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		total, bad = total+len(ss), bad+nbad
		fmt.Printf("%-8s %7d %5.2f%% %9s %9s %9s %9s\n", op, len(ss), 100*float64(nbad)/float64(len(ss)),
			pct(ds, .5).Round(time.Microsecond), pct(ds, .95).Round(time.Microsecond), pct(ds, .99).Round(time.Microsecond), pct(ds, 1).Round(time.Microsecond))
		for _, c := range []struct {
			name string
			got  time.Duration
			lim  time.Duration
		}{{"p50", pct(ds, .5), *p50t}, {"p95", pct(ds, .95), *p95t}, {"p99", pct(ds, .99), *p99t}} {
			if c.lim > 0 && c.got > c.lim {
				fails = append(fails, fmt.Sprintf("%s %s %s > %s", op, c.name, c.got.Round(time.Microsecond), c.lim))
			}
		}
		if v, ok := sample1.Load(op); ok {
			fmt.Printf("         first error: %v\n", v)
		}
	}
	bad += int(dropped.Load())
	total += int(dropped.Load())
	errRate := float64(bad) / math.Max(1, float64(total))
	fmt.Printf("total %d requests, %d errors (%.2f%%, %d dropped at max-inflight), achieved %.1f req/s\n", total, bad, 100*errRate, dropped.Load(), float64(total)/elapsed.Seconds())
	if errRate > *maxErr {
		fails = append(fails, fmt.Sprintf("error rate %.2f%% > %.2f%%", 100*errRate, 100**maxErr))
	}
	if total == 0 {
		fails = append(fails, "no requests were sent")
	}
	if len(fails) > 0 {
		fmt.Println("FAIL:\n  " + strings.Join(fails, "\n  "))
		os.Exit(1)
	}
	fmt.Println("PASS")
}
