package policy_test

import (
	"testing"

	"github.com/RuleEngineLabs/engine/internal/policy"
)

func TestKindConstants(t *testing.T) {
	cases := []struct {
		kind policy.Kind
		want string
	}{
		{policy.KindExecution, "execution"},
		{policy.KindAPICall, "apiCall"},
		{policy.KindDBQuery, "dbQuery"},
		{policy.KindParallel, "parallel"},
		{policy.KindResponse, "response"},
	}
	for _, c := range cases {
		if string(c.kind) != c.want {
			t.Errorf("Kind %q: got %q, want %q", c.kind, string(c.kind), c.want)
		}
	}
}

func TestStateDefaults(t *testing.T) {
	s := policy.State{ID: "start", Kind: policy.KindExecution}
	if s.ID != "start" {
		t.Errorf("expected ID=start, got %q", s.ID)
	}
	if len(s.Transitions) != 0 {
		t.Errorf("expected empty transitions")
	}
}

func TestPolicyEntry(t *testing.T) {
	p := policy.Policy{
		ID:      "p1",
		Version: "1",
		Entry:   "start",
		States: []policy.State{
			{ID: "start", Kind: policy.KindExecution, Fallback: "err"},
			{ID: "err", Kind: policy.KindResponse, Status: 500},
		},
	}
	if p.Entry != "start" {
		t.Errorf("expected entry=start")
	}
	if len(p.States) != 2 {
		t.Errorf("expected 2 states")
	}
}
