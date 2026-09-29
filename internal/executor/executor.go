package executor

import (
	"context"
	"encoding/json"
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
)

const maxSteps = 1000

// Result is the output of a successful policy execution.
type Result struct {
	State string `json:"state"`
	Data  any    `json:"data"`
}

// ResultCache stores and retrieves cached apiCall results keyed by call signature.
type ResultCache interface {
	Get(key string) ([]byte, bool)
	Set(key string, val []byte, ttl time.Duration)
}

// Executor runs policy state machines.
type Executor struct {
	client *http.Client
	cache  ResultCache
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
func (e *Executor) Execute(ctx context.Context, art *compiler.Artifact, input any) (Result, error) {
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

		switch s.Kind {
		case policy.KindResponse:
			return Result{State: s.ID, Data: s.Data}, nil

		case policy.KindAPICall:
			if err := e.runAPICall(ctx, s, env); err != nil {
				if s.Fallback != "" {
					currentID = s.Fallback
					continue
				}
				return Result{}, err
			}

		case policy.KindParallel:
			if err := e.runParallel(ctx, s, env); err != nil {
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
// with up to s.MaxConcurrency concurrent goroutines. Results are collected in
// env["contextKey"][s.ContextKey] as a slice (preserving insertion order).
func (e *Executor) runParallel(ctx context.Context, s *policy.State, env map[string]any) error {
	if s.Over == "" {
		return nil
	}

	overProg, err := expr.Compile(s.Over)
	if err != nil {
		return fmt.Errorf("state %q: compile over expression: %w", s.ID, err)
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
		return fmt.Errorf("state %q: decode response body: %w", s.ID, err)
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
