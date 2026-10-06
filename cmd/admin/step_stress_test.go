package main

// TestHTTPStepStress runs an HTTP staircase load test directly against an
// in-process httptest.Server. It increases concurrent workers at each step
// and measures: RPS achieved, latency percentiles, and error rate.
// The test stops (and marks the last clean step) when error_rate > 1%.
//
// Run with:
//
//	go test -v -run TestHTTPStepStress -count=1 -timeout=300s ./cmd/admin/

import (
	"bytes"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/policy"
	"github.com/RuleEngineLabs/engine/internal/ratelimit"
	"github.com/RuleEngineLabs/engine/internal/store"
)

// concurrency levels to test — fine-grained around the knee, then stress to break point.
// Goal: find the highest concurrency where p99 is still acceptable (< 200ms).
var stressSteps = []int{1, 5, 10, 15, 20, 30, 50, 75, 100, 150, 200, 300, 500}

const stepDuration = 6 * time.Second // hold time per step
const errorRateLimit = 0.01          // abort step if error_rate > 1%
const p99SLO = 200 * time.Millisecond // target p99 for "acceptable latency"

type httpStepResult struct {
	concurrency  int
	totalReqs    int64
	errors       int64
	rps          float64
	errorRate    float64
	p50, p90     time.Duration
	p99, p999    time.Duration
	pMax         time.Duration
	mean, stddev float64
	broken       bool // this step exceeded error threshold
}

func TestHTTPStepStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping HTTP stress test in -short mode; run with -timeout=300s without -short")
	}
	// ── Build in-process server ───────────────────────────────────────────
	ps := store.New()

	creditPol := &policy.Policy{
		ID:    "stress-credit",
		Entry: "eval",
		States: []policy.State{
			{
				ID:   "eval",
				Kind: policy.KindExecution,
				Transitions: []policy.Transition{
					{When: "input.score >= 700 && input.amount <= 100000", To: "approved"},
					{When: "input.score >= 700 && input.amount > 100000", To: "manual"},
					{When: "true", To: "rejected"},
				},
			},
			{ID: "approved", Kind: policy.KindResponse, Status: 200, Data: map[string]any{"decision": "approved"}},
			{ID: "manual", Kind: policy.KindResponse, Status: 200, Data: map[string]any{"decision": "manual_review"}},
			{ID: "rejected", Kind: policy.KindResponse, Status: 200, Data: map[string]any{"decision": "rejected"}},
		},
	}

	art, err := compiler.Compile(creditPol)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	policyID, _, err := ps.Create("stress-credit", "team-stress", art)
	if err != nil {
		t.Fatalf("create policy: %v", err)
	}

	rl := ratelimit.New(time.Second, 10_000) // high limit — don't constrain load test
	mux := newAdminMux(ps, rl)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	executeURL := srv.URL + "/execute/" + policyID

	// Shared HTTP client — connection pool sized per step
	baseClient := &http.Client{
		Transport: &http.Transport{
			MaxIdleConnsPerHost:   1200,
			MaxConnsPerHost:       1200,
			IdleConnTimeout:       90 * time.Second,
			DisableCompression:    true,
			ForceAttemptHTTP2:     false,
		},
		Timeout: 5 * time.Second,
	}

	// Three inputs rotated across workers
	inputs := [][]byte{
		[]byte(`{"score":750,"amount":50000}`),
		[]byte(`{"score":750,"amount":200000}`),
		[]byte(`{"score":400,"amount":5000}`),
	}

	results := make([]httpStepResult, 0, len(stressSteps))
	broken := false

	// Print live header
	fmt.Printf("\n%-8s  %10s  %10s  %8s  %8s  %8s  %8s  %8s\n",
		"Workers", "RPS", "Err%", "p50", "p90", "p99", "p99.9", "max")
	fmt.Println(strings.Repeat("─", 80))

	for _, concurrency := range stressSteps {
		r := runHTTPStep(baseClient, executeURL, inputs, concurrency, stepDuration)
		results = append(results, r)

		statusMark := "✓"
		if r.broken {
			statusMark = "✗"
		}
		fmt.Printf("%-8d  %10s  %9.3f%%  %8s  %8s  %8s  %8s  %8s  %s\n",
			concurrency,
			fmtRPS2(r.rps),
			r.errorRate*100,
			r.p50, r.p90, r.p99, r.p999, r.pMax,
			statusMark)

		t.Logf("step c=%d rps=%.0f err=%.3f%% p50=%v p90=%v p99=%v p99.9=%v max=%v",
			concurrency, r.rps, r.errorRate*100, r.p50, r.p90, r.p99, r.p999, r.pMax)

		// Write one progress line per step so external monitors can tail it
		writeProgressLine(fmt.Sprintf("[step %d/%d] c=%-4d rps=%s err=%.3f%% p50=%v p99=%v %s",
			len(results), len(stressSteps), concurrency,
			fmtRPS2(r.rps), r.errorRate*100, r.p50, r.p99, statusMark))

		if r.broken {
			broken = true
			t.Logf("BREAK POINT at concurrency=%d — error rate %.2f%% > 1%%", concurrency, r.errorRate*100)
			break
		}
	}
	fmt.Println(strings.Repeat("─", 80))

	// Find ceiling (last clean step) and SLO knee (last step within p99SLO)
	var ceiling *httpStepResult
	var knee *httpStepResult
	for i := range results {
		if !results[i].broken {
			ceiling = &results[i]
			if results[i].p99 <= p99SLO {
				knee = &results[i]
			}
		}
	}

	fmt.Println()
	if knee != nil {
		fmt.Printf("Optimal point (p99 ≤ %v): %s RPS @ %d workers (p99=%v)\n",
			p99SLO, fmtRPS2(knee.rps), knee.concurrency, knee.p99)
	}
	if ceiling != nil {
		fmt.Printf("Sustainable ceiling:      %s RPS @ %d workers (p99=%v)\n",
			fmtRPS2(ceiling.rps), ceiling.concurrency, ceiling.p99)
	}
	if broken {
		last := results[len(results)-1]
		fmt.Printf("Break point:              %d workers — error rate %.2f%%\n",
			last.concurrency, last.errorRate*100)
	} else {
		fmt.Println("All steps passed — server did not break within tested range.")
	}
	fmt.Println()

	// Write Markdown report
	writeStressReport(t, results, broken, ceiling, knee)
}

