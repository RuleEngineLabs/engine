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
	// programs holds pre-compiled expr programs keyed by state ID + transition index.
	programs map[string]*vm.Program
}

// Compile validates and compiles a policy into an Artifact.
// Rejects policies with unsupported kinds, duplicate state IDs, reserved contextKeys,
// cyclic graphs, undeclared contextKey field references, and invalid expressions.
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

	adj := make(map[string][]string, len(p.States))
	seen := make(map[string]struct{}, len(p.States))

	for _, s := range p.States {
		if _, dup := seen[s.ID]; dup {
			return fmt.Errorf("duplicate state id %q", s.ID)
		}
		seen[s.ID] = struct{}{}
		if !isSupportedKind(s.Kind) {
			return fmt.Errorf("state %q: unsupported kind %q", s.ID, s.Kind)
		}
		if s.ContextKey != "" {
			if _, reserved := reservedContextKeys[s.ContextKey]; reserved {
				return fmt.Errorf("contextKey %q is reserved", s.ContextKey)
			}
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

func isSupportedKind(k policy.Kind) bool {
	switch k {
	case policy.KindExecution, policy.KindAPICall, policy.KindDBQuery,
		policy.KindParallel, policy.KindResponse, policy.KindMap:
		return true
	}
	return false
}
