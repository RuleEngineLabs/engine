package compiler

import (
	"fmt"
	"regexp"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/RuleEngineLabs/engine/internal/policy"
)

// reservedContextKeys are contextKey values the runtime owns and cannot be redefined.
var reservedContextKeys = map[string]struct{}{
	"input": {},
}

// contextKeyRef matches contextKey.fieldName in expressions.
var contextKeyRef = regexp.MustCompile(`\bcontextKey\.(\w+)\b`)

// Artifact is a compiled policy ready for execution.
type Artifact struct {
	Policy *policy.Policy

	// stateIndex is pre-built for O(1) state lookup by ID, eliminating
	// the per-call rebuild in Execute/Preview.
	stateIndex map[string]*policy.State

	// transitionPrograms holds compiled expressions per state, ordered by
	// transition index. Eliminates fmt.Sprintf("%s:%d") in the hot loop.
	transitionPrograms map[string][]*vm.Program

	// overPrograms holds pre-compiled "over" expressions for parallel states,
	// eliminating expr.Compile calls during execution.
	overPrograms map[string]*vm.Program

	// programs is kept for backward-compatible Program(key) access.
	programs map[string]*vm.Program
}

// State returns the compiled state by ID, or (nil, false) if not found.
func (a *Artifact) State(id string) (*policy.State, bool) {
	s, ok := a.stateIndex[id]
	return s, ok
}

// TransitionPrograms returns the pre-compiled expression programs for a state's
// transitions, in order. The returned slice must not be modified.
func (a *Artifact) TransitionPrograms(stateID string) ([]*vm.Program, bool) {
	p, ok := a.transitionPrograms[stateID]
	return p, ok
}

// OverProgram returns the pre-compiled "over" expression for a parallel state.
func (a *Artifact) OverProgram(stateID string) (*vm.Program, bool) {
	p, ok := a.overPrograms[stateID]
	return p, ok
}

// Program returns the compiled expression program for a state transition.
// Key format: "stateID:transitionIndex". Kept for compatibility.
func (a *Artifact) Program(key string) (*vm.Program, bool) {
	p, ok := a.programs[key]
	return p, ok
}

// Compile validates and compiles a policy into an Artifact.
// Rejects policies with unsupported kinds, duplicate state IDs, reserved contextKeys,
// cyclic graphs, undeclared contextKey field references, and invalid expressions.
func Compile(p *policy.Policy) (*Artifact, error) {
	if err := validate(p); err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}

	stateIndex := make(map[string]*policy.State, len(p.States))
	programs := make(map[string]*vm.Program, len(p.States))
	transitionPrograms := make(map[string][]*vm.Program, len(p.States))
	overPrograms := make(map[string]*vm.Program)

	for i := range p.States {
		s := &p.States[i]
		stateIndex[s.ID] = s

		progs := make([]*vm.Program, 0, len(s.Transitions))
		for j, t := range s.Transitions {
			key := fmt.Sprintf("%s:%d", s.ID, j)
			prog, err := expr.Compile(t.When)
			if err != nil {
				return nil, fmt.Errorf("compile state %q transition %d: %w", s.ID, j, err)
			}
			programs[key] = prog
			progs = append(progs, prog)
		}
		transitionPrograms[s.ID] = progs

		// Pre-compile parallel "over" expressions so execution never calls expr.Compile.
		if s.Kind == policy.KindParallel && s.Over != "" {
			prog, err := expr.Compile(s.Over)
			if err != nil {
				return nil, fmt.Errorf("compile state %q over expression: %w", s.ID, err)
			}
			overPrograms[s.ID] = prog
		}
	}

	return &Artifact{
		Policy:             p,
		stateIndex:         stateIndex,
		transitionPrograms: transitionPrograms,
		overPrograms:       overPrograms,
		programs:           programs,
	}, nil
}

func validate(p *policy.Policy) error {
	if p.Entry == "" {
		return fmt.Errorf("entry state is required")
	}

	adj := make(map[string][]string, len(p.States))
	seen := make(map[string]struct{}, len(p.States))

	for _, s := range p.States {
		if _, dup := seen[s.ID]; dup {
			return fmt.Errorf("duplicate state id %q", s.ID)
		}
		seen[s.ID] = struct{}{}
		if phaseErr := kindPhaseError(s.Kind); phaseErr != nil {
			return fmt.Errorf("state %q: %w", s.ID, phaseErr)
		}
		if !isSupportedKind(s.Kind) {
			return fmt.Errorf("state %q: unsupported kind %q", s.ID, s.Kind)
		}
		if s.ContextKey != "" {
			if _, reserved := reservedContextKeys[s.ContextKey]; reserved {
				return fmt.Errorf("contextKey %q is reserved", s.ContextKey)
			}
		}
		if s.Kind == policy.KindParallel && s.MaxConcurrency <= 0 {
			return fmt.Errorf("state %q: parallel state requires maxConcurrency > 0", s.ID)
		}
		neighbors := make([]string, 0, len(s.Transitions)+1)
		for _, t := range s.Transitions {
			neighbors = append(neighbors, t.To)
		}
		if s.Fallback != "" {
			neighbors = append(neighbors, s.Fallback)
		}
		adj[s.ID] = neighbors
	}

	if _, ok := seen[p.Entry]; !ok {
		return fmt.Errorf("entry state %q not found", p.Entry)
	}

	if cycle := detectCycle(p.Entry, adj); cycle != "" {
		return fmt.Errorf("cycle detected: %s", cycle)
	}

	if err := checkContextKeyRefs(p); err != nil {
		return err
	}

	return nil
}

// checkContextKeyRefs ensures every contextKey.X reference in transition expressions
// is produced by at least one state in the policy via its ContextKey field.
func checkContextKeyRefs(p *policy.Policy) error {
	produced := make(map[string]struct{}, len(p.States))
	for _, s := range p.States {
		if s.ContextKey != "" {
			produced[s.ContextKey] = struct{}{}
		}
	}

	for _, s := range p.States {
		for _, t := range s.Transitions {
			for _, m := range contextKeyRef.FindAllStringSubmatch(t.When, -1) {
				field := m[1]
				if _, ok := produced[field]; !ok {
					return fmt.Errorf("state %q references contextKey.%s which is never written by any state", s.ID, field)
				}
			}
		}
	}
	return nil
}

// detectCycle runs DFS from start and returns a description of the first cycle found.
func detectCycle(start string, adj map[string][]string) string {
	color := make(map[string]int, len(adj)) // 0=white,1=gray,2=black
	path := make([]string, 0, len(adj))
	var dfs func(n string) string
	dfs = func(n string) string {
		color[n] = 1
		path = append(path, n)
		for _, nb := range adj[n] {
			if color[nb] == 1 {
				for i, v := range path {
					if v == nb {
						return fmt.Sprintf("%v → %s", path[i:], nb)
					}
				}
				return nb
			}
			if color[nb] == 0 {
				if msg := dfs(nb); msg != "" {
					return msg
				}
			}
		}
		path = path[:len(path)-1]
		color[n] = 2
		return ""
	}
	return dfs(start)
}

// kindPhaseError returns an error for kinds that are defined but not yet
// available in the current phase, so callers get a phase-specific message
// instead of a generic "unsupported" one.
func kindPhaseError(k policy.Kind) error {
	switch k {
	case policy.KindDBQuery:
		return fmt.Errorf("kind %q not supported in current phase", k)
	}
	return nil
}

func isSupportedKind(k policy.Kind) bool {
	switch k {
	case policy.KindExecution, policy.KindAPICall,
		policy.KindParallel, policy.KindResponse, policy.KindMap:
		return true
	}
	return false
}
