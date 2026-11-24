// Command siteeval runs the site-builder chat-loop eval harness.
//
// # Build note
//
// main.go is guarded by the "siteeval_full" build tag because it imports
// internal/modules/sites (Package A), which is written in parallel and may not
// exist yet. The assertion evaluator (assert.go) has no such dependency and can
// be tested immediately:
//
//	cd apps/api && go test ./cmd/siteeval/ -run Assert
//
// Once Package A lands, run the full harness:
//
//	go run -tags siteeval_full ./cmd/siteeval
//	    -models anthropic:claude-haiku-5-5,anthropic:claude-sonnet-5-5
//	    -cases cmd/siteeval/cases.json
//	    -prices cmd/siteeval/prices.example.json
//	    -n 1 -parallel 4 -out report.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	ai "github.com/cnote/api/internal/modules/ai"
	sites "github.com/cnote/api/internal/modules/sites"
)

// ---------- price table ----------

type priceEntry struct {
	Provider       string  `json:"provider"`
	Model          string  `json:"model"`
	InputUSD       float64 `json:"input_usd"`
	OutputUSD      float64 `json:"output_usd"`
	CacheReadRatio float64 `json:"cache_read_ratio"`
}

type priceFile struct {
	Models []priceEntry `json:"models"`
}

func loadPrices(path string) (map[string]priceEntry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pf priceFile
	if err := json.Unmarshal(raw, &pf); err != nil {
		return nil, err
	}
	m := map[string]priceEntry{}
	for _, e := range pf.Models {
		m[e.Provider+":"+e.Model] = e
	}
	return m, nil
}

// ---------- report types ----------

type failureRecord struct {
	Case   string `json:"case"`
	Reason string `json:"reason"`
	Reply  string `json:"reply"`
}

type latencyStats struct {
	P50 int64 `json:"p50"`
	P95 int64 `json:"p95"`
}

type tokenStats struct {
	In     uint64 `json:"in"`
	Out    uint64 `json:"out"`
	Cached uint64 `json:"cached"`
}

type modelReport struct {
	Model    string          `json:"model"`
	Provider string          `json:"provider"`
	Success  float64         `json:"success"`
	FirstTry float64         `json:"first_try"`
	Latency  latencyStats    `json:"latency_ms"`
	Tokens   tokenStats      `json:"tokens"`
	CostUSD  *float64        `json:"cost_usd"`
	Failures []failureRecord `json:"failures"`
}

type report struct {
	Date        string        `json:"date"`
	Cases       int           `json:"cases"`
	RunsPerCase int           `json:"runs_per_case"`
	Models      []modelReport `json:"models"`
}

// ---------- model spec ----------

// modelSpec is provider:model[-identifier].
type modelSpec struct {
	Provider string
	Model    string
}

func parseModelSpec(s string) (modelSpec, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return modelSpec{}, fmt.Errorf("model spec must be provider:model, got %q", s)
	}
	return modelSpec{Provider: parts[0], Model: parts[1]}, nil
}

// providerFor builds an ai.Provider for the given spec using environment keys.
func providerFor(spec modelSpec) ai.Provider {
	switch spec.Provider {
	case "local":
		return ai.Local{}
	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		baseURL := envPtr("ANTHROPIC_BASE_URL")
		m := spec.Model
		return ai.NewAnthropic(key, baseURL, &m)
	default: // openai-compatible (openai, sarvam, …)
		key := envPtr("OPENAI_API_KEY")
		base := envPtr("OPENAI_BASE_URL")
		m := spec.Model
		return ai.NewOpenAI(key, base, &m, nil)
	}
}

func envPtr(k string) *string {
	v := os.Getenv(k)
	if v == "" {
		return nil
	}
	return &v
}

// ---------- run one case ----------

type runResult struct {
	Success   bool
	FirstTry  bool // no invalid tool result in this turn
	LatencyMS int64
	TokensIn  uint64
	TokensOut uint64
	Cached    uint64
	Failure   *failureRecord
}

