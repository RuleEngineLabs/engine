package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RuleEngineLabs/engine/internal/auth"
	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/policy"
	"github.com/RuleEngineLabs/engine/internal/store"
)

// buildExecuteStore creates a store with a single response policy.
func buildExecuteStore(t *testing.T) (*store.PolicyStore, string) {
	t.Helper()
	ps := store.New()
	p := &policy.Policy{
		ID:    "bp",
		Entry: "s",
		States: []policy.State{
			{ID: "s", Kind: policy.KindResponse, Data: "ok", Status: 200},
		},
	}
	art, _ := compiler.Compile(p)
	id, _, _ := ps.Create("benchPolicy", "team-bench", art)
	return ps, id
}

// withStagingEnv sets ENVIRONMENT=staging for the duration of the test.
func withStagingEnv(t *testing.T) {
	t.Helper()
	orig := isStaging
	t.Setenv("ENVIRONMENT", "staging")
	isStaging = func() bool { return true }
	t.Cleanup(func() { isStaging = orig })
}

// withProductionEnv ensures ENVIRONMENT is not staging for the duration of the test.
func withProductionEnv(t *testing.T) {
	t.Helper()
	orig := isStaging
	isStaging = func() bool { return false }
	t.Cleanup(func() { isStaging = orig })
}

// approverRequest wraps a request with approver claims.
func approverRequest(r *http.Request) *http.Request {
	return r.WithContext(auth.WithClaims(r.Context(), &auth.Claims{Groups: []string{"policy-operators"}}))
}

// consumerRequest wraps a request with non-approver claims.
func consumerRequest(r *http.Request) *http.Request {
	return r.WithContext(auth.WithClaims(r.Context(), &auth.Claims{Groups: []string{"team-consumer"}}))
}

// TestBenchmark_ApproverInStaging_Allowed verifies approver with origin=benchmark is allowed in staging.
func TestBenchmark_ApproverInStaging_Allowed(t *testing.T) {
	ps, id := buildExecuteStore(t)
	withStagingEnv(t)
	mux := newAdminMux(ps, nil)

	req := httptest.NewRequest(http.MethodPost, "/execute/"+id+"?noCache=true", strings.NewReader("{}"))
	req.Header.Set("X-Origin", "benchmark")
	req = approverRequest(req)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("approver in staging: want 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestBenchmark_ApproverInProduction_Forbidden verifies origin=benchmark is rejected outside staging.
func TestBenchmark_ApproverInProduction_Forbidden(t *testing.T) {
	ps, id := buildExecuteStore(t)
	withProductionEnv(t)
	mux := newAdminMux(ps, nil)

	req := httptest.NewRequest(http.MethodPost, "/execute/"+id+"?noCache=true", strings.NewReader("{}"))
	req.Header.Set("X-Origin", "benchmark")
	req = approverRequest(req)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("approver in production: want 403, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "benchmark origin restricted to staging") {
		t.Fatalf("expected staging error message, got: %s", rr.Body.String())
	}
}

// TestBenchmark_ConsumerInStaging_Forbidden verifies non-approver is rejected even in staging.
func TestBenchmark_ConsumerInStaging_Forbidden(t *testing.T) {
	ps, id := buildExecuteStore(t)
	withStagingEnv(t)
	mux := newAdminMux(ps, nil)

	req := httptest.NewRequest(http.MethodPost, "/execute/"+id+"?noCache=true", strings.NewReader("{}"))
	req.Header.Set("X-Origin", "benchmark")
	req = consumerRequest(req)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("consumer in staging: want 403, got %d", rr.Code)
	}
}

// TestBenchmark_NoCacheWithoutBenchmarkOrigin_AllowedInProduction verifies regular noCache still works.
func TestBenchmark_NoCacheWithoutBenchmarkOrigin_AllowedInProduction(t *testing.T) {
	ps, id := buildExecuteStore(t)
	withProductionEnv(t)
	mux := newAdminMux(ps, nil)

	req := httptest.NewRequest(http.MethodPost, "/execute/"+id+"?noCache=true", strings.NewReader("{}"))
	// No X-Origin header — not a benchmark origin
	req = approverRequest(req)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// No staging restriction applies when origin != benchmark
	if rr.Code != http.StatusOK {
		t.Fatalf("noCache without benchmark origin: want 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestBenchmark_NoClaimsInStaging_Forbidden verifies unauthenticated benchmark is rejected.
func TestBenchmark_NoClaimsInStaging_Forbidden(t *testing.T) {
	ps, id := buildExecuteStore(t)
	withStagingEnv(t)
	mux := newAdminMux(ps, nil)

	req := httptest.NewRequest(http.MethodPost, "/execute/"+id+"?noCache=true", nil)
	req.Header.Set("X-Origin", "benchmark")
	// No auth claims
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("no claims in staging: want 403, got %d", rr.Code)
	}
}
