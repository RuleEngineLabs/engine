package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/policy"
	"github.com/RuleEngineLabs/engine/internal/store"
)

func newSearchMux(ps *store.PolicyStore) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /policies/{name}/versions", handleListVersions(ps))
	mux.HandleFunc("GET /policies/{name}/versions/{version}", handleGetVersion(ps))
	mux.HandleFunc("POST /policies/{name}/versions", handlePromote(ps))
	return mux
}

func storeWithDraft(t *testing.T, name string) *store.PolicyStore {
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
	ps.UpsertDraft(name, "1.0.0", p, art)
	return ps
}

func TestHandleListVersions_DraftVisible(t *testing.T) {
	ps := storeWithDraft(t, "ordersPolicy")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/policies/ordersPolicy/versions", nil)
	newSearchMux(ps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp []map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp) != 1 {
		t.Fatalf("expected 1 version entry, got %d", len(resp))
	}
	if resp[0]["status"] != "DRAFT" {
		t.Errorf("expected status=DRAFT, got %v", resp[0]["status"])
	}
	if resp[0]["contentHash"] == "" {
		t.Error("expected non-empty contentHash")
	}
	if resp[0]["createdAt"] == "" {
		t.Error("expected non-empty createdAt")
	}
}

func TestHandleGetVersion_ByIntegerCounter(t *testing.T) {
	ps := storeWithDraft(t, "ordersPolicy")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/policies/ordersPolicy/versions/1", nil)
	newSearchMux(ps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "DRAFT" {
		t.Errorf("expected status=DRAFT, got %v", resp["status"])
	}
}

func TestHandleGetVersion_BySemVer(t *testing.T) {
	ps := storeWithDraft(t, "ordersPolicy")
	// Approve and promote to get a STABLE semver entry
	if err := ps.ApproveDraft("ordersPolicy"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := ps.Promote("ordersPolicy", "patch"); err != nil {
		t.Fatalf("promote: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/policies/ordersPolicy/versions/1.0.1", nil)
	newSearchMux(ps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "STABLE" {
		t.Errorf("expected status=STABLE, got %v", resp["status"])
	}
	if resp["semVersion"] != "1.0.1" {
		t.Errorf("expected semVersion=1.0.1, got %v", resp["semVersion"])
	}
}

func TestHandleGetVersion_RemovedVersionVisible(t *testing.T) {
	ps := store.New()
	// Manually insert a REMOVED version record
	ps.AddHistory("ordersPolicy", &store.VersionRecord{
		PolicyName:  "ordersPolicy",
		Version:     1,
		SemVersion:  "1.1.0",
		Status:      store.VersionStatusRemoved,
		ContentHash: "abc123",
		Author:      "dev@example.com",
		CreatedAt:   time.Now(),
		RemovalNote: "deprecated in favor of 2.0.0",
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/policies/ordersPolicy/versions/1.1.0", nil)
	newSearchMux(ps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "REMOVED" {
		t.Errorf("expected status=REMOVED, got %v", resp["status"])
	}
	if resp["removalNote"] == "" {
		t.Error("expected removalNote in response")
	}
}

func TestHandleListVersions_ExcludesExecutionAuth(t *testing.T) {
	// The search endpoint has no auth check — it should always return metadata
	// (auth is EPIC-007, not yet implemented). Just verify the endpoint returns
	// data without any authorization header.
	ps := storeWithDraft(t, "ordersPolicy")
	r := httptest.NewRequest(http.MethodGet, "/policies/ordersPolicy/versions", nil)
	// No Authorization header — should still work
	w := httptest.NewRecorder()
	newSearchMux(ps).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 without auth, got %d", w.Code)
	}
}

// Ensure the promote endpoint also triggers a correct POST /policies/{name}/versions
func TestHandlePromote_HistoryEntryCreated(t *testing.T) {
	ps := storeWithDraft(t, "ordersPolicy")
	if err := ps.ApproveDraft("ordersPolicy"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	body := `{"bump":"minor"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/versions", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	newSearchMux(ps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("promote: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify history was updated
	recs, ok := ps.GetVersions("ordersPolicy")
	if !ok || len(recs) == 0 {
		t.Fatal("expected history entries after promotion")
	}
	var stableFound bool
	for _, rec := range recs {
		if rec.Status == store.VersionStatusStable {
			stableFound = true
			if rec.SemVersion != "1.1.0" {
				t.Errorf("expected semVersion=1.1.0, got %q", rec.SemVersion)
			}
		}
	}
	if !stableFound {
		t.Error("no STABLE entry in history after promotion")
	}
}
