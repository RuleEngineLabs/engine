package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RuleEngineLabs/engine/internal/auth"
	"github.com/RuleEngineLabs/engine/internal/store"
)

func newDeleteMux(ps *store.PolicyStore, v auth.Verifier) *http.ServeMux {
	mux := http.NewServeMux()
	wrapped := func(h http.HandlerFunc) http.Handler {
		if v != nil {
			return auth.Middleware(v, h)
		}
		return h
	}
	mux.Handle("DELETE /policies/{name}/versions/{version}", wrapped(handleDeleteVersion(ps)))
	mux.HandleFunc("GET /policies/{name}/versions/{version}", handleGetVersion(ps))
	return mux
}

func makeDeleteStore(t *testing.T, name, owner string) *store.PolicyStore {
	t.Helper()
	ps := makeApproveStore(t, name, owner) // creates policy + draft version 1
	return ps
}

func TestHandleDeleteVersion_DraftRemoved(t *testing.T) {
	ps := makeDeleteStore(t, "ordersPolicy", "team-payments")

	v := &auth.HMACVerifier{Secret: testAuthSecret}
	body := `{"reason":"superseded by v4"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/policies/ordersPolicy/versions/1", bytes.NewBufferString(body))
	r.Header.Set("Authorization", bearerHeader([]string{"team-payments"}))
	newDeleteMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "REMOVED" {
		t.Errorf("expected status=REMOVED, got %q", resp["status"])
	}

	// Audit log must have an entry
	entries := ps.GetAuditLog()
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	e := entries[0]
	if e.PolicyName != "ordersPolicy" {
		t.Errorf("audit: expected policy=ordersPolicy, got %q", e.PolicyName)
	}
	if e.Reason != "superseded by v4" {
		t.Errorf("audit: expected reason, got %q", e.Reason)
	}
	if e.RequestedBy != "team-payments" {
		t.Errorf("audit: expected requestedBy=team-payments, got %q", e.RequestedBy)
	}
	if e.Timestamp.IsZero() {
		t.Error("audit: expected non-zero timestamp")
	}
}

func TestHandleDeleteVersion_StableConflict(t *testing.T) {
	ps := makeDeleteStore(t, "ordersPolicy", "team-payments")
	// Approve and promote to STABLE
	if err := ps.ApproveDraft("ordersPolicy"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := ps.Promote("ordersPolicy", "patch"); err != nil {
		t.Fatalf("promote: %v", err)
	}

	v := &auth.HMACVerifier{Secret: testAuthSecret}
	body := `{"reason":"trying to delete stable"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/policies/ordersPolicy/versions/1.0.1", bytes.NewBufferString(body))
	r.Header.Set("Authorization", bearerHeader([]string{"team-payments"}))
	newDeleteMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] != "cannot delete STABLE version with active consumers" {
		t.Errorf("unexpected error message: %q", resp["error"])
	}
}

func TestHandleDeleteVersion_RemovedStatusVisibleOnGet(t *testing.T) {
	ps := makeDeleteStore(t, "ordersPolicy", "team-payments")

	v := &auth.HMACVerifier{Secret: testAuthSecret}
	body := `{"reason":"replaced"}`
	del := httptest.NewRequest(http.MethodDelete, "/policies/ordersPolicy/versions/1", bytes.NewBufferString(body))
	del.Header.Set("Authorization", bearerHeader([]string{"team-payments"}))
	newDeleteMux(ps, v).ServeHTTP(httptest.NewRecorder(), del)

	// Now GET the version and confirm REMOVED status
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/policies/ordersPolicy/versions/1", nil)
	newDeleteMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on get after delete, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "REMOVED" {
		t.Errorf("expected status=REMOVED after delete, got %v", resp["status"])
	}
	if resp["removalNote"] != "replaced" {
		t.Errorf("expected removalNote=replaced, got %v", resp["removalNote"])
	}
}

func TestHandleDeleteVersion_NonOwner_Forbidden(t *testing.T) {
	ps := makeDeleteStore(t, "ordersPolicy", "team-payments")

	v := &auth.HMACVerifier{Secret: testAuthSecret}
	body := `{"reason":"unauthorized attempt"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/policies/ordersPolicy/versions/1", bytes.NewBufferString(body))
	r.Header.Set("Authorization", bearerHeader([]string{"team-other"}))
	newDeleteMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}
