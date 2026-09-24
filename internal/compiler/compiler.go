package compiler

import (
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/RuleEngineLabs/engine/internal/policy"
)

// Artifact is a compiled policy ready for execution.
type Artifact struct {
	Policy *policy.Policy
	// programs holds pre-compiled expr programs keyed by state ID + transition index.
	programs map[string]*vm.Program
}

// Compile validates and compiles a policy into an Artifact.
// Rejects policies with unsupported kinds, duplicate state IDs, and invalid expressions.
func Compile(p *policy.Policy) (*Artifact, error) {
	if err := validate(p); err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}

	programs := make(map[string]*vm.Program, len(p.States))
	for _, s := range p.States {
		for i, t := range s.Transitions {
			key := fmt.Sprintf("%s:%d", s.ID, i)
			prog, err := expr.Compile(t.When)
			if err != nil {
				return nil, fmt.Errorf("compile state %q transition %d: %w", s.ID, i, err)
			}
			programs[key] = prog
		}
	}

	return &Artifact{Policy: p, programs: programs}, nil
}

func validate(p *policy.Policy) error {
	if p.Entry == "" {
		return fmt.Errorf("entry state is required")
	}
	seen := make(map[string]struct{}, len(p.States))
	for _, s := range p.States {
		if _, dup := seen[s.ID]; dup {
			return fmt.Errorf("duplicate state id %q", s.ID)
		}
		seen[s.ID] = struct{}{}
		if !isSupportedKind(s.Kind) {
			return fmt.Errorf("state %q: unsupported kind %q", s.ID, s.Kind)
		}
	}
	if _, ok := seen[p.Entry]; !ok {
		return fmt.Errorf("entry state %q not found", p.Entry)
	}
	return nil
}

func isSupportedKind(k policy.Kind) bool {
	switch k {
	case policy.KindExecution, policy.KindAPICall, policy.KindDBQuery,
		policy.KindParallel, policy.KindResponse:
		return true
	}
	return false
}
