package executor

import (
	"context"
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/policy"
)

const maxSteps = 1000

// Result is the output of a successful policy execution.
type Result struct {
	State string `json:"state"`
	Data  any    `json:"data"`
}

// Execute runs the policy state machine for the given artifact and input.
// It walks the graph from the entry state, evaluating transitions in declaration
// order (first matching transition wins) until a response state is reached.
func Execute(_ context.Context, art *compiler.Artifact, input any) (Result, error) {
	stateIndex := make(map[string]*policy.State, len(art.Policy.States))
	for i := range art.Policy.States {
		stateIndex[art.Policy.States[i].ID] = &art.Policy.States[i]
	}

	env := map[string]any{
		"input":      input,
		"contextKey": map[string]any{},
	}

	currentID := art.Policy.Entry
	for step := 0; step < maxSteps; step++ {
		s, ok := stateIndex[currentID]
		if !ok {
			return Result{}, fmt.Errorf("state %q not found during execution", currentID)
		}

		if s.Kind == policy.KindResponse {
			return Result{State: s.ID, Data: s.Data}, nil
		}

		next, err := evalTransitions(art, s, env)
		if err != nil {
			return Result{}, err
		}
		if next == "" {
			if s.Fallback == "" {
				return Result{}, fmt.Errorf("state %q: no transition matched and no fallback defined", s.ID)
			}
			next = s.Fallback
		}
		currentID = next
	}

	return Result{}, fmt.Errorf("execution exceeded maximum steps (%d)", maxSteps)
}

func evalTransitions(art *compiler.Artifact, s *policy.State, env map[string]any) (string, error) {
	for i, t := range s.Transitions {
		key := fmt.Sprintf("%s:%d", s.ID, i)
		prog, ok := art.Program(key)
		if !ok {
			return "", fmt.Errorf("state %q: compiled program for transition %d not found", s.ID, i)
		}
		out, err := expr.Run(prog, env)
		if err != nil {
			return "", fmt.Errorf("state %q transition %d: %w", s.ID, i, err)
		}
		if matched, ok := out.(bool); ok && matched {
			return t.To, nil
		}
	}
	return "", nil
}
