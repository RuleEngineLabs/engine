package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RuleEngineLabs/engine/internal/store"
)

// TestHealth_Returns200 verifies GET /health returns 200 with status=ok.
// This endpoint is used by the ALB health check in production (US-030).
func TestHealth_Returns200(t *testing.T) {
	ps := store.New()
	mux := newAdminMux(ps, nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}

	var body map[string]string
	json.NewDecoder(rr.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Fatalf("want status=ok, got %v", body)
	}
}

// TestHealth_ContentType verifies application/json is set.
func TestHealth_ContentType(t *testing.T) {
	ps := store.New()
	mux := newAdminMux(ps, nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	ct := rr.Header().Get("Content-Type")
	if ct == "" {
		t.Fatal("expected Content-Type header")
	}
}
