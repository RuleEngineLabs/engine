package policy

// Kind represents the type of a state in a policy graph.
type Kind string

const (
	KindExecution Kind = "execution"
	KindAPICall   Kind = "apiCall"
	KindDBQuery   Kind = "dbQuery"
	KindParallel  Kind = "parallel"
	KindResponse  Kind = "response"
)

// Transition defines a conditional edge between states.
type Transition struct {
	When string `json:"when"`
	To   string `json:"to"`
}

// State is a node in the policy graph.
type State struct {
	ID             string       `json:"id"`
	Kind           Kind         `json:"kind"`
	Transitions    []Transition `json:"transitions,omitempty"`
	Fallback       string       `json:"fallback,omitempty"`
	ContextKey     string       `json:"contextKey,omitempty"`
	MaxConcurrency int          `json:"maxConcurrency,omitempty"`
	Over           string       `json:"over,omitempty"`
	Each           string       `json:"each,omitempty"`
	Status         int          `json:"status,omitempty"`
	Data           any          `json:"data,omitempty"`
}

// Policy is a named, versioned graph of states.
type Policy struct {
	ID      string  `json:"id"`
	Version string  `json:"version"`
	States  []State `json:"states"`
	Entry   string  `json:"entry"`
}