func runHTTPStep(client *http.Client, url string, inputs [][]byte, concurrency int, dur time.Duration) httpStepResult {
	var (
		totalReqs int64
		errors    int64
		latencies sync.Mutex
		samples   []time.Duration
	)

	// Pre-warm: 1 request before measurement
	_, _ = client.Post(url, "application/json", bytes.NewReader(inputs[0]))

	var stopFlag int32
	var wg sync.WaitGroup

	for w := 0; w < concurrency; w++ {
		idx := w % len(inputs)
		body := inputs[idx]
		wg.Add(1)
		go func() {
			defer wg.Done()
			var localSamples []time.Duration
			var localErrors int64
			var localTotal int64

			for atomic.LoadInt32(&stopFlag) == 0 {
				t0 := time.Now()
				resp, err := client.Post(url, "application/json", bytes.NewReader(body))
				elapsed := time.Since(t0)

				localTotal++
				if err != nil || resp.StatusCode != http.StatusOK {
					localErrors++
				}
				if err == nil {
					resp.Body.Close()
				}
				localSamples = append(localSamples, elapsed)
			}

			atomic.AddInt64(&totalReqs, localTotal)
			atomic.AddInt64(&errors, localErrors)
			latencies.Lock()
			samples = append(samples, localSamples...)
			latencies.Unlock()
		}()
	}

	time.Sleep(dur)
	atomic.StoreInt32(&stopFlag, 1)
	wg.Wait()

	if len(samples) == 0 {
		return httpStepResult{concurrency: concurrency, broken: true}
	}

	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	n := len(samples)

	var sum float64
	for _, s := range samples {
		sum += float64(s)
	}
	mean := sum / float64(n)

	var variance float64
	for _, s := range samples {
		d := float64(s) - mean
		variance += d * d
	}
	stddev := math.Sqrt(variance / float64(n))

	errRate := float64(errors) / float64(totalReqs)
	rps := float64(totalReqs) / dur.Seconds()

	return httpStepResult{
		concurrency: concurrency,
		totalReqs:   totalReqs,
		errors:      errors,
		rps:         rps,
		errorRate:   errRate,
		p50:         samples[n/2],
		p90:         samples[int(float64(n)*0.90)],
		p99:         samples[int(float64(n)*0.99)],
		p999:        samples[int(float64(n)*0.999)],
		pMax:        samples[n-1],
		mean:        mean,
		stddev:      stddev,
		broken:      errRate > errorRateLimit,
	}
}

