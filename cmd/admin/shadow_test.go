package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/policy"
	"github.com/RuleEngineLabs/engine/internal/store"
)

// stableArt returns a compiled artifact whose response state returns the given data.
func stableArt(data any) *compiler.Artifact {
	p := &policy.Policy{
		ID:    "pol",
		Entry: "s",
		States: []policy.State{
			{ID: "s", Kind: policy.KindResponse, Data: data, Status: 200},
		},
	}
	art, _ := compiler.Compile(p)
	return art
}

// setupShadowStore creates a store with a STABLE policy and a draft ready for shadow.
func setupShadowStore(stableData, draftData any) (*store.PolicyStore, string) {
	ps := store.New()
	art := stableArt(stableData)
	id, _, _ := ps.Create("ordersPolicy", "team-a", art)

	draftArt := stableArt(draftData)
	ps.UpsertDraft("ordersPolicy", "1.2.0", art.Policy, draftArt)

	return ps, id
}

// TestShadow_StartStop tests activating and stopping shadow mode.
func TestShadow_StartStop(t *testing.T) {
	ps, _ := setupShadowStore("stable", "draft")
	mux := newAdminMux(ps, nil)

	body := `{"candidateVersion":"1.3.0"}`
	req := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/shadow", strings.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("start shadow: want 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var rec store.ShadowRecord
	json.NewDecoder(rr.Body).Decode(&rec)
	if rec.Status != store.ShadowStatusActive {
		t.Fatalf("expected ACTIVE, got %s", rec.Status)
	}
	if rec.CandidateVersion != "1.3.0" {
		t.Fatalf("expected candidateVersion 1.3.0, got %s", rec.CandidateVersion)
	}

	// Stop shadow
	req2 := httptest.NewRequest(http.MethodDelete, "/policies/ordersPolicy/shadow", nil)
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("stop shadow: want 200, got %d", rr2.Code)
	}
}

// TestShadow_AlreadyActive returns 409 when a shadow is already running.
func TestShadow_AlreadyActive(t *testing.T) {
	ps, _ := setupShadowStore("stable", "draft")
	mux := newAdminMux(ps, nil)

	body := `{"candidateVersion":"1.3.0"}`
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/shadow", strings.NewReader(body))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if i == 0 && rr.Code != http.StatusCreated {
			t.Fatalf("first start: want 201, got %d", rr.Code)
		}
		if i == 1 && rr.Code != http.StatusConflict {
			t.Fatalf("second start: want 409, got %d", rr.Code)
		}
	}
}

// TestShadow_StopNotFound returns 404 when no shadow is active.
func TestShadow_StopNotFound(t *testing.T) {
	ps := store.New()
	mux := newAdminMux(ps, nil)
	req := httptest.NewRequest(http.MethodDelete, "/policies/unknown/shadow", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}
}

// TestShadow_NoDraftForPolicy returns 404 when there's no draft to shadow.
func TestShadow_NoDraftForPolicy(t *testing.T) {
	ps := store.New()
	mux := newAdminMux(ps, nil)
	body := `{"candidateVersion":"1.3.0"}`
	req := httptest.NewRequest(http.MethodPost, "/policies/missing/shadow", strings.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}
}

// TestShadow_BadJSON returns 400 for malformed body.
func TestShadow_BadJSON(t *testing.T) {
	ps, _ := setupShadowStore("stable", "draft")
	mux := newAdminMux(ps, nil)
	req := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/shadow", strings.NewReader("{bad"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

// TestShadow_DefaultCandidateVersion uses "draft" when candidateVersion is omitted.
func TestShadow_DefaultCandidateVersion(t *testing.T) {
	ps, _ := setupShadowStore("stable", "draft")
	mux := newAdminMux(ps, nil)
	req := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/shadow", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d", rr.Code)
	}
	var rec store.ShadowRecord
	json.NewDecoder(rr.Body).Decode(&rec)
	if rec.CandidateVersion != "draft" {
		t.Fatalf("expected 'draft', got %q", rec.CandidateVersion)
	}
}

