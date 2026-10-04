package executor_test

import (
	"context"
	"testing"

	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/executor"
	"github.com/RuleEngineLabs/engine/internal/policy"
)

// benchCompile compiles a policy for benchmark fixtures; panics on error.
func benchCompile(p *policy.Policy) *compiler.Artifact {
	art, err := compiler.Compile(p)
	if err != nil {
		panic(err)
	}
	return art
}

// twoStateArt is the minimal policy: one execution state + one response state.
// Exercises the fastest possible path through the executor.
var twoStateArt = benchCompile(&policy.Policy{
	ID:    "bench-simple",
	Entry: "eval",
	States: []policy.State{
		{
			ID:   "eval",
			Kind: policy.KindExecution,
			Transitions: []policy.Transition{
				{When: "true", To: "ok"},
			},
		},
		{ID: "ok", Kind: policy.KindResponse, Status: 200, Data: map[string]any{"result": "ok"}},
	},
})

// creditEvalArt mirrors the Postman collection policy: 3 transitions + 3 response states.
var creditEvalArt = benchCompile(&policy.Policy{
	ID:    "bench-credit",
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
})

// deepChainArt is a 5-state execution chain — tests loop overhead with more steps.
var deepChainArt = benchCompile(&policy.Policy{
	ID:    "bench-chain",
	Entry: "s1",
	States: []policy.State{
		{ID: "s1", Kind: policy.KindExecution, Transitions: []policy.Transition{{When: "true", To: "s2"}}},
		{ID: "s2", Kind: policy.KindExecution, Transitions: []policy.Transition{{When: "true", To: "s3"}}},
		{ID: "s3", Kind: policy.KindExecution, Transitions: []policy.Transition{{When: "true", To: "s4"}}},
		{ID: "s4", Kind: policy.KindExecution, Transitions: []policy.Transition{{When: "true", To: "s5"}}},
		{ID: "s5", Kind: policy.KindResponse, Status: 200, Data: "done"},
	},
})

var ctx = context.Background()

// BenchmarkExecute_TwoState measures the minimal execution path.
// Baseline: should be well under 10µs on modern hardware.
func BenchmarkExecute_TwoState(b *testing.B) {
	input := map[string]any{"x": 1}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = executor.Execute(ctx, twoStateArt, input)
	}
}

// BenchmarkExecute_CreditEval measures the 3-transition policy used in the Postman suite.
func BenchmarkExecute_CreditEval(b *testing.B) {
	input := map[string]any{"score": 750, "amount": 50000}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = executor.Execute(ctx, creditEvalArt, input)
	}
}

// BenchmarkExecute_CreditEval_Rejected exercises the last transition branch.
func BenchmarkExecute_CreditEval_Rejected(b *testing.B) {
	input := map[string]any{"score": 400, "amount": 5000}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = executor.Execute(ctx, creditEvalArt, input)
	}
}

// BenchmarkExecute_DeepChain measures loop overhead for a 5-step execution chain.
func BenchmarkExecute_DeepChain(b *testing.B) {
	input := map[string]any{"x": 1}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = executor.Execute(ctx, deepChainArt, input)
	}
}

// BenchmarkExecute_NoInput exercises nil input (no coerceISODates walk at all).
func BenchmarkExecute_NoInput(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = executor.Execute(ctx, twoStateArt, nil)
	}
}

// BenchmarkExecute_InputWithDates benchmarks the ISO date coercion path.
func BenchmarkExecute_InputWithDates(b *testing.B) {
	input := map[string]any{
		"birthdate":  "1990-05-15",
		"createdAt":  "2024-01-01T10:00:00Z",
		"score":      720,
		"amount":     80000,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = executor.Execute(ctx, creditEvalArt, input)
	}
}

// BenchmarkPreview_CreditEval measures the preview path (includes tracing overhead).
func BenchmarkPreview_CreditEval(b *testing.B) {
	input := map[string]any{"score": 750, "amount": 50000}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = executor.Preview(ctx, creditEvalArt, input)
	}
}

// BenchmarkExecute_Parallel runs N iterations to measure concurrent throughput.
// go test -bench=BenchmarkExecute_CreditEval -benchtime=5s -cpu=1,2,4,8
func BenchmarkExecute_Parallel_CreditEval(b *testing.B) {
	input := map[string]any{"score": 750, "amount": 50000}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = executor.Execute(ctx, creditEvalArt, input)
		}
	})
}
