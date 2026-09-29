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

// storeWithPolicy creates a PolicyStore that already has a published policy by name.
func storeWithPolicy(t *testing.T, name string) *store.PolicyStore {
	t.Helper()
	ps := store.New()
	p := &policy.Policy{
		Name:  name,
		Entry: "s",
		States: []policy.State{
			{ID: "s", Kind: policy.KindResponse, Status: 200},
		},
	}
	art, err := compiler.Compile(p)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, _, err := ps.Create(name, "team-alpha", art); err != nil {
		t.Fatalf("create: %v", err)
	}
	return ps
}

func newMetaMux(ps *store.PolicyStore) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /policies/{name}/meta", handleSetMeta(ps))
	mux.HandleFunc("POST /policies/{name}/versions", handlePromote(ps))
	return mux
}

func TestHandlePromote_MajorWithoutCoexistenceWindow(t *testing.T) {
	ps := storeWithPolicy(t, "ordersPolicy")
	p := &policy.Policy{
		Name:  "ordersPolicy",
		Entry: "s",
		States: []policy.State{
			{ID: "s", Kind: policy.KindResponse, Status: 200},
		},
	}
	art, err := compiler.Compile(p)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ps.UpsertDraft("ordersPolicy", "1.0.0", p, art)
	if err := ps.ApproveDraft("ordersPolicy"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	body := `{"bump":"major"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/versions", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	newMetaMux(ps).ServeHTTP(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] != "coexistence_window_not_set" {
		t.Errorf("unexpected error: %q", resp["error"])
	}
}

func TestHandlePromote_MajorWithCoexistenceWindow(t *testing.T) {
	ps := storeWithPolicy(t, "ordersPolicy")

	// Set coexistence window (86400s = 24h)
	metaBody := `{"coexistence_window_seconds":86400}`
	wm := httptest.NewRecorder()
	rm := httptest.NewRequest(http.MethodPatch, "/policies/ordersPolicy/meta", bytes.NewBufferString(metaBody))
	rm.Header.Set("Content-Type", "application/json")
	newMetaMux(ps).ServeHTTP(wm, rm)
	if wm.Code != http.StatusOK {
		t.Fatalf("set meta: expected 200, got %d: %s", wm.Code, wm.Body.String())
	}

	// Prepare approved draft
	p := &policy.Policy{
		Name:  "ordersPolicy",
		Entry: "s",
		States: []policy.State{
			{ID: "s", Kind: policy.KindResponse, Status: 200},
		},
	}
	art, err := compiler.Compile(p)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ps.UpsertDraft("ordersPolicy", "1.2.0", p, art)
	if err := ps.ApproveDraft("ordersPolicy"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Promote major
	body := `{"bump":"major"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/versions", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	newMetaMux(ps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["version"] != "2.0.0" {
		t.Errorf("expected version=2.0.0, got %q", resp["version"])
	}
}

func TestHandleSetMeta_SetsCoexistenceWindow(t *testing.T) {
	ps := storeWithPolicy(t, "ordersPolicy")

	body := `{"coexistence_window_seconds":172800}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/policies/ordersPolicy/meta", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	newMetaMux(ps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["coexistence_window_seconds"] != float64(172800) {
		t.Errorf("expected 172800, got %v", resp["coexistence_window_seconds"])
	}

	// Verify it's stored
	m := ps.GetMeta("ordersPolicy")
	if m == nil || m.CoexistenceWindowSeconds != 172800 {
		t.Errorf("meta not stored correctly: %+v", m)
	}
}
