package executor_test

import (
	"context"
	"testing"

	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/executor"
	"github.com/RuleEngineLabs/engine/internal/policy"
)

func mustCompile(t *testing.T, p *policy.Policy) *compiler.Artifact {
	t.Helper()
	art, err := compiler.Compile(p)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return art
}

func TestExecute_DirectPathToResponse(t *testing.T) {
	p := &policy.Policy{
		ID:    "p1",
		Entry: "start",
		States: []policy.State{
			{
				ID:   "start",
				Kind: policy.KindExecution,
				Transitions: []policy.Transition{
					{When: "true", To: "ok"},
				},
			},
			{ID: "ok", Kind: policy.KindResponse, Status: 200, Data: map[string]any{"result": "ok"}},
		},
	}
	art := mustCompile(t, p)

	res, err := executor.Execute(context.Background(), art, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.State != "ok" {
		t.Errorf("expected state=ok, got %q", res.State)
	}
	data, ok := res.Data.(map[string]any)
	if !ok || data["result"] != "ok" {
		t.Errorf("unexpected data: %v", res.Data)
	}
}

func TestExecute_TransitionPriority_FirstTrueWins(t *testing.T) {
	p := &policy.Policy{
		ID:    "p2",
		Entry: "check",
		States: []policy.State{
			{
				ID:   "check",
				Kind: policy.KindExecution,
				Transitions: []policy.Transition{
					{When: "input.score >= 90", To: "approve"},
					{When: "true", To: "reject"},
				},
			},
			{ID: "approve", Kind: policy.KindResponse, Status: 200, Data: "approved"},
			{ID: "reject", Kind: policy.KindResponse, Status: 200, Data: "rejected"},
		},
	}
	art := mustCompile(t, p)

	res, err := executor.Execute(context.Background(), art, map[string]any{"score": 95})
	if err != nil {
		t.Fatalf("execute score=95: %v", err)
	}
	if res.State != "approve" {
		t.Errorf("score=95: expected approve, got %q", res.State)
	}

	res, err = executor.Execute(context.Background(), art, map[string]any{"score": 50})
	if err != nil {
		t.Fatalf("execute score=50: %v", err)
	}
	if res.State != "reject" {
		t.Errorf("score=50: expected reject, got %q", res.State)
	}
}

func TestExecute_FallbackOnNoMatch(t *testing.T) {
	p := &policy.Policy{
		ID:    "p3",
		Entry: "check",
		States: []policy.State{
			{
				ID:       "check",
				Kind:     policy.KindExecution,
				Fallback: "err",
				Transitions: []policy.Transition{
					{When: "input.score >= 90", To: "ok"},
				},
			},
			{ID: "ok", Kind: policy.KindResponse, Status: 200},
			{ID: "err", Kind: policy.KindResponse, Status: 500},
		},
	}
	art := mustCompile(t, p)

	res, err := executor.Execute(context.Background(), art, map[string]any{"score": 50})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.State != "err" {
		t.Errorf("expected fallback to err, got %q", res.State)
	}
}
