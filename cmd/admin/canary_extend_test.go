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

var extendAuthSecret = []byte("test-extend-secret")

func newExtendMux(ps *store.PolicyStore, v auth.Verifier) *http.ServeMux {
	mux := http.NewServeMux()
	wrapped := func(h http.HandlerFunc) http.Handler {
		if v != nil {
			return auth.Middleware(v, h)
		}
		return h
	}
	mux.Handle("POST /policies/{name}/canary", wrapped(handleStartCanary(ps)))
	mux.Handle("PATCH /policies/{name}/canary", wrapped(handleExtendCanary(ps)))
	return mux
}

func extendBearerHeader(groups []string) string {
	return "Bearer " + auth.MakeTestToken(extendAuthSecret, groups, time.Now().Add(time.Hour))
}

func storeWithActiveCanary(t *testing.T, name, owner string) *store.PolicyStore {
	t.Helper()
	ps := makeApproveStore(t, name, owner)
	_, err := ps.StartCanary(name, "1.3.0", 10, 3600)
	if err != nil {
		t.Fatalf("StartCanary: %v", err)
	}
	return ps
}

func TestHandleExtendCanary_IncreasesPercent(t *testing.T) {
	ps := storeWithActiveCanary(t, "ordersPolicy", "team-payments")
	v := &auth.HMACVerifier{Secret: extendAuthSecret}

	body := `{"percent":50}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/policies/ordersPolicy/canary", bytes.NewBufferString(body))
	r.Header.Set("Authorization", extendBearerHeader([]string{"team-payments"}))
	newExtendMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["percent"].(float64) != 50 {
		t.Errorf("expected percent=50, got %v", resp["percent"])
	}
	if resp["status"] != "ACTIVE" {
		t.Errorf("expected status=ACTIVE, got %v", resp["status"])
	}
}

func TestHandleExtendCanary_ReducePercent_Rejected(t *testing.T) {
	ps := storeWithActiveCanary(t, "ordersPolicy", "team-payments")
	v := &auth.HMACVerifier{Secret: extendAuthSecret}

	body := `{"percent":5}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/policies/ordersPolicy/canary", bytes.NewBufferString(body))
	r.Header.Set("Authorization", extendBearerHeader([]string{"team-payments"}))
	newExtendMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for reduction, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] != "extend only increases traffic — use cancel to rollback" {
		t.Errorf("unexpected error message: %q", resp["error"])
	}
}

func TestHandleExtendCanary_SamePercent_Rejected(t *testing.T) {
	ps := storeWithActiveCanary(t, "ordersPolicy", "team-payments") // starts at 10%
	v := &auth.HMACVerifier{Secret: extendAuthSecret}

	body := `{"percent":10}` // same as current
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/policies/ordersPolicy/canary", bytes.NewBufferString(body))
	r.Header.Set("Authorization", extendBearerHeader([]string{"team-payments"}))
	newExtendMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for same percent, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleExtendCanary_NonOwner_Forbidden(t *testing.T) {
	ps := storeWithActiveCanary(t, "ordersPolicy", "team-payments")
	v := &auth.HMACVerifier{Secret: extendAuthSecret}

	body := `{"percent":50}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/policies/ordersPolicy/canary", bytes.NewBufferString(body))
	r.Header.Set("Authorization", extendBearerHeader([]string{"team-other"}))
	newExtendMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owner, got %d: %s", w.Code, w.Body.String())
	}
}
