package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RuleEngineLabs/engine/internal/auth"
)

var testSecret = []byte("test-secret-key")

func newVerifier() auth.Verifier {
	return &auth.HMACVerifier{Secret: testSecret}
}

func validToken(groups []string) string {
	return auth.MakeTestToken(testSecret, groups, time.Now().Add(time.Hour))
}

func expiredToken(groups []string) string {
	return auth.MakeTestToken(testSecret, groups, time.Now().Add(-time.Hour))
}

func TestHMACVerifier_ValidToken_ExtractsGroups(t *testing.T) {
	token := validToken([]string{"team-payments", "team-ops"})
	claims, err := newVerifier().Verify(token)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(claims.Groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(claims.Groups))
	}
}

func TestHMACVerifier_InvalidSignature_Rejected(t *testing.T) {
	token := validToken([]string{"team-payments"}) + "tampered"
	_, err := newVerifier().Verify(token)
	if err == nil {
		t.Fatal("expected error for tampered token")
	}
}

func TestHMACVerifier_ExpiredToken_Rejected(t *testing.T) {
	token := expiredToken([]string{"team-payments"})
	_, err := newVerifier().Verify(token)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestIsOwner_MatchingGroup(t *testing.T) {
	token := validToken([]string{"team-payments"})
	claims, _ := newVerifier().Verify(token)
	ctx := auth.WithClaims(t.Context(), claims)

	if !auth.IsOwner(ctx, "team-payments") {
		t.Error("expected owner check to pass for team-payments")
	}
	if auth.IsOwner(ctx, "team-other") {
		t.Error("expected owner check to fail for team-other")
	}
}

func TestIsOwner_GlobalOperator_AlwaysAuthorized(t *testing.T) {
	token := validToken([]string{auth.GlobalOperatorGroup})
	claims, _ := newVerifier().Verify(token)
	ctx := auth.WithClaims(t.Context(), claims)

	if !auth.IsOwner(ctx, "any-policy-owner") {
		t.Error("global operator should be authorized for any owner")
	}
	if !auth.IsOperator(ctx) {
		t.Error("global operator should pass IsOperator check")
	}
}

func TestMiddleware_MissingToken_Returns401(t *testing.T) {
	handler := auth.Middleware(newVerifier(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestMiddleware_ValidToken_PassesThrough(t *testing.T) {
	handler := auth.Middleware(newVerifier(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := auth.FromContext(r.Context())
		if claims == nil {
			http.Error(w, "no claims", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer "+validToken([]string{"team-payments"}))
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestMiddleware_InvalidToken_Returns401(t *testing.T) {
	handler := auth.Middleware(newVerifier(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer not.a.valid.jwt")
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestOwnerCheck_ForbiddenGroup_Returns403InHandler(t *testing.T) {
	// Handler that enforces owner check
	protectedHandler := auth.Middleware(newVerifier(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.IsOwner(r.Context(), "team-payments") {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	// team-other is NOT in team-payments
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/policies/ordersPolicy/versions", nil)
	r.Header.Set("Authorization", "Bearer "+validToken([]string{"team-other"}))
	protectedHandler.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}
