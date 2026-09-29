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
	mux.HandleFunc("POST /execute/{id}", handleExecute(ps))
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
}

func TestHandleExecute_PolicyNotFound(t *testing.T) {
	srv := newExecuteServer()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/execute/does-not-exist", bytes.NewBufferString("{}"))
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}

	var res map[string]string
	json.NewDecoder(w.Body).Decode(&res)
	if res["error"] != "policy_not_found" {
		t.Errorf("expected error=policy_not_found, got %v", res["error"])
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
