package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/expr-lang/expr"
	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/policy"
	"github.com/RuleEngineLabs/engine/internal/sandbox"
)

const maxSteps = 1000

// TraceEntry records one visited state during execution.
type TraceEntry struct {
	StateID    string `json:"stateId"`
	DurationMs int64  `json:"duration_ms"`
	Blocked    bool   `json:"blocked,omitempty"`
}

// Result is the output of a successful policy execution.
type Result struct {
	State  string       `json:"state"`
	Data   any          `json:"data"`
	Trace  []TraceEntry `json:"trace,omitempty"`
	Status int          `json:"-"` // HTTP status from the response state; 0 means use 200
}

// ResultCache stores and retrieves cached apiCall results keyed by call signature.
type ResultCache interface {
	Get(key string) ([]byte, bool)
	Set(key string, val []byte, ttl time.Duration)
}

// Executor runs policy state machines.
type Executor struct {
	client  *http.Client
	cache   ResultCache
	sandbox sandbox.Loader
}

// Option configures an Executor.
type Option func(*Executor)

// WithHTTPClient overrides the HTTP client used for apiCall states.
func WithHTTPClient(c *http.Client) Option {
	return func(e *Executor) { e.client = c }
}

// WithCache provides a ResultCache for apiCall states.
func WithCache(c ResultCache) Option {
	return func(e *Executor) { e.cache = c }
}

// WithSandboxLoader enables sandbox write simulation in Preview mode.
// When set, write states in /preview resolve against the loader instead of being blocked.
func WithSandboxLoader(l sandbox.Loader) Option {
	return func(e *Executor) { e.sandbox = l }
}

