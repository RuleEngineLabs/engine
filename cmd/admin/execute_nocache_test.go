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

	makeReqFull := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/execute/"+id+"?noCache=true", bytes.NewBufferString("{}"))
		r.Header.Set("Authorization", noCacheBearerHeader([]string{"team-rl"}))
		srv.ServeHTTP(w, r)
		return w
	}

	// First 3 should succeed — Retry-After must be absent.
	for i := 0; i < 3; i++ {
		w := makeReqFull()
		if w.Code != http.StatusOK {
			t.Fatalf("call %d: expected 200, got %d", i+1, w.Code)
		}
		// Fix 3: Retry-After must NOT appear on successful requests.
		if ra := w.Header().Get("Retry-After"); ra != "" {
			t.Errorf("call %d: Retry-After must be absent on 200, got %q", i+1, ra)
		}
	}
	// 4th should be rate limited — Retry-After must be "1".
	w := makeReqFull()
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 4th call, got %d", w.Code)
	}
	// Fix 3: Retry-After must be "1" on 429.
	if ra := w.Header().Get("Retry-After"); ra != "1" {
		t.Errorf("expected Retry-After=1 on 429, got %q", ra)
	}
	// Fix 5: duration_ms must be absent on 429.
	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	if _, hasDms := body["duration_ms"]; hasDms {
		t.Error("duration_ms must not appear in error response (429)")
	}
}

func TestHandleExecute_NoCacheBenchmark_ForbiddenWithoutApprover(t *testing.T) {
	// staging=true so the staging gate passes; we want to test the role gate.
	orig := isStaging
	isStaging = func() bool { return true }
	t.Cleanup(func() { isStaging = orig })

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
	orig := isStaging
	isStaging = func() bool { return true }
	t.Cleanup(func() { isStaging = orig })

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