func runCase(
	ctx context.Context,
	spec modelSpec,
	c Case,
	fixture json.RawMessage,
	prov ai.Provider,
) runResult {
	model := spec.Model

	m := func(ctx context.Context, r sites.StepReq) (sites.StepRes, error) {
		req := ai.StepRequest{
			Purpose: r.Purpose,
			Model:   model,
			System:  r.System,
			Tools:   make([]ai.Tool, len(r.Tools)),
			Turns:   make([]ai.Turn, len(r.Turns)),
		}
		for i, t := range r.Tools {
			req.Tools[i] = ai.Tool{Name: t.Name, Description: t.Description, Schema: t.Schema}
		}
		for i, t := range r.Turns {
			at := ai.Turn{Role: t.Role, Text: t.Text, Raw: t.Raw}
			for _, tc := range t.Calls {
				at.Calls = append(at.Calls, ai.ToolCall{ID: tc.ID, Name: tc.Name, Input: tc.Input})
			}
			for _, tr := range t.Results {
				at.Results = append(at.Results, ai.ToolResult{CallID: tr.CallID, Content: tr.Content, IsError: tr.IsError})
			}
			req.Turns[i] = at
		}
		sr, err := prov.Step(ctx, req)
		if err != nil {
			return sites.StepRes{}, err
		}
		res := sites.StepRes{
			Text: sr.Text, Stop: sr.Stop, Provider: sr.Provider, Model: sr.Model, Raw: sr.Raw,
			TokensIn: sr.TokensIn, TokensOut: sr.TokensOut, CacheRead: sr.CacheRead,
		}
		for _, sc := range sr.Calls {
			res.Calls = append(res.Calls, sites.ToolCall{ID: sc.ID, Name: sc.Name, Input: sc.Input})
		}
		return res, nil
	}

	start := time.Now()
	in := sites.TurnInput{
		Message:  c.Prompt,
		Tier:     "routine",
		Business: c.Business,
		History:  c.History,
	}
	res := sites.RunTurn(ctx, m, fixture, in)
	latency := time.Since(start).Milliseconds()

	rr := runResult{
		LatencyMS: latency,
		TokensIn:  uint64(res.TokensIn),
		TokensOut: uint64(res.TokensOut),
		Cached:    uint64(res.CacheRead),
	}

	// Determine first_try: no step had IsError=true in its tool result
	firstTry := true
	for _, step := range res.Steps {
		if step.IsError {
			firstTry = false
			break
		}
	}
	rr.FirstTry = firstTry

	if res.Err != "" {
		rr.Failure = &failureRecord{Case: c.ID, Reason: res.Err, Reply: res.Reply}
		return rr
	}

	if err := sites.Validate(res.Data); err != nil {
		rr.Failure = &failureRecord{Case: c.ID, Reason: "validate: " + err.Error(), Reply: res.Reply}
		return rr
	}

	// Check assertions
	if ae := EvalAssertions(c.ID, res.Data, c.Expect); len(ae) > 0 {
		reasons := make([]string, len(ae))
		for i, e := range ae {
			reasons[i] = e.Error()
		}
		rr.Failure = &failureRecord{Case: c.ID, Reason: strings.Join(reasons, "; "), Reply: res.Reply}
		return rr
	}

	// Check collateral
	var after json.RawMessage
	if res.Changed {
		after = res.Data
	} else {
		after = fixture
	}
	if cd := CheckCollateral(c.ID, fixture, after, c.MayChange); len(cd) > 0 {
		rr.Failure = &failureRecord{Case: c.ID, Reason: strings.Join(cd, "; "), Reply: res.Reply}
		return rr
	}

	rr.Success = true
	return rr
}

// ---------- percentile ----------

func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

// ---------- main ----------

