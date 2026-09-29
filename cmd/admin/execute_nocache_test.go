package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RuleEngineLabs/engine/internal/auth"
	"github.com/RuleEngineLabs/engine/internal/ratelimit"
	"github.com/RuleEngineLabs/engine/internal/store"
)

var noCacheAuthSecret = []byte("test-nocache-secret")

func newNoCacheServer(rl *ratelimit.Limiter, v auth.Verifier) (*http.ServeMux, string) {
	ps := store.New()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /policies", handleCreate(ps))
	wrapped := func(h http.HandlerFunc) http.Handler {
		if v != nil {
			return auth.Middleware(v, h)
		}
		return h
	}
	mux.Handle("POST /execute/{id}", wrapped(handleExecute(ps, rl)))

	// Create a policy and return its ID
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies", bytes.NewBufferString(`{
		"name":"noCachePol",
		"entry":"s",
		"states":[{"id":"s","kind":"response","status":200,"data":{"ok":true}}]
	}`))
	mux.ServeHTTP(w, r)
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	return mux, resp["policyId"].(string)
}

func noCacheBearerHeader(groups []string) string {
	return "Bearer " + auth.MakeTestToken(noCacheAuthSecret, groups, time.Now().Add(time.Hour))
}

func TestHandleExecute_NoCacheAnyAuthenticatedUser(t *testing.T) {
	v := &auth.HMACVerifier{Secret: noCacheAuthSecret}
	srv, id := newNoCacheServer(nil, v)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/execute/"+id+"?noCache=true", bytes.NewBufferString("{}"))
	r.Header.Set("Authorization", noCacheBearerHeader([]string{"team-regular"}))
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for noCache by regular user, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleExecute_NoCacheNoSpecialRoleRequired(t *testing.T) {
	v := &auth.HMACVerifier{Secret: noCacheAuthSecret}
	// Regular user without policy-operators — must NOT get 403
	srv, id := newNoCacheServer(nil, v)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/execute/"+id+"?noCache=true", bytes.NewBufferString("{}"))
	r.Header.Set("Authorization", noCacheBearerHeader([]string{"team-a"}))
	srv.ServeHTTP(w, r)

	if w.Code == http.StatusForbidden {
		t.Fatalf("noCache should not require special role in production, got 403")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleExecute_NoCacheRateLimit(t *testing.T) {
	v := &auth.HMACVerifier{Secret: noCacheAuthSecret}
	rl := ratelimit.New(time.Second, 3) // low threshold for test
	srv, id := newNoCacheServer(rl, v)

	makeReq := func() int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/execute/"+id+"?noCache=true", bytes.NewBufferString("{}"))
		r.Header.Set("Authorization", noCacheBearerHeader([]string{"team-rl"}))
		srv.ServeHTTP(w, r)
		return w.Code
	}

	// First 3 should succeed
	for i := 0; i < 3; i++ {
		if code := makeReq(); code != http.StatusOK {
			t.Fatalf("call %d: expected 200, got %d", i+1, code)
		}
	}
	// 4th should be rate limited
	if code := makeReq(); code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 4th call, got %d", code)
	}
}

func TestHandleExecute_NoCacheBenchmark_ForbiddenWithoutApprover(t *testing.T) {
	v := &auth.HMACVerifier{Secret: noCacheAuthSecret}
	srv, id := newNoCacheServer(nil, v)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/execute/"+id+"?noCache=true", bytes.NewBufferString("{}"))
	r.Header.Set("Authorization", noCacheBearerHeader([]string{"team-regular"}))
	r.Header.Set("X-Origin", "benchmark")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("benchmark noCache without approver: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleExecute_NoCacheBenchmark_GlobalOperatorAllowed(t *testing.T) {
	// benchmark origin requires staging environment (US-029)
	isStaging = func() bool { return true }
	t.Cleanup(func() { isStaging = func() bool { return false } })

	v := &auth.HMACVerifier{Secret: noCacheAuthSecret}
	srv, id := newNoCacheServer(nil, v)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/execute/"+id+"?noCache=true", bytes.NewBufferString("{}"))
	r.Header.Set("Authorization", noCacheBearerHeader([]string{auth.GlobalOperatorGroup}))
	r.Header.Set("X-Origin", "benchmark")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("global operator benchmark noCache: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}
