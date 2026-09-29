package executor_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/executor"
	"github.com/RuleEngineLabs/engine/internal/policy"
)

func mustCompile(t *testing.T, p *policy.Policy) *compiler.Artifact {
	t.Helper()
	art, err := compiler.Compile(p)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return art
}

func TestExecute_DirectPathToResponse(t *testing.T) {
	p := &policy.Policy{
		ID:    "p1",
		Entry: "start",
		States: []policy.State{
			{
				ID:   "start",
				Kind: policy.KindExecution,
				Transitions: []policy.Transition{
					{When: "true", To: "ok"},
				},
			},
			{ID: "ok", Kind: policy.KindResponse, Status: 200, Data: map[string]any{"result": "ok"}},
		},
	}
	art := mustCompile(t, p)

	res, err := executor.Execute(context.Background(), art, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.State != "ok" {
		t.Errorf("expected state=ok, got %q", res.State)
	}
	data, ok := res.Data.(map[string]any)
	if !ok || data["result"] != "ok" {
		t.Errorf("unexpected data: %v", res.Data)
	}
}

func TestExecute_TransitionPriority_FirstTrueWins(t *testing.T) {
	p := &policy.Policy{
		ID:    "p2",
		Entry: "check",
		States: []policy.State{
			{
				ID:   "check",
				Kind: policy.KindExecution,
				Transitions: []policy.Transition{
					{When: "input.score >= 90", To: "approve"},
					{When: "true", To: "reject"},
				},
			},
			{ID: "approve", Kind: policy.KindResponse, Status: 200, Data: "approved"},
			{ID: "reject", Kind: policy.KindResponse, Status: 200, Data: "rejected"},
		},
	}
	art := mustCompile(t, p)

	res, err := executor.Execute(context.Background(), art, map[string]any{"score": 95})
	if err != nil {
		t.Fatalf("execute score=95: %v", err)
	}
	if res.State != "approve" {
		t.Errorf("score=95: expected approve, got %q", res.State)
	}

	res, err = executor.Execute(context.Background(), art, map[string]any{"score": 50})
	if err != nil {
		t.Fatalf("execute score=50: %v", err)
	}
	if res.State != "reject" {
		t.Errorf("score=50: expected reject, got %q", res.State)
	}
}

func TestExecute_FallbackOnNoMatch(t *testing.T) {
	p := &policy.Policy{
		ID:    "p3",
		Entry: "check",
		States: []policy.State{
			{
				ID:       "check",
				Kind:     policy.KindExecution,
				Fallback: "err",
				Transitions: []policy.Transition{
					{When: "input.score >= 90", To: "ok"},
				},
			},
			{ID: "ok", Kind: policy.KindResponse, Status: 200},
			{ID: "err", Kind: policy.KindResponse, Status: 500},
		},
	}
	art := mustCompile(t, p)

	res, err := executor.Execute(context.Background(), art, map[string]any{"score": 50})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.State != "err" {
		t.Errorf("expected fallback to err, got %q", res.State)
	}
}

func TestExecute_APICall_ResultInContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/42" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 42, "active": true})
	}))
	defer srv.Close()

	p := &policy.Policy{
		ID:    "p4",
		Entry: "fetchUser",
		States: []policy.State{
			{
				ID:         "fetchUser",
				Kind:       policy.KindAPICall,
				URL:        srv.URL + "/users/{input.id}",
				Method:     "GET",
				ContextKey: "user",
				Transitions: []policy.Transition{
					{When: "contextKey.user.active == true", To: "approve"},
				},
				Fallback: "reject",
			},
			{ID: "approve", Kind: policy.KindResponse, Status: 200, Data: "approved"},
			{ID: "reject", Kind: policy.KindResponse, Status: 200, Data: "rejected"},
		},
	}
	art := mustCompile(t, p)
	ex := executor.New(executor.WithHTTPClient(srv.Client()))

	res, err := ex.Execute(context.Background(), art, map[string]any{"id": 42})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.State != "approve" {
		t.Errorf("expected approve, got %q", res.State)
	}
}

func TestExecute_APICall_RetryAndCache(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	p := &policy.Policy{
		ID:    "p5",
		Entry: "fetch",
		States: []policy.State{
			{
				ID:         "fetch",
				Kind:       policy.KindAPICall,
				URL:        srv.URL + "/data",
				ContextKey: "result",
				Retry:      &policy.RetryConfig{MaxAttempts: 3},
				Cache:      &policy.CacheConfig{TTLSeconds: 60},
				Transitions: []policy.Transition{
					{When: "true", To: "ok"},
				},
			},
			{ID: "ok", Kind: policy.KindResponse, Status: 200},
		},
	}
	art := mustCompile(t, p)

	cache := executor.NewMemCache()
	ex := executor.New(executor.WithHTTPClient(srv.Client()), executor.WithCache(cache))

	// First execution: 3 HTTP calls (2 failures + 1 success), result cached
	res, err := ex.Execute(context.Background(), art, nil)
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	if res.State != "ok" {
		t.Errorf("expected ok, got %q", res.State)
	}
	if callCount.Load() != 3 {
		t.Errorf("expected 3 HTTP calls, got %d", callCount.Load())
	}

	// Second execution: result from cache, no new HTTP calls
	res, err = ex.Execute(context.Background(), art, nil)
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}
	if res.State != "ok" {
		t.Errorf("expected ok from cache, got %q", res.State)
	}
	if callCount.Load() != 3 {
		t.Errorf("expected no new HTTP calls, count still %d", callCount.Load())
	}
}
