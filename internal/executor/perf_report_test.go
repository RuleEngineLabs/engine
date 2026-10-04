package executor_test

// TestPerfReport collects latency percentiles, memory stats, and throughput (RPS)
// for every canonical executor scenario and writes a Markdown baseline report to
// docs/perf/local-baseline-YYYY-MM-DD.md.
//
// Run with:
//
//	go test -v -run TestPerfReport -count=1 -timeout=300s ./internal/executor/

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RuleEngineLabs/engine/internal/executor"
)

func TestPerfReport(t *testing.T) {
	const (
		N          = 50_000  // total calls per scenario (for memory + RPS)
		batchSize  = 100     // calls per timing sample (avoids Windows sub-ms timer floor)
		warmup     = 1_000
		rpsSeconds = 3
	)

	pctx := context.Background()

	type scenario struct {
		name string
		fn   func()
	}

	scenarios := []scenario{
		{
			name: "TwoState / nil input",
			fn:   func() { executor.Execute(pctx, twoStateArt, nil) }, //nolint:errcheck
		},
		{
			name: "CreditEval / approved (1st branch)",
			fn:   func() { executor.Execute(pctx, creditEvalArt, map[string]any{"score": 750, "amount": 50000}) }, //nolint:errcheck
		},
		{
			name: "CreditEval / rejected (last branch)",
			fn:   func() { executor.Execute(pctx, creditEvalArt, map[string]any{"score": 400, "amount": 5000}) }, //nolint:errcheck
		},
		{
			name: "DeepChain / 5 states",
			fn:   func() { executor.Execute(pctx, deepChainArt, map[string]any{"x": 1}) }, //nolint:errcheck
		},
		{
			name: "CreditEval / with ISO dates",
			fn: func() {
				executor.Execute(pctx, creditEvalArt, map[string]any{ //nolint:errcheck
					"birthdate": "1990-05-15", "createdAt": "2024-01-01T10:00:00Z",
					"score": 720, "amount": 80000,
				})
			},
		},
	}

	numWorkers := runtime.GOMAXPROCS(0)
	results := make([]scenarioResult, len(scenarios))

	for si, sc := range scenarios {
		// Warmup
		for i := 0; i < warmup; i++ {
			sc.fn()
		}
		runtime.GC()
		runtime.GC() // two passes to stabilise heap

		// ── Sequential: latency (batched) + memory ───────────────────────
		// On Windows, time.Now() resolution is ~100ns — too coarse for
		// sub-µs individual calls. Batch batchSize calls per sample and
		// divide to get per-call average; N total calls keep memory stats
		// accurate while nBatches gives the percentile distribution.
		nBatches := N / batchSize

		var msStart, msEnd runtime.MemStats
		runtime.ReadMemStats(&msStart)

		samples := make([]time.Duration, nBatches)
		seqStart := time.Now()
		for i := 0; i < nBatches; i++ {
			t0 := time.Now()
			for j := 0; j < batchSize; j++ {
				sc.fn()
			}
			samples[i] = time.Since(t0) / time.Duration(batchSize)
		}
		totalSeq := time.Since(seqStart)

		runtime.ReadMemStats(&msEnd)

		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })

		var sum float64
		for _, s := range samples {
			sum += float64(s)
		}
		mean := sum / float64(nBatches)

		var variance float64
		for _, s := range samples {
			d := float64(s) - mean
			variance += d * d
		}
		stddev := math.Sqrt(variance / float64(nBatches))

		r := scenarioResult{
			name:          sc.name,
			p50:           samples[nBatches/2],
			p90:           samples[int(float64(nBatches)*0.90)],
			p99:           samples[int(float64(nBatches)*0.99)],
			p999:          samples[int(float64(nBatches)*0.999)],
			pMax:          samples[nBatches-1],
			mean:          mean,
			stddev:        stddev,
			rps1:          float64(N) / totalSeq.Seconds(),
			bytesPerCall:  float64(msEnd.TotalAlloc-msStart.TotalAlloc) / float64(N),
			allocsPerCall: float64(msEnd.Mallocs-msStart.Mallocs) / float64(N),
			gcCycles:      msEnd.NumGC - msStart.NumGC,
			heapPeakMB:    float64(msEnd.HeapSys) / (1 << 20),
		}

		// ── Parallel throughput: GOMAXPROCS goroutines for rpsSeconds ─────
		// Use an atomic stop flag instead of time.Now() per-iteration:
		// on Windows, QPC calls in a tight multi-goroutine loop cause
		// severe contention and make the test appear to hang.
		var stopFlag int32 // 0 = run, 1 = stop
		var counter int64
		var wg sync.WaitGroup
		fn := sc.fn // capture by value before goroutines start
		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				var local int64
				for atomic.LoadInt32(&stopFlag) == 0 {
					fn()
					local++
				}
				atomic.AddInt64(&counter, local)
			}()
		}
		time.Sleep(time.Duration(rpsSeconds) * time.Second)
		atomic.StoreInt32(&stopFlag, 1)
		wg.Wait()
		r.rpsN = float64(counter) / float64(rpsSeconds)

		results[si] = r
		t.Logf("[%s] p50=%v p99=%v rps1=%.0f rpsN=%.0f B/call=%.1f allocs/call=%.1f",
			sc.name, r.p50, r.p99, r.rps1, r.rpsN, r.bytesPerCall, r.allocsPerCall)
	}

	// ── Build Markdown report ──────────────────────────────────────────────
	report := buildPerfReport(results, N, warmup, rpsSeconds, numWorkers)

	// Write to file
	dir := filepath.Join("..", "..", "docs", "perf")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("WARNING: could not create report directory: %v", err)
	} else {
		filename := filepath.Join(dir, fmt.Sprintf("local-baseline-%s.md", time.Now().Format("2006-01-02")))
		if err := os.WriteFile(filename, []byte(report), 0o644); err != nil {
			t.Logf("WARNING: could not write report file: %v", err)
		} else {
			t.Logf("Report written → %s", filename)
		}
	}

	fmt.Print(report)
}

