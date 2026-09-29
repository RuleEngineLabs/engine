package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RuleEngineLabs/engine/internal/store"
)

func newTestServer() *http.ServeMux {
	ps := store.New()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /policies", handleCreate(ps))
	mux.HandleFunc("GET /policies", handleList(ps))
	mux.HandleFunc("GET /policies/{id}", handleGet(ps))
	return mux
}

func TestHandleCreate_OK(t *testing.T) {
	body := `{
		"name": "checkout",
		"entry": "validate",
		"states": [
			{"id": "validate", "kind": "execution", "fallback": "err"},
			{"id": "err", "kind": "response", "status": 500},
			{"id": "ok",  "kind": "response", "status": 201}
		]
	}`

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")

	newTestServer().ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["policyId"] == "" {
		t.Error("expected non-empty policyId")
	}
	if resp["version"] != float64(1) {
		t.Errorf("expected version=1, got %v", resp["version"])
	}
}

func TestHandleCreate_ReservedContextKey(t *testing.T) {
	body := `{
		"name": "bad",
		"entry": "s",
		"states": [
			{"id": "s", "kind": "execution", "contextKey": "input", "fallback": "end"},
			{"id": "end", "kind": "response", "status": 200}
		]
	}`

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies", bytes.NewBufferString(body))

	newTestServer().ServeHTTP(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", w.Code)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] == "" {
		t.Error("expected error message in body")
	}
}

func TestHandleCreate_CyclicGraph(t *testing.T) {
	body := `{
		"name": "cyclic",
		"entry": "a",
		"states": [
			{"id": "a", "kind": "execution", "transitions": [{"when": "true", "to": "b"}]},
			{"id": "b", "kind": "execution", "transitions": [{"when": "true", "to": "a"}]}
		]
	}`

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies", bytes.NewBufferString(body))

	newTestServer().ServeHTTP(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", w.Code)
	}
}

func TestHandleCreate_InvalidJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies", bytes.NewBufferString("{bad json"))

	newTestServer().ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
