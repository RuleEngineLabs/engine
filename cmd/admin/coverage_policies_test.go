package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/policy"
	"github.com/RuleEngineLabs/engine/internal/store"
)

func newFullPoliciesMux(ps *store.PolicyStore) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /policies", handleCreate(ps))
	mux.HandleFunc("GET /policies", handleList(ps))
	mux.HandleFunc("GET /policies/{id}", handleGet(ps))
	mux.HandleFunc("PUT /policies/{id}", handleUpdate(ps))
	mux.HandleFunc("DELETE /policies/{id}", handleDelete(ps))
	return mux
}

func storeWithTwoPolices(t *testing.T) (*store.PolicyStore, string, string) {
	t.Helper()
	ps := store.New()
	mkPolicy := func(name string) *compiler.Artifact {
		p := &policy.Policy{Name: name, Entry: "s", States: []policy.State{{ID: "s", Kind: policy.KindResponse, Status: 200}}}
		art, err := compiler.Compile(p)
		if err != nil {
			t.Fatalf("compile %s: %v", name, err)
		}
		return art
	}
	id1, _, _ := ps.Create("alpha", "team-a", mkPolicy("alpha"))
	id2, _, _ := ps.Create("beta", "team-b", mkPolicy("beta"))
	return ps, id1, id2
}

func TestHandleList_ReturnsAllPolicies(t *testing.T) {
	ps, _, _ := storeWithTwoPolices(t)
	mux := newFullPoliciesMux(ps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/policies", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp []map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp) != 2 {
		t.Errorf("expected 2 policies, got %d", len(resp))
	}
	for _, p := range resp {
		if p["policyId"] == "" || p["name"] == "" {
			t.Errorf("missing fields in policy: %v", p)
		}
	}
}

func TestHandleList_EmptyStore(t *testing.T) {
	ps := store.New()
	mux := newFullPoliciesMux(ps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/policies", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp []map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp) != 0 {
		t.Errorf("expected empty list, got %d items", len(resp))
	}
}

func TestHandleGet_Success(t *testing.T) {
	ps, id1, _ := storeWithTwoPolices(t)
	mux := newFullPoliciesMux(ps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/policies/"+id1, nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["policyId"] != id1 {
		t.Errorf("expected policyId=%s, got %v", id1, resp["policyId"])
	}
	if resp["name"] != "alpha" {
		t.Errorf("expected name=alpha, got %v", resp["name"])
	}
	if resp["entry"] == nil {
		t.Error("expected entry field in response")
	}
}

func TestHandleGet_NotFound(t *testing.T) {
	ps := store.New()
	mux := newFullPoliciesMux(ps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/policies/does-not-exist", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleUpdate_NotImplemented(t *testing.T) {
	ps := store.New()
	mux := newFullPoliciesMux(ps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/policies/any-id", bytes.NewBufferString("{}"))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", w.Code)
	}
}

func TestHandleDelete_NotImplemented(t *testing.T) {
	ps := store.New()
	mux := newFullPoliciesMux(ps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/policies/any-id", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", w.Code)
	}
}
