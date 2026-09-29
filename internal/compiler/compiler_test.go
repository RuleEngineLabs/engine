package compiler_test

import (
	"testing"

	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/policy"
)

func okPolicy() *policy.Policy {
	return &policy.Policy{
		ID:    "p1",
		Entry: "start",
		States: []policy.State{
			{ID: "start", Kind: policy.KindExecution, Fallback: "err"},
			{ID: "err", Kind: policy.KindResponse, Status: 500},
		},
	}
}

func TestCompile_OK(t *testing.T) {
	_, err := compiler.Compile(okPolicy())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestCompile_MissingEntry(t *testing.T) {
	p := okPolicy()
	p.Entry = ""
	if _, err := compiler.Compile(p); err == nil {
		t.Fatal("expected error for missing entry")
	}
}

func TestCompile_DuplicateStateID(t *testing.T) {
	p := okPolicy()
	p.States = append(p.States, policy.State{ID: "start", Kind: policy.KindResponse})
	if _, err := compiler.Compile(p); err == nil {
		t.Fatal("expected error for duplicate state id")
	}
}

func TestCompile_UnknownEntry(t *testing.T) {
	p := okPolicy()
	p.Entry = "nonexistent"
	if _, err := compiler.Compile(p); err == nil {
		t.Fatal("expected error for unknown entry")
	}
}

func TestCompile_UnsupportedKind(t *testing.T) {
	p := okPolicy()
	p.States[0].Kind = "unknownKind"
	if _, err := compiler.Compile(p); err == nil {
		t.Fatal("expected error for unsupported kind")
	}
}

func TestCompile_WithTransition(t *testing.T) {
	p := &policy.Policy{
		ID:    "p2",
		Entry: "check",
		States: []policy.State{
			{
				ID:   "check",
				Kind: policy.KindExecution,
				Transitions: []policy.Transition{
					{When: "true", To: "ok"},
				},
				Fallback: "err",
			},
			{ID: "ok", Kind: policy.KindResponse, Status: 200},
			{ID: "err", Kind: policy.KindResponse, Status: 500},
		},
	}
	if _, err := compiler.Compile(p); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestCompile_ReservedContextKey(t *testing.T) {
	p := &policy.Policy{
		ID:    "p3",
		Entry: "s",
		States: []policy.State{
			{ID: "s", Kind: policy.KindExecution, ContextKey: "input", Fallback: "end"},
			{ID: "end", Kind: policy.KindResponse, Status: 200},
		},
	}
	_, err := compiler.Compile(p)
	if err == nil {
		t.Fatal("expected error for reserved contextKey")
	}
	if !containsSubstr(err.Error(), "input") {
		t.Errorf("expected error to mention 'input', got: %v", err)
	}
}

func TestCompile_CyclicGraph(t *testing.T) {
	p := &policy.Policy{
		ID:    "p4",
		Entry: "a",
		States: []policy.State{
			{
				ID:   "a",
				Kind: policy.KindExecution,
				Transitions: []policy.Transition{
					{When: "true", To: "b"},
				},
			},
			{
				ID:   "b",
				Kind: policy.KindExecution,
				Transitions: []policy.Transition{
					{When: "true", To: "a"},
				},
			},
		},
	}
	_, err := compiler.Compile(p)
	if err == nil {
		t.Fatal("expected error for cyclic graph")
	}
	if !containsSubstr(err.Error(), "cycle") {
		t.Errorf("expected error to mention 'cycle', got: %v", err)
	}
}

func TestCompile_ThreeStateChain(t *testing.T) {
	p := &policy.Policy{
		ID:    "p5",
		Entry: "s1",
		States: []policy.State{
			{
				ID:   "s1",
				Kind: policy.KindExecution,
				Transitions: []policy.Transition{
					{When: "true", To: "s2"},
				},
			},
			{
				ID:   "s2",
				Kind: policy.KindExecution,
				Transitions: []policy.Transition{
					{When: "true", To: "s3"},
				},
			},
			{ID: "s3", Kind: policy.KindResponse, Status: 200},
		},
	}
	if _, err := compiler.Compile(p); err != nil {
		t.Fatalf("expected no error for valid 3-state chain, got %v", err)
	}
}

func TestCompile_ContextKeyRefNeverWritten(t *testing.T) {
	p := &policy.Policy{
		ID:    "p6",
		Entry: "check",
		States: []policy.State{
			{
				ID:   "check",
				Kind: policy.KindExecution,
				Transitions: []policy.Transition{
					{When: "contextKey.preco > 100", To: "ok"},
				},
				Fallback: "err",
			},
			{ID: "ok", Kind: policy.KindResponse, Status: 200},
			{ID: "err", Kind: policy.KindResponse, Status: 500},
		},
	}
	_, err := compiler.Compile(p)
	if err == nil {
		t.Fatal("expected error for contextKey.preco never written")
	}
	if !containsSubstr(err.Error(), "preco") {
		t.Errorf("expected error to mention field 'preco', got: %v", err)
	}
	if !containsSubstr(err.Error(), "check") {
		t.Errorf("expected error to mention state 'check', got: %v", err)
	}
}

func TestCompile_ContextKeyRefProducedByMapState(t *testing.T) {
	p := &policy.Policy{
		ID:    "p7",
		Entry: "mapPrices",
		States: []policy.State{
			{
				ID:         "mapPrices",
				Kind:       policy.KindMap,
				ContextKey: "preco",
				Transitions: []policy.Transition{
					{When: "true", To: "checkPrice"},
				},
			},
			{
				ID:   "checkPrice",
				Kind: policy.KindExecution,
				Transitions: []policy.Transition{
					{When: "contextKey.preco > 100", To: "ok"},
				},
				Fallback: "err",
			},
			{ID: "ok", Kind: policy.KindResponse, Status: 200},
			{ID: "err", Kind: policy.KindResponse, Status: 500},
		},
	}
	if _, err := compiler.Compile(p); err != nil {
		t.Fatalf("expected no error when contextKey.preco is produced by mapPrices, got %v", err)
	}
}

func containsSubstr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsAt(s, sub))
}

func containsAt(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
