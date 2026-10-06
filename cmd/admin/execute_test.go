package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RuleEngineLabs/engine/internal/store"
)

func newExecuteServer() *http.ServeMux {
	ps := store.New()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /policies", handleCreate(ps))
	mux.HandleFunc("POST /execute/{id}", handleExecute(ps, nil))
	return mux
}

func createPolicy(t *testing.T, srv *http.ServeMux, body string) string {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies", bytes.NewBufferString(body))
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("createPolicy: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	return resp["policyId"].(string)
}

func TestHandleExecute_DirectPath(t *testing.T) {
	srv := newExecuteServer()
	policyID := createPolicy(t, srv, `{
		"name": "simple",
		"entry": "start",
		"states": [
			{"id": "start", "kind": "execution", "transitions": [{"when": "true", "to": "ok"}]},
			{"id": "ok",    "kind": "response",  "status": 200, "data": {"result": "ok"}}
		]
	}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/execute/"+policyID, bytes.NewBufferString("{}"))
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var res map[string]any
	json.NewDecoder(w.Body).Decode(&res)
	if res["state"] != "ok" {
		t.Errorf("expected state=ok, got %v", res["state"])
	}
	// Fix 5: duration_ms must be present and >= 0 on success.
	dms, ok := res["duration_ms"]
	if !ok {
		t.Error("expected duration_ms in success response")
	}
	if v, _ := dms.(float64); v < 0 {
		t.Errorf("expected duration_ms >= 0, got %v", v)
	}
}

func TestHandleExecute_PolicyNotFound(t *testing.T) {
	srv := newExecuteServer()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/execute/does-not-exist", bytes.NewBufferString("{}"))
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	// Fix 1: Content-Type must be application/json.
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
	// Fix 1 + Fix 5: body has exactly "error" field and no "duration_ms".
	var raw map[string]any
	json.NewDecoder(w.Body).Decode(&raw)
	if raw["error"] != "policy_not_found" {
		t.Errorf("expected error=policy_not_found, got %v", raw["error"])
	}
	if _, hasDms := raw["duration_ms"]; hasDms {
		t.Error("duration_ms must not appear in error responses (404)")
	}
}

func TestHandleExecute_TransitionPriority(t *testing.T) {
	srv := newExecuteServer()
	policyID := createPolicy(t, srv, `{
		"name": "scoring",
		"entry": "check",
		"states": [
			{
				"id": "check", "kind": "execution",
				"transitions": [
					{"when": "input.score >= 90", "to": "approve"},
					{"when": "true",              "to": "reject"}
				]
			},
			{"id": "approve", "kind": "response", "status": 200},
			{"id": "reject",  "kind": "response", "status": 200}
		]
	}`)

	cases := []struct {
		input string
		want  string
	}{
		{`{"score": 95}`, "approve"},
		{`{"score": 50}`, "reject"},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/execute/"+policyID, bytes.NewBufferString(c.input))
		srv.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Fatalf("input=%s: expected 200, got %d", c.input, w.Code)
		}
		var res map[string]any
		json.NewDecoder(w.Body).Decode(&res)
		if res["state"] != c.want {
			t.Errorf("input=%s: expected state=%s, got %v", c.input, c.want, res["state"])
		}
	}
}
