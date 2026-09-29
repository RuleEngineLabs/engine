package policy

// Kind represents the type of a state in a policy graph.
type Kind string

const (
	KindExecution Kind = "execution"
	KindAPICall   Kind = "apiCall"
	KindDBQuery   Kind = "dbQuery"
	KindParallel  Kind = "parallel"
	KindResponse  Kind = "response"
	KindMap       Kind = "map"
)

// Transition defines a conditional edge between states.
type Transition struct {
	When string `json:"when"`
	To   string `json:"to"`
}

// RetryConfig controls retry behaviour for apiCall states.
type RetryConfig struct {
	MaxAttempts int `json:"maxAttempts"`
}

// CacheConfig controls result caching for apiCall states.
// Only the final post-retry result is cached.
type CacheConfig struct {
	TTLSeconds int `json:"ttlSeconds"`
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
	// apiCall-specific
	URL    string       `json:"url,omitempty"`
	Method string       `json:"method,omitempty"`
	Retry  *RetryConfig `json:"retry,omitempty"`
	Cache  *CacheConfig `json:"cache,omitempty"`
}

// Policy is a named, versioned graph of states.
type Policy struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Version int     `json:"version"`
	States  []State `json:"states"`
	Entry   string  `json:"entry"`
}
