package main

// TestHTTPConfigSustainedLoad exercises the four HTTPConfig knobs under
// sustained load for 30 minutes (5 scenarios × 6 min, sequential).
//
// Scenarios:
//
//	1  baseline-post       POST credit-eval, no meta (control)
//	2  meta-idle           POST credit-eval, TimeoutMs=5000 (never fires) — GetMeta overhead
//	3  cache-get           GET simple-200, CacheMaxAgeSeconds=300
//	4  retry-after-429     POST 429-policy, RetryOn=[429] RetryAfterSeconds=5
//	5  timeout-circuit     POST with apiCall to jitter server, TimeoutMs=30ms (≈33% cut)
//
// Run with:
//
//	go test -v -run TestHTTPConfigSustainedLoad -count=1 -timeout=2200s ./cmd/admin/

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

const scenarioDuration = 6 * time.Minute
const scenarioConcurrency = 10

// runScenario is a generalised load runner supporting GET/POST and custom success check.
// isSuccess receives the HTTP status code and returns true if the response is expected.
func runScenario(
	client *http.Client,
	url string,
	method string,
	bodies [][]byte,
	isSuccess func(int) bool,
	concurrency int,
	dur time.Duration,
) httpStepResult {
	var (
		totalReqs int64
		errors    int64
		latencies sync.Mutex
		samples   []time.Duration
	)

	// Pre-warm: 1 request before measurement
	body0 := bodies[0]
	if method == http.MethodPost {
		resp, _ := client.Post(url, "application/json", bytes.NewReader(body0))
		if resp != nil {
			resp.Body.Close()
		}
	} else {
		req, _ := http.NewRequest(method, url, nil)
		resp, _ := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
	}

	var stopFlag int32
	var wg sync.WaitGroup

	for w := 0; w < concurrency; w++ {
		idx := w % len(bodies)
		body := bodies[idx]
		wg.Add(1)
		go func() {
			defer wg.Done()
			var localSamples []time.Duration
			var localErrors int64
			var localTotal int64

			for atomic.LoadInt32(&stopFlag) == 0 {
				t0 := time.Now()
				var statusCode int
				var reqErr error

				if method == http.MethodPost {
					resp, err := client.Post(url, "application/json", bytes.NewReader(body))
					reqErr = err
					if err == nil {
						statusCode = resp.StatusCode
						resp.Body.Close()
					}
				} else {
					req, _ := http.NewRequest(method, url, nil)
					resp, err := client.Do(req)
					reqErr = err
					if err == nil {
						statusCode = resp.StatusCode
						resp.Body.Close()
					}
				}
				elapsed := time.Since(t0)

				localTotal++
				if reqErr != nil || !isSuccess(statusCode) {
					localErrors++
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

func TestHTTPConfigSustainedLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 30-min HTTPConfig sustained load test in -short mode; use -timeout=2200s without -short")
	}

	// ── Jitter server for timeout circuit-breaker scenario ──────────────────
	// Every 3rd request sleeps 50ms; the other 2 return immediately.
	// With TimeoutMs=30ms: fast requests succeed, slow ones are cut (~33% errors).
	var jitterSeq int64
	jitterSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&jitterSeq, 1)
		if n%3 == 0 {
			time.Sleep(50 * time.Millisecond)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer jitterSrv.Close()

	// ── Build policies ───────────────────────────────────────────────────────
	ps := store.New()

	creditPol := &policy.Policy{
		Name:  "sustained-credit",
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
	creditArt, err := compiler.Compile(creditPol)
	if err != nil {
		t.Fatalf("compile credit: %v", err)
	}
	baselineID, _, err := ps.Create("sustained-credit", "team-load", creditArt)
	if err != nil {
		t.Fatalf("create baseline: %v", err)
	}

	// meta-idle: same credit policy with a generous timeout that never fires
	creditPol2 := &policy.Policy{
		Name:  "sustained-credit-meta",
		Entry: "eval",
		States: creditPol.States,
	}
	creditArt2, _ := compiler.Compile(creditPol2)
	metaID, _, err := ps.Create("sustained-credit-meta", "team-load", creditArt2)
	if err != nil {
		t.Fatalf("create meta-idle: %v", err)
	}
	ps.SetMeta("sustained-credit-meta", &store.PolicyMeta{
		HTTP: store.HTTPConfig{TimeoutMs: 5000},
	})

	// cache-get: simple 200 response, Cache-Control on GET
	cachePol := &policy.Policy{
		Name:  "sustained-cache",
		Entry: "s",
		States: []policy.State{
			{ID: "s", Kind: policy.KindResponse, Status: 200, Data: map[string]any{"cached": true}},
		},
	}
	cacheArt, _ := compiler.Compile(cachePol)
	cacheID, _, err := ps.Create("sustained-cache", "team-load", cacheArt)
	if err != nil {
		t.Fatalf("create cache: %v", err)
	}
	ps.SetMeta("sustained-cache", &store.PolicyMeta{
		HTTP: store.HTTPConfig{CacheMaxAgeSeconds: 300},
	})

	// retry-after: policy returns 429; meta wires Retry-After header
	retryPol := &policy.Policy{
		Name:  "sustained-retry",
		Entry: "s",
		States: []policy.State{
			{ID: "s", Kind: policy.KindResponse, Status: 429, Data: map[string]any{"reason": "rate_limited"}},
		},
	}
	retryArt, _ := compiler.Compile(retryPol)
	retryID, _, err := ps.Create("sustained-retry", "team-load", retryArt)
	if err != nil {
		t.Fatalf("create retry: %v", err)
	}
	ps.SetMeta("sustained-retry", &store.PolicyMeta{
		HTTP: store.HTTPConfig{
			RetryOn:           []int{429},
			RetryAfterSeconds: 5,
		},
	})

	// timeout-circuit: policy makes an apiCall to the jitter server, cut at 30ms
	timeoutPol := &policy.Policy{
		Name:  "sustained-timeout",
		Entry: "call",
		States: []policy.State{
			{
				ID:          "call",
				Kind:        policy.KindAPICall,
				URL:         jitterSrv.URL + "/jitter",
				Method:      "GET",
				Transitions: []policy.Transition{{When: "true", To: "ok"}},
			},
			{ID: "ok", Kind: policy.KindResponse, Status: 200, Data: map[string]any{"result": "ok"}},
		},
	}
	timeoutArt, err := compiler.Compile(timeoutPol)
	if err != nil {
		t.Fatalf("compile timeout: %v", err)
	}
	timeoutID, _, err := ps.Create("sustained-timeout", "team-load", timeoutArt)
	if err != nil {
		t.Fatalf("create timeout: %v", err)
	}
	ps.SetMeta("sustained-timeout", &store.PolicyMeta{
		HTTP: store.HTTPConfig{TimeoutMs: 30},
	})

	// ── HTTP server ──────────────────────────────────────────────────────────
	rl := ratelimit.New(time.Second, 100_000)
	mux := newAdminMux(ps, rl)
	mux.HandleFunc("GET /execute/{id}", handleExecute(ps, rl))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	baseClient := &http.Client{
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 200,
			MaxConnsPerHost:     200,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  true,
			ForceAttemptHTTP2:   false,
		},
		Timeout: 10 * time.Second,
	}

	// ── Inputs ───────────────────────────────────────────────────────────────
	creditInputs := [][]byte{
		[]byte(`{"score":750,"amount":50000}`),
		[]byte(`{"score":750,"amount":200000}`),
		[]byte(`{"score":400,"amount":5000}`),
	}
	emptyBody := [][]byte{[]byte(`{}`)}

	// ── Run scenarios sequentially ───────────────────────────────────────────
	scenarios := []struct {
		name    string
		feature string
		run     func() httpStepResult
	}{
		{
			name:    "1-baseline-post",
			feature: "no HTTPConfig (control)",
			run: func() httpStepResult {
				return runScenario(baseClient, srv.URL+"/execute/"+baselineID,
					http.MethodPost, creditInputs,
					func(s int) bool { return s == 200 },
					scenarioConcurrency, scenarioDuration)
			},
		},
		{
			name:    "2-meta-idle",
			feature: "TimeoutMs=5000 (never fires) — GetMeta overhead",
			run: func() httpStepResult {
				return runScenario(baseClient, srv.URL+"/execute/"+metaID,
					http.MethodPost, creditInputs,
					func(s int) bool { return s == 200 },
					scenarioConcurrency, scenarioDuration)
			},
		},
		{
			name:    "3-cache-get",
			feature: "CacheMaxAgeSeconds=300 (GET)",
			run: func() httpStepResult {
				return runScenario(baseClient, srv.URL+"/execute/"+cacheID,
					http.MethodGet, emptyBody,
					func(s int) bool { return s == 200 },
					scenarioConcurrency, scenarioDuration)
			},
		},
		{
			name:    "4-retry-after-429",
			feature: "RetryOn=[429] RetryAfterSeconds=5",
			run: func() httpStepResult {
				return runScenario(baseClient, srv.URL+"/execute/"+retryID,
					http.MethodPost, emptyBody,
					func(s int) bool { return s == 429 }, // 429 is EXPECTED
					scenarioConcurrency, scenarioDuration)
			},
		},
		{
			name:    "5-timeout-circuit",
			feature: "TimeoutMs=30ms, jitter server every-3rd=50ms (≈33% cut)",
			run: func() httpStepResult {
				return runScenario(baseClient, srv.URL+"/execute/"+timeoutID,
					http.MethodPost, emptyBody,
					// 200 = apiCall completed; 500 = circuit-breaker fired
					func(s int) bool { return s == 200 || s == 500 },
					scenarioConcurrency, scenarioDuration)
			},
		},
	}

	results := make([]namedResult, 0, len(scenarios))

	fmt.Printf("\n═══════════════════════════════════════════════════════════════════════════════\n")
	fmt.Printf("  HTTPConfig Sustained Load — %d scenarios × %v (total: %v)\n",
		len(scenarios), scenarioDuration, time.Duration(len(scenarios))*scenarioDuration)
	fmt.Printf("  c=%d workers per scenario · p99 SLO: %v\n", scenarioConcurrency, p99SLO)
	fmt.Printf("═══════════════════════════════════════════════════════════════════════════════\n\n")

	fmt.Printf("%-26s  %8s  %8s  %8s  %8s  %9s  %s\n",
		"Scenario", "RPS", "p50", "p99", "p99.9", "Err%", "Feature")
	fmt.Println(strings.Repeat("─", 100))

	for i, sc := range scenarios {
		fmt.Printf("[%d/%d] Running %s for %v ...\n", i+1, len(scenarios), sc.name, scenarioDuration)
		writeProgressLine(fmt.Sprintf("[sustained %d/%d] starting %s", i+1, len(scenarios), sc.name))

		r := sc.run()
		results = append(results, namedResult{sc.name, sc.feature, r})

		fmt.Printf("%-26s  %8s  %8s  %8s  %8s  %8.3f%%  %s\n",
			sc.name, fmtRPS2(r.rps), r.p50, r.p99, r.p999, r.errorRate*100, sc.feature)
		t.Logf("scenario=%s rps=%.0f p50=%v p99=%v err=%.3f%%", sc.name, r.rps, r.p50, r.p99, r.errorRate*100)

		writeProgressLine(fmt.Sprintf("[sustained %d/%d] done %s rps=%.0f p99=%v err=%.3f%%",
			i+1, len(scenarios), sc.name, r.rps, r.p99, r.errorRate*100))
	}

	fmt.Println(strings.Repeat("─", 100))
	fmt.Println()

	// ── Analysis ─────────────────────────────────────────────────────────────
	if len(results) >= 2 {
		base := results[0].result
		fmt.Println("HTTPConfig overhead vs baseline (Δ p99):")
		for _, r := range results[1:] {
			delta := r.result.p99 - base.p99
			sign := "+"
			if delta < 0 {
				sign = ""
			}
			fmt.Printf("  %-26s  %sp99=%v  ΔRPS=%.0f/s\n",
				r.name, sign, delta, r.result.rps-base.rps)
		}
		fmt.Println()
	}

	writeSustainedLoadReport(t, results)
}

type namedResult struct {
	name    string
	feature string
	result  httpStepResult
}

func writeSustainedLoadReport(t *testing.T, results []namedResult) {
	t.Helper()

	var sb strings.Builder
	now := time.Now()

	sb.WriteString("# RuleEngine — HTTPConfig Sustained Load Test\n\n")
	fmt.Fprintf(&sb, "**Date**: %s  \n", now.Format("2006-01-02"))
	fmt.Fprintf(&sb, "**Go**: %s  \n", runtime.Version())
	fmt.Fprintf(&sb, "**OS/Arch**: %s/%s  \n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&sb, "**CPU cores**: %d logical · GOMAXPROCS=%d  \n", runtime.NumCPU(), runtime.GOMAXPROCS(0))
	fmt.Fprintf(&sb, "**Server**: `httptest.NewServer` (in-process, loopback TCP)  \n")
	fmt.Fprintf(&sb, "**Duration per scenario**: %v · **Concurrency**: %d workers  \n", scenarioDuration, scenarioConcurrency)
	fmt.Fprintf(&sb, "**Scenarios**: %d sequential · Total duration: %v  \n\n",
		len(results), time.Duration(len(results))*scenarioDuration)

	sb.WriteString("## Scenario Results\n\n")
	sb.WriteString("| Scenario | Feature | RPS | Err% | p50 | p90 | p99 | p99.9 | max | Reqs |\n")
	sb.WriteString("|:---------|:--------|----:|-----:|----:|----:|----:|------:|----:|-----:|\n")
	for _, r := range results {
		fmt.Fprintf(&sb, "| `%s` | %s | %s | %.3f%% | %v | %v | %v | %v | %v | %d |\n",
			r.name, r.feature,
			fmtRPS2(r.result.rps), r.result.errorRate*100,
			r.result.p50, r.result.p90, r.result.p99, r.result.p999, r.result.pMax,
			r.result.totalReqs)
	}
	sb.WriteString("\n")

	// Delta table vs baseline
	if len(results) >= 2 {
		base := results[0].result
		sb.WriteString("## Overhead vs Baseline\n\n")
		sb.WriteString("| Scenario | Δ RPS | Δ p50 | Δ p99 | Δ Err% |\n")
		sb.WriteString("|:---------|------:|------:|------:|-------:|\n")
		for _, r := range results[1:] {
			fmt.Fprintf(&sb, "| `%s` | %+.0f/s | %v | %v | %+.3f%% |\n",
				r.name,
				r.result.rps-base.rps,
				r.result.p50-base.p50,
				r.result.p99-base.p99,
				(r.result.errorRate-base.errorRate)*100)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Scenario Notes\n\n")
	sb.WriteString("- **2-meta-idle**: measures `GetMeta` lookup cost + `TimeoutMs` range check on every request (timeout never fires at 5000ms)\n")
	sb.WriteString("- **3-cache-get**: measures Cache-Control header write path on GET; header is set but not consumed by any cache in this in-process test\n")
	sb.WriteString("- **4-retry-after-429**: policy always returns 429; `Retry-After: 5` header is always written; 429 counted as success (expected status)\n")
	sb.WriteString("- **5-timeout-circuit**: jitter server sleeps 50ms every 3rd request; `TimeoutMs=30ms` cuts those → ~33% errors from circuit-breaker; latency bimodal (fast ~5ms, cut ~30ms)\n")

	sb.WriteString("\n## Environment\n\n```\n")
	fmt.Fprintf(&sb, "Go:         %s\n", runtime.Version())
	fmt.Fprintf(&sb, "OS/Arch:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&sb, "CPU cores:  %d logical\n", runtime.NumCPU())
	fmt.Fprintf(&sb, "GOMAXPROCS: %d\n", runtime.GOMAXPROCS(0))
	fmt.Fprintf(&sb, "Scenario dur: %v\n", scenarioDuration)
	fmt.Fprintf(&sb, "Concurrency:  %d workers\n", scenarioConcurrency)
	sb.WriteString("```\n")

	report := sb.String()

	dir := filepath.Join("..", "..", "docs", "perf")
	if err := os.MkdirAll(dir, 0o755); err == nil {
		fn := filepath.Join(dir, fmt.Sprintf("httpconfig-sustained-%s.md", now.Format("2006-01-02")))
		if err := os.WriteFile(fn, []byte(report), 0o644); err == nil {
			t.Logf("Report → %s", fn)
		}
	}

	fmt.Print(report)
}
