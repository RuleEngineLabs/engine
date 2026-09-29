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

func approvedDraftStore(t *testing.T, name, baseVersion string) *store.PolicyStore {
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
	ps.UpsertDraft(name, baseVersion, p, art)
	if err := ps.ApproveDraft(name); err != nil {
		t.Fatalf("approve: %v", err)
	}
	return ps
}

func newVersionsMux(ps *store.PolicyStore) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /policies/{name}/versions", handlePromote(ps))
	return mux
}

func TestHandlePromote_PatchBump(t *testing.T) {
	ps := approvedDraftStore(t, "ordersPolicy", "1.2.0")

	body := `{"bump":"patch"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/versions", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	newVersionsMux(ps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["version"] != "1.2.1" {
		t.Errorf("expected version=1.2.1, got %q", resp["version"])
	}
	if resp["status"] != "STABLE" {
		t.Errorf("expected status=STABLE, got %q", resp["status"])
	}
}

func TestHandlePromote_MinorBump(t *testing.T) {
	ps := approvedDraftStore(t, "ordersPolicy", "1.2.0")

	body := `{"bump":"minor"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/versions", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	newVersionsMux(ps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["version"] != "1.3.0" {
		t.Errorf("expected version=1.3.0, got %q", resp["version"])
	}
}

func TestHandlePromote_DraftNotApproved(t *testing.T) {
	ps := store.New()
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
	// Draft stays in DRAFT status (not approved)
	ps.UpsertDraft("ordersPolicy", "1.2.0", p, art)

	body := `{"bump":"patch"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/versions", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	newVersionsMux(ps).ServeHTTP(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] != "draft must be approved before promotion" {
		t.Errorf("unexpected error message: %q", resp["error"])
	}
}
