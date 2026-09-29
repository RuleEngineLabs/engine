package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newPreviewMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /preview", handlePreview())
	return mux
}

func TestHandlePreview_TraceAllStates(t *testing.T) {
	// Three chained execution states → response
	body := `{
		"name": "chain",
		"entry": "s1",
		"states": [
			{"id": "s1", "kind": "execution", "transitions": [{"when": "true", "to": "s2"}]},
			{"id": "s2", "kind": "execution", "transitions": [{"when": "true", "to": "done"}]},
			{"id": "done", "kind": "response", "status": 200}
		],
		"input": {}
	}`

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/preview", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	newPreviewMux().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	trace, ok := resp["trace"].([]any)
	if !ok {
		t.Fatalf("expected trace array, got %T: %v", resp["trace"], resp["trace"])
	}
	if len(trace) != 3 {
		t.Errorf("expected 3 trace entries (s1, s2, done), got %d", len(trace))
	}

	ids := make([]string, len(trace))
	for i, e := range trace {
		m := e.(map[string]any)
		ids[i] = m["stateId"].(string)
	}
	expected := []string{"s1", "s2", "done"}
	for i, want := range expected {
		if ids[i] != want {
			t.Errorf("trace[%d]: expected stateId=%q, got %q", i, want, ids[i])
		}
	}
}

func TestHandlePreview_WriteStateBlocked(t *testing.T) {
	// Policy with a POST apiCall state — should be blocked (preview_blocked_write)
	body := `{
		"name": "write-pol",
		"entry": "call-step",
		"states": [
			{"id": "call-step", "kind": "apiCall", "url": "http://example.com/orders", "method": "POST",
			 "transitions": [{"when": "true", "to": "done"}]},
			{"id": "done", "kind": "response", "status": 201}
		],
		"input": {}
	}`

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/preview", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	newPreviewMux().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	trace, ok := resp["trace"].([]any)
	if !ok || len(trace) == 0 {
		t.Fatalf("expected trace with entries, got %v", resp["trace"])
	}

	first := trace[0].(map[string]any)
	if first["stateId"] != "call-step" {
		t.Errorf("expected first trace entry to be call-step, got %v", first["stateId"])
	}
	if first["blocked"] != true {
		t.Errorf("expected call-step to be blocked, got blocked=%v", first["blocked"])
	}
	if first["duration_ms"] != float64(0) {
		t.Errorf("expected blocked state duration_ms=0, got %v", first["duration_ms"])
	}
}

func TestHandlePreview_InvalidPolicy(t *testing.T) {
	// Unsupported kind → compile error → 422
	body := `{
		"name": "bad",
		"entry": "s",
		"states": [
			{"id": "s", "kind": "dbQuery", "transitions": [{"when": "true", "to": "end"}]},
			{"id": "end", "kind": "response", "status": 200}
		],
		"input": {}
	}`

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/preview", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	newPreviewMux().ServeHTTP(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] == "" {
		t.Error("expected error message in body")
	}
}

func TestHandlePreview_InvalidJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/preview", bytes.NewBufferString("{bad"))
	newPreviewMux().ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