// TestShadow_ExecuteRecordsDivergence covers the Gherkin scenario: outputs differ → logged.
func TestShadow_ExecuteRecordsDivergence(t *testing.T) {
	ps, id := setupShadowStore(map[string]any{"result": "stable"}, map[string]any{"result": "candidate"})
	mux := newAdminMux(ps, nil)

	// Activate shadow
	startBody := `{"candidateVersion":"1.3.0"}`
	req := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/shadow", strings.NewReader(startBody))
	httptest.NewRecorder() // discard
	mux.ServeHTTP(httptest.NewRecorder(), req)

	// Execute policy (STABLE responds to consumer)
	execReq := httptest.NewRequest(http.MethodPost, "/execute/"+id, bytes.NewBufferString(`{}`))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, execReq)
	if rr.Code != http.StatusOK {
		t.Fatalf("execute: want 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Consumer gets STABLE response
	var result map[string]any
	json.NewDecoder(rr.Body).Decode(&result)
	data, _ := result["data"].(map[string]any)
	if data["result"] != "stable" {
		t.Fatalf("consumer got candidate output, want stable: %v", data)
	}

	// Wait briefly for goroutine to complete
	time.Sleep(50 * time.Millisecond)

	// Divergence should be logged
	divReq := httptest.NewRequest(http.MethodGet, "/policies/ordersPolicy/shadow/divergences", nil)
	divRR := httptest.NewRecorder()
	mux.ServeHTTP(divRR, divReq)
	if divRR.Code != http.StatusOK {
		t.Fatalf("divergences: want 200, got %d", divRR.Code)
	}
	var divs []store.ShadowDivergence
	json.NewDecoder(divRR.Body).Decode(&divs)
	if len(divs) != 1 {
		t.Fatalf("expected 1 divergence, got %d", len(divs))
	}
	if divs[0].CandidateVersion != "1.3.0" {
		t.Fatalf("expected candidateVersion 1.3.0, got %s", divs[0].CandidateVersion)
	}
}

// TestShadow_ExecuteNoDivergenceWhenIdentical covers: identical outputs → no log entry.
func TestShadow_ExecuteNoDivergenceWhenIdentical(t *testing.T) {
	sameData := map[string]any{"result": "same"}
	ps, id := setupShadowStore(sameData, sameData)
	mux := newAdminMux(ps, nil)

	startBody := `{"candidateVersion":"1.3.0"}`
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/shadow", strings.NewReader(startBody)))

	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/execute/"+id, bytes.NewBufferString(`{}`)))

	time.Sleep(50 * time.Millisecond)

	divs := ps.GetDivergences("ordersPolicy")
	if len(divs) != 0 {
		t.Fatalf("expected no divergences for identical outputs, got %d", len(divs))
	}
}

// TestShadow_CandidateErrorLoggedSilently covers: candidate error → logged, consumer unaffected.
func TestShadow_CandidateErrorLoggedSilently(t *testing.T) {
	ps := store.New()
	// STABLE artifact is valid
	stableArtifact := stableArt(map[string]any{"result": "ok"})
	id, _, _ := ps.Create("ordersPolicy", "team-a", stableArtifact)

	// Candidate artifact has a broken entry (set after compile) → will fail at runtime.
	brokenArt, _ := compiler.Compile(stableArtifact.Policy)
	brokenArt.Policy = &policy.Policy{
		ID:    "broken",
		Entry: "nonexistent",
		States: []policy.State{
			{ID: "s", Kind: policy.KindResponse, Status: 200},
		},
	}
	ps.UpsertDraft("ordersPolicy", "1.2.0", stableArtifact.Policy, brokenArt)

	mux := newAdminMux(ps, nil)

	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/shadow", strings.NewReader(`{"candidateVersion":"1.3.0"}`)))

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/execute/"+id, bytes.NewBufferString(`{}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("consumer must get 200, got %d: %s", rr.Code, rr.Body.String())
	}

	time.Sleep(50 * time.Millisecond)

	divs := ps.GetDivergences("ordersPolicy")
	if len(divs) != 1 {
		t.Fatalf("expected 1 divergence (candidate error), got %d", len(divs))
	}
	if divs[0].CandidateError == "" {
		t.Fatal("expected CandidateError to be set")
	}
}