// New creates an Executor with optional configuration.
func New(opts ...Option) *Executor {
	e := &Executor{
		client: http.DefaultClient,
		cache:  &noopCache{},
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

var defaultExecutor = New()

// Execute runs the policy using the default executor.
func Execute(ctx context.Context, art *compiler.Artifact, input any) (Result, error) {
	return defaultExecutor.Execute(ctx, art, input)
}

// Execute runs the policy state machine for the given artifact and input.
// Uses pre-built stateIndex and transitionPrograms from the Artifact to eliminate
// per-call allocations and fmt.Sprintf in the hot loop.
func (e *Executor) Execute(ctx context.Context, art *compiler.Artifact, input any) (Result, error) {
	env := map[string]any{
		"input":      coerceISODates(input),
		"contextKey": map[string]any{},
	}

	currentID := art.Policy.Entry
	for step := 0; step < maxSteps; step++ {
		s, ok := art.State(currentID)
		if !ok {
			return Result{}, fmt.Errorf("state %q not found during execution", currentID)
		}

		switch s.Kind {
		case policy.KindResponse:
			return Result{State: s.ID, Status: s.Status, Data: s.Data}, nil

		case policy.KindAPICall:
			if err := e.runAPICall(ctx, s, env); err != nil {
				if s.Fallback != "" {
					currentID = s.Fallback
					continue
				}
				return Result{}, err
			}

		case policy.KindParallel:
			if err := e.runParallel(ctx, art, s, env); err != nil {
				if s.Fallback != "" {
					currentID = s.Fallback
					continue
				}
				return Result{}, err
			}
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

// Preview runs the policy in preview mode: traces every visited state and blocks
// write apiCall states (any method other than GET) without executing them.
// The blocked state's contextKey is set to "preview_blocked_write" so downstream
// expressions that reference it still receive a defined value.
func Preview(ctx context.Context, art *compiler.Artifact, input any) (Result, error) {
	return defaultExecutor.Preview(ctx, art, input)
}

// Preview is the Executor-level entry point for preview mode.
func (e *Executor) Preview(ctx context.Context, art *compiler.Artifact, input any) (Result, error) {
	env := map[string]any{
		"input":      coerceISODates(input),
		"contextKey": map[string]any{},
	}

	var trace []TraceEntry
	currentID := art.Policy.Entry
	for step := 0; step < maxSteps; step++ {
		s, ok := art.State(currentID)
		if !ok {
			return Result{}, fmt.Errorf("state %q not found during execution", currentID)
		}

		start := time.Now()

		switch s.Kind {
		case policy.KindResponse:
			trace = append(trace, TraceEntry{StateID: s.ID, DurationMs: time.Since(start).Milliseconds()})
			return Result{State: s.ID, Status: s.Status, Data: s.Data, Trace: trace}, nil

		case policy.KindAPICall:
			method := s.Method
			if method == "" {
				method = http.MethodGet
			}
			if isWriteMethod(method) {
				if e.sandbox != nil {
					// Sandbox mode: resolve the write against the mapping store.
					sandboxResp, err := e.resolveSandbox(ctx, s, method, env)
					if err != nil {
						return Result{}, err
					}
					if s.ContextKey != "" {
						env["contextKey"].(map[string]any)[s.ContextKey] = sandboxResp
					}
					trace = append(trace, TraceEntry{StateID: s.ID, DurationMs: time.Since(start).Milliseconds()})
				} else {
					// No sandbox loader: block write and mark contextKey as preview_blocked_write.
					if s.ContextKey != "" {
						env["contextKey"].(map[string]any)[s.ContextKey] = "preview_blocked_write"
					}
					trace = append(trace, TraceEntry{StateID: s.ID, DurationMs: 0, Blocked: true})
				}
			} else {
				if err := e.runAPICall(ctx, s, env); err != nil {
					if s.Fallback != "" {
						trace = append(trace, TraceEntry{StateID: s.ID, DurationMs: time.Since(start).Milliseconds()})
						currentID = s.Fallback
						continue
					}
					return Result{}, err
				}
				trace = append(trace, TraceEntry{StateID: s.ID, DurationMs: time.Since(start).Milliseconds()})
			}

		case policy.KindParallel:
			if err := e.runParallel(ctx, art, s, env); err != nil {
				if s.Fallback != "" {
					trace = append(trace, TraceEntry{StateID: s.ID, DurationMs: time.Since(start).Milliseconds()})
					currentID = s.Fallback
					continue
				}
				return Result{}, err
			}
			trace = append(trace, TraceEntry{StateID: s.ID, DurationMs: time.Since(start).Milliseconds()})

		default:
			trace = append(trace, TraceEntry{StateID: s.ID, DurationMs: time.Since(start).Milliseconds()})
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

// resolveSandbox calls the sandbox Loader for a write state in preview mode.
func (e *Executor) resolveSandbox(ctx context.Context, s *policy.State, method string, env map[string]any) (any, error) {
	var body map[string]any
	if input, ok := env["input"]; ok {
		if m, ok := input.(map[string]any); ok {
			body = m
		}
	}

	resp, err := e.sandbox.Resolve(ctx, method, s.URL, body)
	if err != nil {
		if errors.Is(err, sandbox.ErrMappingNotFound) {
			return nil, fmt.Errorf("state %q: %w", s.ID, sandbox.ErrMappingNotFound)
		}
		return nil, fmt.Errorf("state %q: %w", s.ID, sandbox.ErrSandboxUnavailable)
	}

	if resp.Body == nil {
		return nil, nil
	}
	var parsed any
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		return nil, fmt.Errorf("state %q: sandbox response decode: %w", s.ID, err)
	}
	return parsed, nil
}

func isWriteMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

var urlTemplateRe = regexp.MustCompile(`\{([^}]+)\}`)

// runAPICall executes the HTTP call, applying retry and caching only the final result.
func (e *Executor) runAPICall(ctx context.Context, s *policy.State, env map[string]any) error {
	resolvedURL := resolveTemplate(s.URL, env)
	method := s.Method
	if method == "" {
		method = http.MethodGet
	}

	cacheKey := s.ID + ":" + method + ":" + resolvedURL
	if cached, ok := e.cache.Get(cacheKey); ok {
		return storeResult(s, cached, env)
	}

	maxAttempts := 1
	if s.Retry != nil && s.Retry.MaxAttempts > 1 {
		maxAttempts = s.Retry.MaxAttempts
	}

	var (
		lastBody []byte
		lastErr  error
	)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, resolvedURL, nil)
		if err != nil {
			return fmt.Errorf("state %q: build request: %w", s.ID, err)
		}
		resp, err := e.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		lastBody = body
		lastErr = nil
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			break
		}
		lastErr = fmt.Errorf("state %q: HTTP %d", s.ID, resp.StatusCode)
	}

	if lastErr != nil {
		return lastErr
	}

	if s.Cache != nil && s.Cache.TTLSeconds > 0 {
		e.cache.Set(cacheKey, lastBody, time.Duration(s.Cache.TTLSeconds)*time.Second)
	}

	return storeResult(s, lastBody, env)
}

// runParallel evaluates s.Over to get an item array, then processes each item
// with up to s.MaxConcurrency concurrent goroutines. Uses pre-compiled over
// expression from the Artifact instead of calling expr.Compile at runtime.
func (e *Executor) runParallel(ctx context.Context, art *compiler.Artifact, s *policy.State, env map[string]any) error {
	if s.Over == "" {
		return nil
	}

	overProg, ok := art.OverProgram(s.ID)
	if !ok {
		return fmt.Errorf("state %q: over program not found in artifact", s.ID)
	}
	raw, err := expr.Run(overProg, env)
	if err != nil {
		return fmt.Errorf("state %q: evaluate over expression: %w", s.ID, err)
	}
	items, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("state %q: over expression must return an array", s.ID)
	}
	if len(items) == 0 {
		if s.ContextKey != "" {
			env["contextKey"].(map[string]any)[s.ContextKey] = []any{}
		}
		return nil
	}

	sem := make(chan struct{}, s.MaxConcurrency)
	results := make([]any, len(items))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i, item := range items {
		wg.Add(1)
		go func(idx int, it any) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			mu.Lock()
			hasErr := firstErr != nil
			mu.Unlock()
			if hasErr {
				return
			}

			results[idx] = it
		}(i, item)
	}
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}

	if s.ContextKey != "" {
		env["contextKey"].(map[string]any)[s.ContextKey] = results
	}
	return nil
}

func storeResult(s *policy.State, body []byte, env map[string]any) error {
	if s.ContextKey == "" {
		return nil
	}
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("state %q: decode response body: %w", s.ContextKey, err)
	}
	env["contextKey"].(map[string]any)[s.ContextKey] = parsed
	return nil
}

