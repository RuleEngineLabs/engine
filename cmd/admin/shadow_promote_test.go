package main

import (
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

// buildPromoteStore creates a store with an ACTIVE shadow ready for promotion tests.
// stable and candidate are separate artifacts so divergence tests can distinguish them.
func buildPromoteStore(t *testing.T) (*store.PolicyStore, string, *store.ShadowRecord) {
	t.Helper()
	ps := store.New()

	stableArt := buildArt(map[string]any{"v": "stable"})
	id, _, _ := ps.Create("ordersPolicy", "team-a", stableArt)

	draftArt := buildArt(map[string]any{"v": "candidate"})
	ps.UpsertDraft("ordersPolicy", "0.0.1", stableArt.Policy, draftArt)

	shadow, _ := ps.StartShadow("ordersPolicy", "1.3.0", draftArt)
	return ps, id, shadow
}

// buildArt compiles a trivial response policy with the given data.
func buildArt(data any) *compiler.Artifact {
	p := &policy.Policy{
		ID:    "p",
		Entry: "s",
		States: []policy.State{
			{ID: "s", Kind: policy.KindResponse, Data: data, Status: 200},
		},
	}
	art, _ := compiler.Compile(p)
	return art
}

// TestPromoteShadow_NoDivergences promotes directly when no divergences logged.
func TestPromoteShadow_NoDivergences(t *testing.T) {
	ps, _, _ := buildPromoteStore(t)
	mux := newAdminMux(ps, nil)

	req := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/shadow/promote", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("promote: want 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["promoted"] != true {
		t.Fatalf("expected promoted:true, got %v", resp)
	}
	if resp["version"] == "" || resp["version"] == nil {
		t.Fatalf("expected version in response, got %v", resp)
	}

	// Shadow must be stopped after promotion
	_, ok := ps.GetActiveShadow("ordersPolicy")
	if ok {
		t.Fatal("expected shadow to be stopped after promotion")
	}
}

// TestPromoteShadow_WithDivergencesNoForce returns 409 when divergences exist.
func TestPromoteShadow_WithDivergencesNoForce(t *testing.T) {
	ps, _, _ := buildPromoteStore(t)
	mux := newAdminMux(ps, nil)

	// Inject a divergence directly
	ps.LogDivergence(&store.ShadowDivergence{
		Timestamp:        time.Now(),
		PolicyName:       "ordersPolicy",
		StableVersion:    "0.0.1",
		CandidateVersion: "1.3.0",
		StableOutput:     "stable",
		CandidateOutput:  "candidate",
	})

	req := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/shadow/promote", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]any
	json.NewDecoder(rr.Body).Decode(&body)
	if _, hasDivs := body["divergences"]; !hasDivs {
		t.Fatal("expected divergences in 409 body")
	}
}

// TestPromoteShadow_WithDivergencesForceTrue promotes even with divergences when force=true.
func TestPromoteShadow_WithDivergencesForceTrue(t *testing.T) {
	ps, _, _ := buildPromoteStore(t)
	mux := newAdminMux(ps, nil)

	ps.LogDivergence(&store.ShadowDivergence{
		PolicyName: "ordersPolicy",
		Timestamp:  time.Now(),
	})

	req := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/shadow/promote", strings.NewReader(`{"force":true}`))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("force promote: want 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestPromoteShadow_CustomBump promotes with explicit major bump.
func TestPromoteShadow_CustomBump(t *testing.T) {
	ps, _, _ := buildPromoteStore(t)
	mux := newAdminMux(ps, nil)

	req := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/shadow/promote", strings.NewReader(`{"bump":"patch"}`))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("patch bump: want 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	// patch of "0.0.1" → "0.0.2"
	if resp["version"] != "0.0.2" {
		t.Fatalf("expected 0.0.2, got %v", resp["version"])
	}
}

// TestPromoteShadow_NoActiveShadow returns 404 when no shadow is active.
func TestPromoteShadow_NoActiveShadow(t *testing.T) {
	ps := store.New()
	mux := newAdminMux(ps, nil)
	req := httptest.NewRequest(http.MethodPost, "/policies/unknown/shadow/promote", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}
}

// TestPromoteShadow_BadJSON returns 400.
func TestPromoteShadow_BadJSON(t *testing.T) {
	ps, _, _ := buildPromoteStore(t)
	mux := newAdminMux(ps, nil)
	req := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/shadow/promote", strings.NewReader("{bad"))
	req.ContentLength = int64(len("{bad"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

// TestPromoteShadow_StopShadowPreservesdraft verifies DELETE shadow leaves draft intact.
func TestPromoteShadow_StopShadowPreservesDraft(t *testing.T) {
	ps, _, _ := buildPromoteStore(t)
	mux := newAdminMux(ps, nil)

	req := httptest.NewRequest(http.MethodDelete, "/policies/ordersPolicy/shadow", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("stop shadow: want 200, got %d", rr.Code)
	}

	// Draft must still exist
	draft, ok := ps.GetDraft("ordersPolicy")
	if !ok || draft == nil {
		t.Fatal("expected draft to survive shadow stop")
	}

	// Shadow is inactive
	_, ok = ps.GetActiveShadow("ordersPolicy")
	if ok {
		t.Fatal("expected shadow to be stopped")
	}
}

// TestPromoteShadow_EmptyBody promotes with no body (Content-Length == 0).
func TestPromoteShadow_EmptyBody(t *testing.T) {
	ps, _, _ := buildPromoteStore(t)
	mux := newAdminMux(ps, nil)

	req := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/shadow/promote", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("empty body promote: want 200, got %d: %s", rr.Code, rr.Body.String())
	}
}