// TestShadow_NoShadowActiveNoEffect verifies execute works normally without shadow.
func TestShadow_NoShadowActiveNoEffect(t *testing.T) {
	ps, id := setupShadowStore(map[string]any{"result": "stable"}, map[string]any{"result": "draft"})
	mux := newAdminMux(ps, nil)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/execute/"+id, bytes.NewBufferString(`{}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}

	time.Sleep(10 * time.Millisecond)
	if len(ps.GetDivergences("ordersPolicy")) != 0 {
		t.Fatal("expected no divergences when shadow not active")
	}
}

// TestShadow_GetDivergencesEmpty returns empty array when no divergences logged.
func TestShadow_GetDivergencesEmpty(t *testing.T) {
	ps := store.New()
	mux := newAdminMux(ps, nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/policies/anyPolicy/shadow/divergences", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	var divs []store.ShadowDivergence
	json.NewDecoder(rr.Body).Decode(&divs)
	if len(divs) != 0 {
		t.Fatalf("expected empty array, got %d entries", len(divs))
	}
}

// TestStore_Shadow_DirectMethods tests store shadow methods directly.
func TestStore_Shadow_DirectMethods(t *testing.T) {
	ps := store.New()
	art := stableArt("v")

	// StartShadow on unknown policy (no existing shadow)
	rec, err := ps.StartShadow("pol", "1.0.0", art)
	if err != nil {
		t.Fatalf("StartShadow: %v", err)
	}
	if rec.Status != store.ShadowStatusActive {
		t.Fatalf("expected ACTIVE")
	}

	// Second StartShadow → conflict
	_, err = ps.StartShadow("pol", "1.1.0", art)
	if err == nil {
		t.Fatal("expected ErrShadowAlreadyActive")
	}

	// GetActiveShadow
	s, ok := ps.GetActiveShadow("pol")
	if !ok || s.CandidateVersion != "1.0.0" {
		t.Fatalf("GetActiveShadow: unexpected %v %v", ok, s)
	}

	// StopShadow
	stopped, err := ps.StopShadow("pol")
	if err != nil || stopped.Status != store.ShadowStatusStopped {
		t.Fatalf("StopShadow: %v %v", err, stopped)
	}

	// GetActiveShadow after stop
	_, ok = ps.GetActiveShadow("pol")
	if ok {
		t.Fatal("expected no active shadow after stop")
	}

	// StopShadow again → not found
	_, err = ps.StopShadow("pol")
	if err == nil {
		t.Fatal("expected ErrShadowNotFound")
	}

	// LogDivergence + GetDivergences
	ps.LogDivergence(&store.ShadowDivergence{PolicyName: "pol", StableVersion: "1.0.0"})
	divs := ps.GetDivergences("pol")
	if len(divs) != 1 {
		t.Fatalf("expected 1 divergence, got %d", len(divs))
	}

	// ErrShadowAlreadyActive / ErrShadowNotFound error strings
	eActive := store.ErrShadowAlreadyActive{Name: "pol"}
	if !strings.Contains(eActive.Error(), "pol") {
		t.Fatalf("ErrShadowAlreadyActive message: %s", eActive.Error())
	}
	eNotFound := store.ErrShadowNotFound{Name: "pol"}
	if !strings.Contains(eNotFound.Error(), "pol") {
		t.Fatalf("ErrShadowNotFound message: %s", eNotFound.Error())
	}
}