// resolveTemplate replaces {top.field} patterns in tmpl with values from env.
func resolveTemplate(tmpl string, env map[string]any) string {
	return urlTemplateRe.ReplaceAllStringFunc(tmpl, func(m string) string {
		path := m[1 : len(m)-1]
		parts := strings.SplitN(path, ".", 2)
		if len(parts) != 2 {
			return m
		}
		top, ok := env[parts[0]]
		if !ok {
			return m
		}
		if topMap, ok := top.(map[string]any); ok {
			if v, ok := topMap[parts[1]]; ok {
				return fmt.Sprintf("%v", v)
			}
		}
		return m
	})
}

// evalTransitions uses pre-compiled programs from the Artifact, eliminating
// fmt.Sprintf key construction in the hot loop.
func evalTransitions(art *compiler.Artifact, s *policy.State, env map[string]any) (string, error) {
	progs, _ := art.TransitionPrograms(s.ID)
	for i, prog := range progs {
		out, err := expr.Run(prog, env)
		if err != nil {
			return "", fmt.Errorf("state %q transition %d: %w", s.ID, i, err)
		}
		if matched, ok := out.(bool); ok && matched {
			return s.Transitions[i].To, nil
		}
	}
	return "", nil
}

// isoDateFormats are the ISO 8601 variants tried in order when coercing strings to time.Time.
var isoDateFormats = []string{time.RFC3339, time.DateOnly}

// containsDates reports whether v (or any value nested within it) is an ISO date
// string. It scans without allocating — used as a fast gate before coerceISODates
// commits to building a new map/slice copy.
func containsDates(v any) bool {
	switch val := v.(type) {
	case string:
		for _, layout := range isoDateFormats {
			if _, err := time.Parse(layout, val); err == nil {
				return true
			}
		}
		return false
	case map[string]any:
		for _, child := range val {
			if containsDates(child) {
				return true
			}
		}
		return false
	case []any:
		for _, child := range val {
			if containsDates(child) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// coerceISODates recursively walks v and converts ISO 8601 strings to time.Time.
// Fast path: if the subtree contains no ISO date strings, the original value is
// returned with zero allocations. A map or slice is only copied when at least one
// date string is found (two-pass: containsDates scan + coerce pass).
func coerceISODates(v any) any {
	switch val := v.(type) {
	case string:
		for _, layout := range isoDateFormats {
			if t, err := time.Parse(layout, val); err == nil {
				return t
			}
		}
		return val
	case map[string]any:
		if !containsDates(val) {
			return val // fast path: no dates in subtree, zero allocations
		}
		out := make(map[string]any, len(val))
		for k, child := range val {
			out[k] = coerceISODates(child)
		}
		return out
	case []any:
		if !containsDates(val) {
			return val // fast path: no dates in subtree, zero allocations
		}
		out := make([]any, len(val))
		for i, child := range val {
			out[i] = coerceISODates(child)
		}
		return out
	default:
		return v
	}
}

// MemCache is a simple in-memory ResultCache with TTL eviction.
type MemCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	value   []byte
	expires time.Time
}

// NewMemCache creates a MemCache.
func NewMemCache() *MemCache {
	return &MemCache{entries: make(map[string]cacheEntry)}
}

func (c *MemCache) Get(key string) ([]byte, bool) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.value, true
}

func (c *MemCache) Set(key string, val []byte, ttl time.Duration) {
	c.mu.Lock()
	c.entries[key] = cacheEntry{value: val, expires: time.Now().Add(ttl)}
	c.mu.Unlock()
}

type noopCache struct{}

func (noopCache) Get(string) ([]byte, bool)         { return nil, false }
func (noopCache) Set(string, []byte, time.Duration) {}
