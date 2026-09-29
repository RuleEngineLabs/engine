package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/executor"
	"github.com/RuleEngineLabs/engine/internal/policy"
	"github.com/RuleEngineLabs/engine/internal/sandbox"
)

// buildWritePolicy creates a policy that does a write state and checks the contextKey result.
// The write state (method POST) hits writeURL. A transition checks contextKey["result"] != nil
// to advance to "done" or falls back to "fallback".
func buildWritePolicy(writeURL string) *compiler.Artifact {
	p := &policy.Policy{
		ID:    "wp",
		Entry: "write",
		States: []policy.State{
			{
				ID:         "write",
				Kind:       policy.KindAPICall,
				Method:     http.MethodPost,
				URL:        writeURL,
				ContextKey: "result",
				Transitions: []policy.Transition{
					{When: `contextKey["result"] != nil`, To: "done"},
				},
				Fallback: "fallback",
			},
			{ID: "done", Kind: policy.KindResponse, Data: "ok", Status: 200},
			{ID: "fallback", Kind: policy.KindResponse, Data: "fallback", Status: 200},
		},
	}
	art, _ := compiler.Compile(p)
	return art
}

// buildBlockedWritePolicy creates a policy whose write state has no sandbox config.
// After the write is blocked, a transition always advances to "done".
func buildBlockedWritePolicy(writeURL string) *compiler.Artifact {
	p := &policy.Policy{
		ID:    "bwp",
		Entry: "write",
		States: []policy.State{
			{
				ID:         "write",
				Kind:       policy.KindAPICall,
				Method:     http.MethodPost,
				URL:        writeURL,
				ContextKey: "result",
				Transitions: []policy.Transition{
					{When: `true`, To: "done"},
				},
			},
			{ID: "done", Kind: policy.KindResponse, Data: "blocked", Status: 200},
		},
	}
	art, _ := compiler.Compile(p)
	return art
}

// TestPreview_SandboxWrite_MappingFound verifies that a POST state resolves via sandbox
// and advances normally when a matching mapping exists.
func TestPreview_SandboxWrite_MappingFound(t *testing.T) {
	loader := sandbox.NewMemLoader(
		&sandbox.Mapping{
			Method: "POST",
			Path:   "/orders",
			Status: 201,
			Body:   json.RawMessage(`{"id":"ord-1"}`),
			Headers: map[string]string{"Location": "/orders/ord-1"},
		},
	)
	e := executor.New(executor.WithSandboxLoader(loader))
	art := buildWritePolicy("/orders")

	result, err := e.Preview(t.Context(), art, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.State != "done" {
		t.Fatalf("expected state=done, got %q", result.State)
	}

	// Trace must NOT have blocked=true for the write state
	for _, entry := range result.Trace {
		if entry.StateID == "write" && entry.Blocked {
			t.Fatal("write state must not be blocked when sandbox mapping resolves it")
		}
	}
}

// TestPreview_SandboxWrite_MappingNotFound verifies sandbox_mapping_not_found error.
func TestPreview_SandboxWrite_MappingNotFound(t *testing.T) {
	loader := sandbox.NewMemLoader() // no mappings
	e := executor.New(executor.WithSandboxLoader(loader))
	art := buildWritePolicy("/orders")

	_, err := e.Preview(t.Context(), art, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "sandbox_mapping_not_found") {
		t.Fatalf("expected sandbox_mapping_not_found in error, got: %v", err)
	}
}

// TestPreview_SandboxWrite_SandboxUnavailable verifies sandbox_unavailable error.
func TestPreview_SandboxWrite_SandboxUnavailable(t *testing.T) {
	loader := &alwaysUnavailableLoader{}
	e := executor.New(executor.WithSandboxLoader(loader))
	art := buildWritePolicy("/orders")

	_, err := e.Preview(t.Context(), art, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "sandbox_unavailable") {
		t.Fatalf("expected sandbox_unavailable in error, got: %v", err)
	}
}

// TestPreview_WriteBlocked_NoSandbox verifies legacy behavior: write is blocked without sandbox.
func TestPreview_WriteBlocked_NoSandbox(t *testing.T) {
	e := executor.New() // no sandbox loader
	art := buildBlockedWritePolicy("/orders")

	result, err := e.Preview(t.Context(), art, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.State != "done" {
		t.Fatalf("expected state=done, got %q", result.State)
	}

	// The write state must appear in trace with blocked=true
	var writeEntry *executor.TraceEntry
	for i, entry := range result.Trace {
		if entry.StateID == "write" {
			writeEntry = &result.Trace[i]
			break
		}
	}
	if writeEntry == nil {
		t.Fatal("expected write state in trace")
	}
	if !writeEntry.Blocked {
		t.Fatal("expected write state to be blocked when no sandbox configured")
	}
}

// TestPreview_SandboxRegexBodyMatcher verifies sandbox regex matching on body fields.
func TestPreview_SandboxRegexBodyMatcher(t *testing.T) {
	loader := sandbox.NewMemLoader(
		&sandbox.Mapping{
			Method:   "POST",
			Path:     "/orders",
			Matchers: map[string]string{"orderId": `^TEST-.*`},
			Status:   200,
			Body:     json.RawMessage(`{"matched":true}`),
		},
	)
	e := executor.New(executor.WithSandboxLoader(loader))
	art := buildWritePolicy("/orders")

	// Matching input
	result, err := e.Preview(t.Context(), art, map[string]any{"orderId": "TEST-ABC"})
	if err != nil {
		t.Fatalf("unexpected error with matching input: %v", err)
	}
	if result.State != "done" {
		t.Fatalf("expected done, got %q", result.State)
	}

	// Non-matching input → ErrMappingNotFound
	_, err = e.Preview(t.Context(), art, map[string]any{"orderId": "PROD-XYZ"})
	if err == nil || !strings.Contains(err.Error(), "sandbox_mapping_not_found") {
		t.Fatalf("expected sandbox_mapping_not_found, got %v", err)
	}
}

// TestHandlePreview_SandboxIntegration tests the /preview HTTP handler with sandbox.
func TestHandlePreview_SandboxIntegration(t *testing.T) {
	// The handler currently uses executor.Preview (no sandbox). This test verifies
	// the existing blocked-write behavior via the HTTP handler.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /preview", handlePreview())

	reqBody := `{
		"name":"test","entry":"w",
		"states":[
			{"id":"w","kind":"apiCall","method":"POST","url":"/x","contextKey":"r",
			 "transitions":[{"when":"true","to":"done"}]},
			{"id":"done","kind":"response","data":"ok","status":200}
		],
		"input":{}
	}`
	req := httptest.NewRequest(http.MethodPost, "/preview", strings.NewReader(reqBody))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	trace, _ := resp["trace"].([]any)
	var writeFound bool
	for _, entry := range trace {
		if m, ok := entry.(map[string]any); ok {
			if m["stateId"] == "w" && m["blocked"] == true {
				writeFound = true
			}
		}
	}
	if !writeFound {
		t.Fatalf("expected write state blocked in trace, got trace: %v", trace)
	}
}

// alwaysUnavailableLoader simulates S3 being unreachable.
type alwaysUnavailableLoader struct{}

func (l *alwaysUnavailableLoader) Resolve(_ context.Context, _, _ string, _ map[string]any) (*sandbox.Response, error) {
	return nil, sandbox.ErrSandboxUnavailable
}
