package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RuleEngineLabs/engine/internal/auth"
	"github.com/RuleEngineLabs/engine/internal/store"
)

var canaryAuthSecret = []byte("test-canary-secret")

func newCanaryMux(ps *store.PolicyStore, v auth.Verifier) *http.ServeMux {
	mux := http.NewServeMux()
	wrapped := func(h http.HandlerFunc) http.Handler {
		if v != nil {
			return auth.Middleware(v, h)
		}
		return h
	}
	mux.Handle("POST /policies/{name}/canary", wrapped(handleStartCanary(ps)))
	return mux
}

func makeCanaryStore(t *testing.T, name, owner string) *store.PolicyStore {
	t.Helper()
	return makeApproveStore(t, name, owner)
}

func canaryBearerHeader(groups []string) string {
	return "Bearer " + auth.MakeTestToken(canaryAuthSecret, groups, time.Now().Add(time.Hour))
}

func TestHandleStartCanary_Success(t *testing.T) {
	ps := makeCanaryStore(t, "ordersPolicy", "team-payments")
	v := &auth.HMACVerifier{Secret: canaryAuthSecret}

	body := `{"candidateVersion":"1.3.0","percent":10,"ttl":3600}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/canary", bytes.NewBufferString(body))
	r.Header.Set("Authorization", canaryBearerHeader([]string{"team-payments"}))
	newCanaryMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ACTIVE" {
		t.Errorf("expected status=ACTIVE, got %v", resp["status"])
	}
	if resp["gsiActiveStatus"] != "ACTIVE" {
		t.Errorf("expected gsiActiveStatus=ACTIVE, got %v", resp["gsiActiveStatus"])
	}
	if resp["percent"].(float64) != 10 {
		t.Errorf("expected percent=10, got %v", resp["percent"])
	}
}

func TestHandleStartCanary_PercentZero_Rejected(t *testing.T) {
	ps := makeCanaryStore(t, "ordersPolicy", "team-payments")
	v := &auth.HMACVerifier{Secret: canaryAuthSecret}

	body := `{"candidateVersion":"1.3.0","percent":0,"ttl":3600}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/canary", bytes.NewBufferString(body))
	r.Header.Set("Authorization", canaryBearerHeader([]string{"team-payments"}))
	newCanaryMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for percent=0, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleStartCanary_Percent100_Rejected(t *testing.T) {
	ps := makeCanaryStore(t, "ordersPolicy", "team-payments")
	v := &auth.HMACVerifier{Secret: canaryAuthSecret}

	body := `{"candidateVersion":"1.3.0","percent":100,"ttl":3600}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/canary", bytes.NewBufferString(body))
	r.Header.Set("Authorization", canaryBearerHeader([]string{"team-payments"}))
	newCanaryMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for percent=100, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleStartCanary_ExpiredCanary_CanBeRestarted(t *testing.T) {
	ps := store.New()
	ps.Bootstrap([]*store.PolicyRecord{{PolicyID: "x", Name: "ordersPolicy", Owner: "team-payments"}})

	// First canary with TTL=0 → immediately expired
	_, err := ps.StartCanary("ordersPolicy", "1.3.0", 10, 0)
	if err != nil {
		t.Fatalf("first StartCanary: %v", err)
	}

	// A second start should succeed because the first expired
	_, err = ps.StartCanary("ordersPolicy", "1.4.0", 20, 3600)
	if err != nil {
		t.Fatalf("second StartCanary after expiry: %v", err)
	}

	rec, ok := ps.GetActiveCanary("ordersPolicy")
	if !ok {
		t.Fatal("expected active canary after restart")
	}
	if rec.CandidateVersion != "1.4.0" {
		t.Errorf("expected 1.4.0, got %q", rec.CandidateVersion)
	}
}

func TestHandleStartCanary_ExpiredTTL_GsiRemovedAndStatusCompleted(t *testing.T) {
	ps := store.New()
	ps.Bootstrap([]*store.PolicyRecord{{PolicyID: "x", Name: "ordersPolicy", Owner: "team-payments"}})

	// Start with TTL=0 → expires immediately
	_, err := ps.StartCanary("ordersPolicy", "1.3.0", 10, 0)
	if err != nil {
		t.Fatalf("StartCanary: %v", err)
	}

	// GetCanary auto-expires
	rec, ok := ps.GetCanary("ordersPolicy")
	if !ok {
		t.Fatal("canary record missing")
	}
	if rec.Status != store.CanaryStatusCompleted {
		t.Errorf("expected COMPLETED after TTL=0, got %v", rec.Status)
	}
	if rec.GsiActiveStatus != "" {
		t.Errorf("expected empty GsiActiveStatus after expire, got %q", rec.GsiActiveStatus)
	}

	active, ok := ps.GetActiveCanary("ordersPolicy")
	if ok || active != nil {
		t.Error("GetActiveCanary should return false after TTL expiry")
	}
}

func TestHandleStartCanary_NonOwner_Forbidden(t *testing.T) {
	ps := makeCanaryStore(t, "ordersPolicy", "team-payments")
	v := &auth.HMACVerifier{Secret: canaryAuthSecret}

	body := `{"candidateVersion":"1.3.0","percent":10,"ttl":3600}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/canary", bytes.NewBufferString(body))
	r.Header.Set("Authorization", canaryBearerHeader([]string{"team-other"}))
	newCanaryMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owner, got %d: %s", w.Code, w.Body.String())
	}
}
