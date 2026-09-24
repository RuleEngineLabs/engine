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
