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

var covAuthSecret = []byte("test-cov-secret")

func covBearerHeader(groups []string) string {
	return "Bearer " + auth.MakeTestToken(covAuthSecret, groups, time.Now().Add(time.Hour))
}

// ---- handleSetMeta coverage ----

func TestHandleSetMeta_PolicyNotFound(t *testing.T) {
	ps := store.New()
	mux := newMetaMux(ps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/policies/notexist/meta", bytes.NewBufferString(`{"coexistence_window_seconds":100}`))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleSetMeta_BadJSON(t *testing.T) {
	ps := storeWithPolicy(t, "mypol")
	mux := newMetaMux(ps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/policies/mypol/meta", bytes.NewBufferString("{bad"))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- handlePromote coverage ----

func TestHandlePromote_NoDraft(t *testing.T) {
	ps := storeWithPolicy(t, "mypol") // no draft created
	mux := newMetaMux(ps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/mypol/versions", bytes.NewBufferString(`{"bump":"patch"}`))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for no draft, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlePromote_BadJSON(t *testing.T) {
	ps := storeWithPolicy(t, "mypol")
	mux := newVersionsMux(ps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/mypol/versions", bytes.NewBufferString("{bad"))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ---- handleListVersions / handleGetVersion coverage ----

func newSearchCovMux(ps *store.PolicyStore) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /policies/{name}/versions", handleListVersions(ps))
	mux.HandleFunc("GET /policies/{name}/versions/{version}", handleGetVersion(ps))
	return mux
}

func TestHandleListVersions_NotFound(t *testing.T) {
	ps := store.New()
	mux := newSearchCovMux(ps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/policies/notexist/versions", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleGetVersion_NotFound(t *testing.T) {
	ps := store.New()
	mux := newSearchCovMux(ps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/policies/notexist/versions/1", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleGetVersion_VersionNotFound(t *testing.T) {
	ps := storeWithDraft(t, "mypol") // has version 1, not version 99
	mux := newSearchCovMux(ps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/policies/mypol/versions/99", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for nonexistent version, got %d", w.Code)
	}
}

// ---- handleApproveDraft coverage ----

func TestHandleApproveDraft_NoDraft(t *testing.T) {
	ps := makeApproveStore(t, "mypol", "team-a")
	// Consume the draft by promoting it
	ps.ApproveDraft("mypol")
	ps.Promote("mypol", "patch")
	// Now no draft exists

	v := &auth.HMACVerifier{Secret: testAuthSecret}
	mux := newApproveMux(ps, v)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/mypol/versions/1/approve", bytes.NewBufferString("{}"))
	r.Header.Set("Authorization", bearerHeader([]string{"team-a"}))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when no draft, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- handleDeleteVersion coverage ----

func TestHandleDeleteVersion_VersionNotFound(t *testing.T) {
	ps := makeApproveStore(t, "mypol", "team-a")
	v := &auth.HMACVerifier{Secret: testAuthSecret}
	mux := newDeleteMux(ps, v)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/policies/mypol/versions/99", bytes.NewBufferString(`{"reason":"gone"}`))
	r.Header.Set("Authorization", bearerHeader([]string{"team-a"}))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for nonexistent version, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleDeleteVersion_BadJSON(t *testing.T) {
	ps := makeApproveStore(t, "mypol", "team-a")
	v := &auth.HMACVerifier{Secret: testAuthSecret}
	mux := newDeleteMux(ps, v)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/policies/mypol/versions/1", bytes.NewBufferString("{bad"))
	r.Header.Set("Authorization", bearerHeader([]string{"team-a"}))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad JSON, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleDeleteVersion_PolicyNotFound(t *testing.T) {
	ps := store.New() // empty store
	v := &auth.HMACVerifier{Secret: testAuthSecret}
	mux := newDeleteMux(ps, v)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/policies/notexist/versions/1", bytes.NewBufferString(`{"reason":"x"}`))
	r.Header.Set("Authorization", bearerHeader([]string{"team-a"}))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for policy not found, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- canary handler error paths ----

func newCanaryCovMux(ps *store.PolicyStore, v auth.Verifier) *http.ServeMux {
	mux := http.NewServeMux()
	wrapped := func(h http.HandlerFunc) http.Handler {
		if v != nil {
			return auth.Middleware(v, h)
		}
		return h
	}
	mux.Handle("POST /policies/{name}/canary", wrapped(handleStartCanary(ps)))
	mux.Handle("PATCH /policies/{name}/canary", wrapped(handleExtendCanary(ps)))
	mux.Handle("DELETE /policies/{name}/canary", wrapped(handleCancelCanary(ps)))
	return mux
}

func TestHandleStartCanary_BadJSON(t *testing.T) {
	ps := makeApproveStore(t, "mypol", "team-a")
	v := &auth.HMACVerifier{Secret: covAuthSecret}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/mypol/canary", bytes.NewBufferString("{bad"))
	r.Header.Set("Authorization", covBearerHeader([]string{"team-a"}))
	newCanaryCovMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleStartCanary_PolicyNotFound(t *testing.T) {
	ps := store.New()
	v := &auth.HMACVerifier{Secret: covAuthSecret}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/notexist/canary", bytes.NewBufferString(`{"percent":10,"ttl":3600}`))
	r.Header.Set("Authorization", covBearerHeader([]string{"team-a"}))
	newCanaryCovMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleStartCanary_AlreadyActive_Conflict(t *testing.T) {
	ps := makeApproveStore(t, "mypol", "team-a")
	ps.StartCanary("mypol", "1.2.0", 10, 3600) // start first canary
	v := &auth.HMACVerifier{Secret: covAuthSecret}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/mypol/canary", bytes.NewBufferString(`{"candidateVersion":"1.3.0","percent":20,"ttl":3600}`))
	r.Header.Set("Authorization", covBearerHeader([]string{"team-a"}))
	newCanaryCovMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for already active, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleExtendCanary_BadJSON(t *testing.T) {
	ps := makeApproveStore(t, "mypol", "team-a")
	ps.StartCanary("mypol", "1.2.0", 10, 3600)
	v := &auth.HMACVerifier{Secret: covAuthSecret}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/policies/mypol/canary", bytes.NewBufferString("{bad"))
	r.Header.Set("Authorization", covBearerHeader([]string{"team-a"}))
	newCanaryCovMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleExtendCanary_PolicyNotFound(t *testing.T) {
	ps := store.New()
	v := &auth.HMACVerifier{Secret: covAuthSecret}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/policies/notexist/canary", bytes.NewBufferString(`{"percent":50}`))
	r.Header.Set("Authorization", covBearerHeader([]string{"team-a"}))
	newCanaryCovMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleExtendCanary_NoActiveCanary(t *testing.T) {
	ps := makeApproveStore(t, "mypol", "team-a") // no canary started
	v := &auth.HMACVerifier{Secret: covAuthSecret}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/policies/mypol/canary", bytes.NewBufferString(`{"percent":50}`))
	r.Header.Set("Authorization", covBearerHeader([]string{"team-a"}))
	newCanaryCovMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for no active canary, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCancelCanary_BadJSON(t *testing.T) {
	ps := makeApproveStore(t, "mypol", "team-a")
	ps.StartCanary("mypol", "1.2.0", 10, 3600)
	v := &auth.HMACVerifier{Secret: covAuthSecret}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/policies/mypol/canary", bytes.NewBufferString("{bad"))
	r.Header.Set("Authorization", covBearerHeader([]string{"team-a"}))
	newCanaryCovMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCancelCanary_PolicyNotFound(t *testing.T) {
	ps := store.New()
	v := &auth.HMACVerifier{Secret: covAuthSecret}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/policies/notexist/canary", bytes.NewBufferString(`{"reason":"x"}`))
	r.Header.Set("Authorization", covBearerHeader([]string{"team-a"}))
	newCanaryCovMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- execute handler coverage ----

func TestHandleExecute_NoCacheRemoteAddrCallerKey(t *testing.T) {
	// noCache without auth uses RemoteAddr as rate limit key
	rl := ratelimit.New(time.Second, 5)
	srv, id := newNoCacheServer(rl, nil) // no auth verifier → claims will be nil

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/execute/"+id+"?noCache=true", bytes.NewBufferString("{}"))
	// No Authorization header → claims nil → uses RemoteAddr
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with RemoteAddr key, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleExecute_NoCacheIsOperatorOrApproverFalseNilClaims(t *testing.T) {
	// isOperatorOrApprover with nil claims returns false
	result := isOperatorOrApprover(nil)
	if result {
		t.Error("expected false for nil claims")
	}
}

// ---- meta handler coverage ----

func TestHandleSetMeta_SetsApprovers(t *testing.T) {
	ps := storeWithPolicy(t, "mypol")
	mux := newMetaMux(ps)

	body := `{"approvers":["team-reviewers"]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/policies/mypol/meta", bytes.NewBufferString(body))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	approvers, _ := resp["approvers"].([]any)
	if len(approvers) != 1 || approvers[0] != "team-reviewers" {
		t.Errorf("expected approvers=[team-reviewers], got %v", approvers)
	}
}