func fmtRPS2(rps float64) string {
	switch {
	case rps >= 1_000_000:
		return fmt.Sprintf("%.2fM/s", rps/1_000_000)
	case rps >= 1_000:
		return fmt.Sprintf("%.1fk/s", rps/1_000)
	default:
		return fmt.Sprintf("%.0f/s", rps)
	}
}

func writeProgressLine(line string) {
	path := filepath.Join(os.TempDir(), "stress-progress.txt")
	f, _ := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if f != nil {
		f.WriteString(line + "\n")
		f.Close()
	}
}

func writeStressReport(t *testing.T, results []httpStepResult, broken bool, ceiling, knee *httpStepResult) {
	t.Helper()

	var sb strings.Builder
	now := time.Now()

	sb.WriteString("# RuleEngine — HTTP Step Stress Test\n\n")
	fmt.Fprintf(&sb, "**Date**: %s  \n", now.Format("2006-01-02"))
	fmt.Fprintf(&sb, "**Go**: %s  \n", runtime.Version())
	fmt.Fprintf(&sb, "**OS/Arch**: %s/%s  \n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&sb, "**CPU cores**: %d logical · GOMAXPROCS=%d  \n", runtime.NumCPU(), runtime.GOMAXPROCS(0))
	fmt.Fprintf(&sb, "**Server**: `httptest.NewServer` (in-process, loopback TCP)  \n")
	fmt.Fprintf(&sb, "**Endpoint**: `POST /execute/{id}` · credit-eval (3 branches)  \n")
	fmt.Fprintf(&sb, "**Step duration**: %s · p99 SLO: %v  \n\n", stepDuration, p99SLO)

	// Summary
	sb.WriteString("## Summary\n\n")
	fmt.Fprintf(&sb, "| | Value |\n|---|---|\n")
	if knee != nil {
		fmt.Fprintf(&sb, "| **Optimal point** (p99 ≤ %v) | **%s RPS @ %d workers** |\n", p99SLO, fmtRPS2(knee.rps), knee.concurrency)
		fmt.Fprintf(&sb, "| p50 at optimal | %v |\n", knee.p50)
		fmt.Fprintf(&sb, "| p99 at optimal | %v |\n", knee.p99)
	}
	if ceiling != nil {
		fmt.Fprintf(&sb, "| Sustainable ceiling | %s @ %d workers |\n", fmtRPS2(ceiling.rps), ceiling.concurrency)
		fmt.Fprintf(&sb, "| p99 at ceiling | %v |\n", ceiling.p99)
	}
	if broken {
		last := results[len(results)-1]
		fmt.Fprintf(&sb, "| Break point | **%d workers** (err %.2f%%) |\n", last.concurrency, last.errorRate*100)
	} else {
		sb.WriteString("| Break point | Not reached |\n")
	}
	sb.WriteString("\n")

	// Full table
	sb.WriteString("## Step Results\n\n")
	sb.WriteString("| Workers | RPS | Err% | p50 | p90 | p99 | p99.9 | max | Reqs | Note |\n")
	sb.WriteString("|--------:|----:|-----:|----:|----:|----:|------:|----:|-----:|:----:|\n")
	for i := range results {
		r := &results[i]
		note := ""
		if knee != nil && r.concurrency == knee.concurrency {
			note = "⭐ optimal"
		} else if ceiling != nil && r.concurrency == ceiling.concurrency && !r.broken {
			note = "🔝 ceiling"
		} else if r.broken {
			note = "❌"
		}
		fmt.Fprintf(&sb, "| %d | %s | %.3f%% | %v | %v | %v | %v | %v | %d | %s |\n",
			r.concurrency, fmtRPS2(r.rps), r.errorRate*100,
			r.p50, r.p90, r.p99, r.p999, r.pMax, r.totalReqs, note)
	}
	sb.WriteString("\n")

	// Comparison with executor baseline — use c=1 p99 for accurate per-request overhead
	sb.WriteString("## vs Executor Baseline (no HTTP)\n\n")
	sb.WriteString("| Layer | p99 (c=1) | RPS |\n")
	sb.WriteString("|:------|----------:|----:|\n")
	sb.WriteString("| Pure executor (Go calls) | ~20µs | ~455k/s |\n")
	if len(results) > 0 {
		r1 := results[0] // c=1 step
		fmt.Fprintf(&sb, "| HTTP + JSON (this test, c=1) | %v | %s |\n", r1.p99, fmtRPS2(r1.rps))
		overhead := r1.p99 - 20_000
		if overhead > 0 {
			fmt.Fprintf(&sb, "\n> HTTP/JSON overhead per request ≈ **%v** (c=1 p99 − 20µs executor baseline).\n\n", overhead)
		}
	}

	sb.WriteString("## Environment\n\n```\n")
	fmt.Fprintf(&sb, "Go:         %s\n", runtime.Version())
	fmt.Fprintf(&sb, "OS/Arch:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&sb, "CPU cores:  %d logical\n", runtime.NumCPU())
	fmt.Fprintf(&sb, "GOMAXPROCS: %d\n", runtime.GOMAXPROCS(0))
	fmt.Fprintf(&sb, "Step dur:   %s\n", stepDuration)
	fmt.Fprintf(&sb, "Steps:      %v\n", stressSteps)
	sb.WriteString("```\n")

	report := sb.String()

	dir := filepath.Join("..", "..", "docs", "perf")
	if err := os.MkdirAll(dir, 0o755); err == nil {
		fn := filepath.Join(dir, fmt.Sprintf("http-step-stress-%s.md", now.Format("2006-01-02")))
		if err := os.WriteFile(fn, []byte(report), 0o644); err == nil {
			t.Logf("Report → %s", fn)
		}
	}

	fmt.Print(report)
}

// TestHTTPBranchLatency runs each policy branch (approved / manual / rejected) separately
// at the optimal concurrency (c=10, p99 SLO point) and reports per-branch percentiles.
// This isolates whether branch evaluation depth (1, 2, or 3 conditions) affects HTTP latency.
//
// Run with:
//
//	go test -v -run TestHTTPBranchLatency -count=1 -timeout=120s ./cmd/admin/
func TestHTTPBranchLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping HTTP branch latency test in -short mode; run with -timeout=120s without -short")
	}
	ps := store.New()

	creditPol := &policy.Policy{
		ID:    "branch-credit",
		Entry: "eval",
		States: []policy.State{
			{
				ID:   "eval",
				Kind: policy.KindExecution,
				Transitions: []policy.Transition{
					{When: "input.score >= 700 && input.amount <= 100000", To: "approved"},
					{When: "input.score >= 700 && input.amount > 100000", To: "manual"},
					{When: "true", To: "rejected"},
				},
			},
			{ID: "approved", Kind: policy.KindResponse, Status: 200, Data: map[string]any{"decision": "approved"}},
			{ID: "manual", Kind: policy.KindResponse, Status: 200, Data: map[string]any{"decision": "manual_review"}},
			{ID: "rejected", Kind: policy.KindResponse, Status: 200, Data: map[string]any{"decision": "rejected"}},
		},
	}

	art, err := compiler.Compile(creditPol)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	policyID, _, err := ps.Create("branch-credit", "team-branch", art)
	if err != nil {
		t.Fatalf("create policy: %v", err)
	}

	rl := ratelimit.New(time.Second, 10_000)
	mux := newAdminMux(ps, rl)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	executeURL := srv.URL + "/execute/" + policyID

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 20,
			MaxConnsPerHost:     20,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  true,
		},
		Timeout: 5 * time.Second,
	}

	type branchCase struct {
		name  string
		input []byte
		// conditions evaluated before match (1=approved, 2=manual, 3=rejected)
		depth int
	}
	cases := []branchCase{
		{"approved (depth=1)", []byte(`{"score":750,"amount":50000}`), 1},
		{"manual   (depth=2)", []byte(`{"score":750,"amount":200000}`), 2},
		{"rejected (depth=3)", []byte(`{"score":400,"amount":5000}`), 3},
	}

	const branchConcurrency = 10
	const branchDuration = 6 * time.Second

	fmt.Printf("\n%-22s  %10s  %8s  %8s  %8s  %8s  depth\n",
		"Branch", "RPS", "p50", "p99", "p99.9", "max")
	fmt.Println(strings.Repeat("─", 75))

	type branchResult struct {
		branchCase
		httpStepResult
	}
	var results []branchResult

	for _, bc := range cases {
		r := runHTTPStep(client, executeURL, [][]byte{bc.input}, branchConcurrency, branchDuration)
		results = append(results, branchResult{bc, r})
		fmt.Printf("%-22s  %10s  %8s  %8s  %8s  %8s  %d cond\n",
			bc.name, fmtRPS2(r.rps), r.p50, r.p99, r.p999, r.pMax, bc.depth)
		t.Logf("branch=%s rps=%.0f p50=%v p99=%v p99.9=%v depth=%d",
			bc.name, r.rps, r.p50, r.p99, r.p999, bc.depth)
	}
	fmt.Println(strings.Repeat("─", 75))

	// delta approved→rejected
	if len(results) == 3 {
		dp99 := results[2].p99 - results[0].p99
		drps := results[0].rps - results[2].rps
		fmt.Printf("\nrejected vs approved: Δp99=%v  ΔRPS=%.0f/s\n", dp99, drps)
		if dp99 < 5*time.Millisecond {
			fmt.Println("→ branch depth has NO measurable HTTP impact (TCP dominates)")
		} else {
			fmt.Printf("→ branch depth adds ~%v per condition evaluated\n", dp99/time.Duration(2))
		}
	}
	fmt.Println()

	// write report
	var sb strings.Builder
	fmt.Fprintf(&sb, "# RuleEngine — Branch Latency Breakdown\n\n")
	fmt.Fprintf(&sb, "**Date**: %s · **c=%d** · **duration**: %s  \n\n",
		time.Now().Format("2006-01-02"), branchConcurrency, branchDuration)
	sb.WriteString("| Branch | Conditions | RPS | p50 | p99 | p99.9 | max |\n")
	sb.WriteString("|:-------|----------:|----:|----:|----:|------:|----:|\n")
	for _, r := range results {
		fmt.Fprintf(&sb, "| %s | %d | %s | %v | %v | %v | %v |\n",
			r.name, r.depth, fmtRPS2(r.rps), r.p50, r.p99, r.p999, r.pMax)
	}
	if len(results) == 3 {
		dp99 := results[2].p99 - results[0].p99
		fmt.Fprintf(&sb, "\n> Δp99 (rejected − approved) = **%v**\n", dp99)
	}
	dir := filepath.Join("..", "..", "docs", "perf")
	if err := os.MkdirAll(dir, 0o755); err == nil {
		fn := filepath.Join(dir, fmt.Sprintf("branch-latency-%s.md", time.Now().Format("2006-01-02")))
		_ = os.WriteFile(fn, []byte(sb.String()), 0o644)
		t.Logf("Report → %s", fn)
	}
	fmt.Print(sb.String())
}