func main() {
	modelsFlag := flag.String("models", "local:local", "comma-separated list of provider:model")
	casesFlag := flag.String("cases", "cmd/siteeval/cases.json", "path to cases.json")
	pricesFlag := flag.String("prices", "cmd/siteeval/prices.example.json", "path to prices JSON")
	nFlag := flag.Int("n", 1, "runs per case")
	parallelFlag := flag.Int("parallel", 4, "parallel workers")
	outFlag := flag.String("out", "", "path to write report.json (stdout if empty)")
	flag.Parse()

	// Load cases
	casesRaw, err := os.ReadFile(*casesFlag)
	if err != nil {
		log.Fatalf("cases: %v", err)
	}
	var cases []Case
	if err := json.Unmarshal(casesRaw, &cases); err != nil {
		log.Fatalf("cases JSON: %v", err)
	}

	// Load fixture
	fixtureRaw, err := os.ReadFile("cmd/siteeval/fixture.json")
	if err != nil {
		// try relative to CWD (when run from cmd/siteeval/)
		fixtureRaw, err = os.ReadFile("fixture.json")
		if err != nil {
			log.Fatalf("fixture: %v", err)
		}
	}
	fixture := json.RawMessage(fixtureRaw)

	// Load prices
	prices, err := loadPrices(*pricesFlag)
	if err != nil {
		log.Printf("prices: %v (cost will be null)", err)
	}

	// Parse model specs
	var specs []modelSpec
	for _, s := range strings.Split(*modelsFlag, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		ms, err := parseModelSpec(s)
		if err != nil {
			log.Fatalf("bad model spec: %v", err)
		}
		specs = append(specs, ms)
	}

	r := report{
		Date:        time.Now().UTC().Format("2006-01-02"),
		Cases:       len(cases),
		RunsPerCase: *nFlag,
	}

	for _, spec := range specs {
		prov := providerFor(spec)
		priceKey := spec.Provider + ":" + spec.Model

		type job struct {
			c Case
			n int
		}
		jobs := make(chan job, len(cases)**nFlag)
		for _, c := range cases {
			for i := 0; i < *nFlag; i++ {
				jobs <- job{c, i}
			}
		}
		close(jobs)

		type result struct {
			c  Case
			rr runResult
		}
		results := make([]result, 0, len(cases)**nFlag)
		var mu sync.Mutex
		var wg sync.WaitGroup

		for w := 0; w < *parallelFlag; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobs {
					ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
					rr := runCase(ctx, spec, j.c, fixture, prov)
					cancel()
					mu.Lock()
					results = append(results, result{j.c, rr})
					mu.Unlock()
				}
			}()
		}
		wg.Wait()

		// Aggregate
		successes := 0
		firstTries := 0
		var latencies []int64
		var totalIn, totalOut, totalCached uint64
		var failures []failureRecord

		for _, res := range results {
			latencies = append(latencies, res.rr.LatencyMS)
			totalIn += res.rr.TokensIn
			totalOut += res.rr.TokensOut
			totalCached += res.rr.Cached
			if res.rr.Success {
				successes++
			}
			if res.rr.FirstTry {
				firstTries++
			}
			if res.rr.Failure != nil {
				failures = append(failures, *res.rr.Failure)
			}
		}

		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		total := len(results)
		if total == 0 {
			total = 1
		}

		mr := modelReport{
			Model:    spec.Model,
			Provider: spec.Provider,
			Success:  float64(successes) / float64(total),
			FirstTry: float64(firstTries) / float64(total),
			Latency: latencyStats{
				P50: percentile(latencies, 0.5),
				P95: percentile(latencies, 0.95),
			},
			Tokens: tokenStats{
				In:     totalIn,
				Out:    totalOut,
				Cached: totalCached,
			},
			Failures: failures,
		}

		// Cost calculation
		if pe, ok := prices[priceKey]; ok && (pe.InputUSD > 0 || pe.OutputUSD > 0) {
			uncached := totalIn - totalCached
			cacheReadUSD := pe.InputUSD * pe.CacheReadRatio
			cost := (float64(uncached)*pe.InputUSD +
				float64(totalCached)*cacheReadUSD +
				float64(totalOut)*pe.OutputUSD) / 1_000_000
			mr.CostUSD = &cost
		}

		r.Models = append(r.Models, mr)

		// Print table row to stderr
		cStr := "null"
		if mr.CostUSD != nil {
			cStr = fmt.Sprintf("$%.4f", *mr.CostUSD)
		}
		fmt.Fprintf(os.Stderr, "%-40s  success=%.0f%%  p50=%dms  p95=%dms  cost=%s\n",
			spec.Provider+":"+spec.Model,
			mr.Success*100, mr.Latency.P50, mr.Latency.P95, cStr)
	}

	// Marshal report
	out, _ := json.MarshalIndent(r, "", "  ")

	var w io.Writer = os.Stdout
	if *outFlag != "" {
		f, err := os.Create(*outFlag)
		if err != nil {
			log.Fatalf("open output: %v", err)
		}
		defer f.Close()
		w = f
		fmt.Fprintf(os.Stderr, "report written to %s\n", *outFlag)
	}
	fmt.Fprintln(w, string(out))
}
