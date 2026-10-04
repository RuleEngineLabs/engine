package executor_test

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/RuleEngineLabs/engine/internal/executor"
)

// TestPercentiles coleta 200 000 amostras por cenário e imprime p50/p90/p99/p999/max.
// Não é um teste funcional — rode com:
//
//	go test -v -run TestPercentiles -count=1 -timeout=120s ./internal/executor/
func TestPercentiles(t *testing.T) {
	const N = 200_000
	ctx := context.Background()

	type run struct {
		name string
		fn   func()
	}

	runs := []run{
		{
			name: "TwoState / nil input",
			fn:   func() { executor.Execute(ctx, twoStateArt, nil) }, //nolint:errcheck
		},
		{
			name: "CreditEval / approved (1st branch)",
			fn:   func() { executor.Execute(ctx, creditEvalArt, map[string]any{"score": 750, "amount": 50000}) },
		},
		{
			name: "CreditEval / rejected (last branch)",
			fn:   func() { executor.Execute(ctx, creditEvalArt, map[string]any{"score": 400, "amount": 5000}) },
		},
		{
			name: "DeepChain / 5 states",
			fn:   func() { executor.Execute(ctx, deepChainArt, map[string]any{"x": 1}) },
		},
		{
			name: "CreditEval / with ISO dates",
			fn: func() {
				executor.Execute(ctx, creditEvalArt, map[string]any{ //nolint:errcheck
					"birthdate": "1990-05-15", "createdAt": "2024-01-01T10:00:00Z",
					"score": 720, "amount": 80000,
				})
			},
		},
	}

	fmt.Printf("\n%-42s  %8s  %8s  %8s  %8s  %8s\n",
		"Scenario", "p50", "p90", "p99", "p99.9", "max")
	fmt.Printf("%-42s  %8s  %8s  %8s  %8s  %8s\n",
		"----------------------------------------",
		"-------", "-------", "-------", "-------", "-------")

	for _, r := range runs {
		// Warm up the JIT and OS scheduler
		for i := 0; i < 2000; i++ {
			r.fn()
		}

		samples := make([]time.Duration, N)
		for i := 0; i < N; i++ {
			start := time.Now()
			r.fn()
			samples[i] = time.Since(start)
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })

		p50 := samples[N/2]
		p90 := samples[int(float64(N)*0.90)]
		p99 := samples[int(float64(N)*0.99)]
		p999 := samples[int(float64(N)*0.999)]
		max := samples[N-1]

		fmt.Printf("%-42s  %8s  %8s  %8s  %8s  %8s\n",
			r.name, p50, p90, p99, p999, max)
		t.Logf("%-42s  p50=%v  p90=%v  p99=%v  p99.9=%v  max=%v",
			r.name, p50, p90, p99, p999, max)
	}
	fmt.Println()
}
