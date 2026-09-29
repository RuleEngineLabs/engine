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

var cancelAuthSecret = []byte("test-cancel-secret")

func newCancelMux(ps *store.PolicyStore, v auth.Verifier) *http.ServeMux {
	mux := http.NewServeMux()
	wrapped := func(h http.HandlerFunc) http.Handler {
		if v != nil {
			return auth.Middleware(v, h)
		}
		return h
	}
	mux.Handle("POST /policies/{name}/canary", wrapped(handleStartCanary(ps)))
	mux.Handle("DELETE /policies/{name}/canary", wrapped(handleCancelCanary(ps)))
	return mux
}

func cancelBearerHeader(groups []string) string {
	return "Bearer " + auth.MakeTestToken(cancelAuthSecret, groups, time.Now().Add(time.Hour))
}

func storeWithCanaryForCancel(t *testing.T, name, owner string) *store.PolicyStore {
	t.Helper()
	ps := makeApproveStore(t, name, owner)
	_, err := ps.StartCanary(name, "1.3.0", 30, 3600)
	if err != nil {
		t.Fatalf("StartCanary: %v", err)
	}
	return ps
}

func TestHandleCancelCanary_RevertToStable(t *testing.T) {
	ps := storeWithCanaryForCancel(t, "ordersPolicy", "team-payments")
	v := &auth.HMACVerifier{Secret: cancelAuthSecret}

	body := `{"reason":"latência alta"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/policies/ordersPolicy/canary", bytes.NewBufferString(body))
	r.Header.Set("Authorization", cancelBearerHeader([]string{"team-payments"}))
	newCancelMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "CANCELLED" {
		t.Errorf("expected status=CANCELLED, got %v", resp["status"])
	}
}

func TestHandleCancelCanary_GsiClearedAfterCancel(t *testing.T) {
	ps := storeWithCanaryForCancel(t, "ordersPolicy", "team-payments")
	v := &auth.HMACVerifier{Secret: cancelAuthSecret}

	body := `{"reason":"error rate spike"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/policies/ordersPolicy/canary", bytes.NewBufferString(body))
	r.Header.Set("Authorization", cancelBearerHeader([]string{"team-payments"}))
	newCancelMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("cancel: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if gsi, _ := resp["gsiActiveStatus"].(string); gsi != "" {
		t.Errorf("expected empty gsiActiveStatus after cancel, got %q", gsi)
	}

	// Verify GetActiveCanary returns false (no active canary)
	_, active := ps.GetActiveCanary("ordersPolicy")
	if active {
		t.Error("GetActiveCanary should return false after cancel")
	}

	// GetCanary still returns the record with CANCELLED status
	rec, ok := ps.GetCanary("ordersPolicy")
	if !ok {
		t.Fatal("GetCanary should still return the cancelled record")
	}
	if rec.Status != store.CanaryStatusCancelled {
		t.Errorf("expected CANCELLED, got %v", rec.Status)
	}
	if rec.GsiActiveStatus != "" {
		t.Errorf("expected empty gsiActiveStatus, got %q", rec.GsiActiveStatus)
	}
}

func TestHandleCancelCanary_NoActiveCanary_NotFound(t *testing.T) {
	ps := makeApproveStore(t, "ordersPolicy", "team-payments") // no canary started
	v := &auth.HMACVerifier{Secret: cancelAuthSecret}

	body := `{"reason":"test"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/policies/ordersPolicy/canary", bytes.NewBufferString(body))
	r.Header.Set("Authorization", cancelBearerHeader([]string{"team-payments"}))
	newCancelMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for no active canary, got %d: %s", w.Code, w.Body.String())
	}
}
