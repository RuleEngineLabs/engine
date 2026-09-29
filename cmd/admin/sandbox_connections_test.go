package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RuleEngineLabs/engine/internal/auth"
	"github.com/RuleEngineLabs/engine/internal/sandbox"
	"github.com/RuleEngineLabs/engine/internal/store"
)

// memS3Writer is an in-memory S3Writer used in tests.
type memS3Writer struct {
	objects map[string][]byte
	err     error
}

func newMemS3Writer() *memS3Writer {
	return &memS3Writer{objects: make(map[string][]byte)}
}

func (w *memS3Writer) PutObject(_ context.Context, bucket, key string, data []byte) error {
	if w.err != nil {
		return w.err
	}
	w.objects[bucket+"/"+key] = data
	return nil
}

// buildSandboxMux returns a mux with the sandbox connection route registered.
func buildSandboxMux(ps *store.PolicyStore, writer *memS3Writer) *http.ServeMux {
	mux := newAdminMux(ps, nil)
	pub := sandbox.NewS3Publisher(writer, "sandbox-bucket")
	registerSandboxRoutes(mux, ps, pub)
	return mux
}

// buildPolicyStore creates a store with a policy owned by "team-payments".
func buildSandboxPolicyStore(t *testing.T) (*store.PolicyStore, string) {
	t.Helper()
	ps := store.New()
	art := buildArt(map[string]any{"v": "stable"})
	id, _, _ := ps.Create("ordersPolicy", "team-payments", art)
	return ps, id
}

// ownerCtx returns a request context with Claims for a user in the given groups.
func ownerCtx(r *http.Request, groups ...string) *http.Request {
	return r.WithContext(auth.WithClaims(r.Context(), &auth.Claims{Groups: groups}))
}

// TestPublishSandboxMapping_OwnerCanPublish verifies the owner can PUT a mapping.
func TestPublishSandboxMapping_OwnerCanPublish(t *testing.T) {
	ps, _ := buildSandboxPolicyStore(t)
	writer := newMemS3Writer()
	mux := buildSandboxMux(ps, writer)

	body := `{"method":"POST","path":"/orders","status":201,"body":{"id":"ord-1"}}`
	req := httptest.NewRequest(http.MethodPut, "/policies/ordersPolicy/sandbox/connections/orders-service", strings.NewReader(body))
	req = ownerCtx(req, "team-payments")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["status"] != "published" {
		t.Fatalf("expected status=published, got %v", resp)
	}
	if resp["service"] != "orders-service" {
		t.Fatalf("expected service=orders-service, got %v", resp)
	}

	// Verify object was written to S3
	key := "sandbox-bucket/sandbox/overrides/ordersPolicy/orders-service.json"
	if _, ok := writer.objects[key]; !ok {
		t.Fatalf("expected object at %q, got keys: %v", key, writer.objects)
	}
}

// TestPublishSandboxMapping_PolicyOperatorCanPublish verifies global operator group can publish.
func TestPublishSandboxMapping_PolicyOperatorCanPublish(t *testing.T) {
	ps, _ := buildSandboxPolicyStore(t)
	writer := newMemS3Writer()
	mux := buildSandboxMux(ps, writer)

	body := `{"method":"POST","path":"/orders","status":201}`
	req := httptest.NewRequest(http.MethodPut, "/policies/ordersPolicy/sandbox/connections/svc", strings.NewReader(body))
	req = ownerCtx(req, "policy-operators")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestPublishSandboxMapping_ForbiddenForConsumer verifies 403 for unauthorized caller.
func TestPublishSandboxMapping_ForbiddenForConsumer(t *testing.T) {
	ps, _ := buildSandboxPolicyStore(t)
	writer := newMemS3Writer()
	mux := buildSandboxMux(ps, writer)

	body := `{"method":"POST","path":"/orders","status":201}`
	req := httptest.NewRequest(http.MethodPut, "/policies/ordersPolicy/sandbox/connections/svc", strings.NewReader(body))
	req = ownerCtx(req, "team-other") // different team, not operator
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestPublishSandboxMapping_NoAuthClaims returns 403 when there are no auth claims.
func TestPublishSandboxMapping_NoAuthClaims(t *testing.T) {
	ps, _ := buildSandboxPolicyStore(t)
	writer := newMemS3Writer()
	mux := buildSandboxMux(ps, writer)

	body := `{"method":"POST","path":"/orders","status":201}`
	req := httptest.NewRequest(http.MethodPut, "/policies/ordersPolicy/sandbox/connections/svc", strings.NewReader(body))
	// No ownerCtx — no Claims in context
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestPublishSandboxMapping_PolicyNotFound returns 404 for unknown policy.
func TestPublishSandboxMapping_PolicyNotFound(t *testing.T) {
	ps := store.New()
	writer := newMemS3Writer()
	mux := buildSandboxMux(ps, writer)

	req := httptest.NewRequest(http.MethodPut, "/policies/unknown/sandbox/connections/svc", strings.NewReader("{}"))
	req = ownerCtx(req, "policy-operators")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}
}

// TestPublishSandboxMapping_BadJSON returns 400 for invalid JSON.
func TestPublishSandboxMapping_BadJSON(t *testing.T) {
	ps, _ := buildSandboxPolicyStore(t)
	writer := newMemS3Writer()
	mux := buildSandboxMux(ps, writer)

	req := httptest.NewRequest(http.MethodPut, "/policies/ordersPolicy/sandbox/connections/svc", strings.NewReader("{bad"))
	req.ContentLength = int64(len("{bad"))
	req = ownerCtx(req, "team-payments")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

// TestPublishSandboxMapping_S3Error returns 500 when S3 write fails.
func TestPublishSandboxMapping_S3Error(t *testing.T) {
	ps, _ := buildSandboxPolicyStore(t)
	writer := newMemS3Writer()
	writer.err = errors.New("s3: connection refused")
	mux := buildSandboxMux(ps, writer)

	body := `{"method":"POST","path":"/orders","status":201}`
	req := httptest.NewRequest(http.MethodPut, "/policies/ordersPolicy/sandbox/connections/svc", strings.NewReader(body))
	req = ownerCtx(req, "team-payments")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rr.Code)
	}
}

// TestPublishSandboxMapping_WrittenObjectIsValidJSON verifies the S3 object is valid JSON.
func TestPublishSandboxMapping_WrittenObjectIsValidJSON(t *testing.T) {
	ps, _ := buildSandboxPolicyStore(t)
	writer := newMemS3Writer()
	mux := buildSandboxMux(ps, writer)

	body := `{"method":"POST","path":"/orders","status":201,"body":{"id":"ord-1"},"headers":{"Location":"/orders/ord-1"}}`
	req := httptest.NewRequest(http.MethodPut, "/policies/ordersPolicy/sandbox/connections/orders-service", strings.NewReader(body))
	req = ownerCtx(req, "team-payments")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d", rr.Code)
	}

	key := "sandbox-bucket/sandbox/overrides/ordersPolicy/orders-service.json"
	data, ok := writer.objects[key]
	if !ok {
		t.Fatal("object not written to S3")
	}

	var m sandbox.Mapping
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("S3 object is not valid JSON: %v", err)
	}
	if m.Method != "POST" || m.Path != "/orders" || m.Status != 201 {
		t.Fatalf("unexpected mapping: %+v", m)
	}
}