type scenarioResult = struct {
	name          string
	p50, p90, p99 time.Duration
	p999, pMax    time.Duration
	mean, stddev  float64
	rps1          float64
	rpsN          float64
	bytesPerCall  float64
	allocsPerCall float64
	gcCycles      uint32
	heapPeakMB    float64
}

func buildPerfReport(results []scenarioResult, N, warmup, rpsSeconds, numWorkers int) string {
	var sb strings.Builder
	now := time.Now()

	// Header
	sb.WriteString("# RuleEngine — Local Performance Baseline\n\n")
	fmt.Fprintf(&sb, "**Date**: %s  \n", now.Format("2006-01-02"))
	fmt.Fprintf(&sb, "**Go**: %s  \n", runtime.Version())
	fmt.Fprintf(&sb, "**OS/Arch**: %s/%s  \n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&sb, "**CPU cores**: %d logical · GOMAXPROCS=%d  \n", runtime.NumCPU(), runtime.GOMAXPROCS(0))
	fmt.Fprintf(&sb, "**Samples**: %s calls · %s latency batches · warmup %s  \n", fmtN(N), fmtN(N/100), fmtN(warmup))
	sb.WriteString("\n> **Purpose**: local baseline for cloud deployment comparison.\n")
	sb.WriteString("> When deploying, compare cloud p99 against these values to quantify integration overhead.\n\n")
	sb.WriteString("---\n\n")

	// 1. Latency
	sb.WriteString("## 1. Latency Distribution (single-threaded, 2 k batch samples)\n\n")
	sb.WriteString("_Each sample is the average over 100 consecutive calls (batch timing) to overcome Windows QPC floor._\n\n")
	sb.WriteString("| Scenario | Mean | p50 | p90 | p99 | p99.9 | Max | StdDev |\n")
	sb.WriteString("|:---------|-----:|----:|----:|----:|------:|----:|-------:|\n")
	for _, r := range results {
		fmt.Fprintf(&sb, "| %-44s | %s | %s | %s | %s | %s | %s | %s |\n",
			r.name,
			fmtDur(r.mean), r.p50, r.p90, r.p99, r.p999, r.pMax, fmtDur(r.stddev),
		)
	}
	sb.WriteString("\n")

	// 2. Tail latency
	sb.WriteString("## 2. Tail Latency Amplification\n\n")
	sb.WriteString("p99/p50 ratio: how much worse the 99th percentile is vs the median.\n")
	sb.WriteString("Values < 3× indicate consistent execution; > 10× suggests GC pauses or OS scheduling jitter.\n\n")
	sb.WriteString("| Scenario | p50 | p99 | p99/p50 | p99.9/p50 |\n")
	sb.WriteString("|:---------|----:|----:|--------:|----------:|\n")
	for _, r := range results {
		ratio99 := float64(r.p99) / float64(r.p50)
		ratio999 := float64(r.p999) / float64(r.p50)
		fmt.Fprintf(&sb, "| %-44s | %s | %s | %.1fx | %.1fx |\n",
			r.name, r.p50, r.p99, ratio99, ratio999)
	}
	sb.WriteString("\n")

	// 3. Throughput
	sb.WriteString("## 3. Throughput (RPS)\n\n")
	sb.WriteString("### Single-threaded\n\n")
	sb.WriteString("| Scenario | RPS | Latency budget used |\n")
	sb.WriteString("|:---------|----:|--------------------:|\n")
	for _, r := range results {
		// RPS from sequential test (N / totalSeq)
		fmt.Fprintf(&sb, "| %-44s | %s | — |\n", r.name, fmtRPS(r.rps1))
	}
	sb.WriteString("\n")

	fmt.Fprintf(&sb, "### Parallel (%d workers · %ds window)\n\n", numWorkers, rpsSeconds)
	sb.WriteString("| Scenario | RPS (peak) | Scale vs single | Efficiency |\n")
	sb.WriteString("|:---------|----------:|----------------:|-----------:|\n")
	for _, r := range results {
		scale := r.rpsN / r.rps1
		efficiency := scale / float64(numWorkers) * 100
		fmt.Fprintf(&sb, "| %-44s | %s | %.1fx | %.0f%% |\n",
			r.name, fmtRPS(r.rpsN), scale, efficiency)
	}
	sb.WriteString("\n> **Efficiency** = scale / GOMAXPROCS × 100. ")
	sb.WriteString("100% means perfect linear scaling. Lower values indicate lock contention or shared-state bottlenecks.\n\n")

	// 4. Memory
	sb.WriteString("## 4. Memory per Call\n\n")
	sb.WriteString("Measured as (TotalAlloc after − before) / N. Includes all heap allocations inside `Execute()`.\n\n")
	sb.WriteString("| Scenario | Bytes/call | Allocs/call | GC cycles (200k calls) |\n")
	sb.WriteString("|:---------|----------:|------------:|-----------------------:|\n")
	for _, r := range results {
		fmt.Fprintf(&sb, "| %-44s | %6.1f B | %6.1f | %6d |\n",
			r.name, r.bytesPerCall, r.allocsPerCall, r.gcCycles)
	}
	sb.WriteString("\n")

	// 5. Cloud comparison targets
	sb.WriteString("## 5. Cloud Comparison Targets\n\n")
	sb.WriteString("Use these thresholds when profiling the deployed service.\n")
	sb.WriteString("The \"integration overhead\" is the cost added by HTTP transport, serialisation, and cold starts.\n\n")
	sb.WriteString("| Scenario | Local p99 | Warm cloud budget (×3) | Cold start budget (×20) |\n")
	sb.WriteString("|:---------|----------:|----------------------:|------------------------:|\n")
	for _, r := range results {
		fmt.Fprintf(&sb, "| %-44s | %s | %s | %s |\n",
			r.name, r.p99, r.p99*3, r.p99*20)
	}
	sb.WriteString("\n> **Rule**: if cloud (warm) p99 > local p99 × 3, investigate HTTP/JSON serialisation overhead.\n\n")

	// 6. Environment
	sb.WriteString("## 6. Environment\n\n```\n")
	fmt.Fprintf(&sb, "Go:         %s\n", runtime.Version())
	fmt.Fprintf(&sb, "OS/Arch:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&sb, "CPU cores:  %d logical\n", runtime.NumCPU())
	fmt.Fprintf(&sb, "GOMAXPROCS: %d\n", runtime.GOMAXPROCS(0))
	fmt.Fprintf(&sb, "N samples:  %s per scenario\n", fmtN(N))
	fmt.Fprintf(&sb, "Warmup:     %s iterations\n", fmtN(warmup))
	fmt.Fprintf(&sb, "RPS test:   %ds window · %d goroutines\n", rpsSeconds, numWorkers)
	sb.WriteString("```\n\n")

	return sb.String()
}

// fmtDur formats a float64 nanosecond duration as a time.Duration string.
func fmtDur(ns float64) string {
	return time.Duration(int64(ns)).String()
}

// fmtRPS formats requests per second as k/M suffix for readability.
func fmtRPS(rps float64) string {
	switch {
	case rps >= 1_000_000:
		return fmt.Sprintf("%.2fM/s", rps/1_000_000)
	case rps >= 1_000:
		return fmt.Sprintf("%.1fk/s", rps/1_000)
	default:
		return fmt.Sprintf("%.0f/s", rps)
	}
}

// fmtN formats a large integer with k/M suffix.
func fmtN(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.0fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
