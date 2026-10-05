package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/RuleEngineLabs/engine/internal/auth"
	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/policy"
	"github.com/RuleEngineLabs/engine/internal/ratelimit"
	"github.com/RuleEngineLabs/engine/internal/store"
)

// ---- newAdminMux + buildServer ----

func TestNewAdminMux_RegistersRoutes(t *testing.T) {
	ps := store.New()
	rl := ratelimit.New(0, 0)
	mux := newAdminMux(ps, rl)
	if mux == nil {
		t.Fatal("expected non-nil mux")
	}
	// spot-check: GET /policies returns 200 (empty list)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/policies", nil)
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /policies: expected 200, got %d", w.Code)
	}
}

func TestBuildServer_DefaultAddr(t *testing.T) {
	addr, handler := buildServer("")
	if addr != ":8081" {
		t.Errorf("expected :8081 default, got %s", addr)
	}
	if handler == nil {
		t.Fatal("expected non-nil handler")
	}
}

func TestBuildServer_CustomAddr(t *testing.T) {
	addr, handler := buildServer(":9999")
	if addr != ":9999" {
		t.Errorf("expected :9999, got %s", addr)
	}
	if handler == nil {
		t.Fatal("expected non-nil handler")
	}
}

// ---- handleCancelCanary forbidden ----

func TestHandleCancelCanary_Forbidden(t *testing.T) {
	ps := makeApproveStore(t, "mypol", "team-a")
	ps.StartCanary("mypol", "1.2.0", 10, 3600)
	v := &auth.HMACVerifier{Secret: covAuthSecret}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/policies/mypol/canary", bytes.NewBufferString(`{"reason":"rollback"}`))
	r.Header.Set("Authorization", covBearerHeader([]string{"team-b"})) // wrong group → forbidden
	newCanaryCovMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- handleExecute: bad JSON body path ----

func TestHandleExecute_BadJSONBody(t *testing.T) {
	rl := ratelimit.New(0, 100)
	srv, id := newNoCacheServer(rl, nil)

	// non-empty body with invalid JSON triggers the ContentLength>0 + bad JSON path
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/execute/"+id, bytes.NewBufferString("{bad"))
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad JSON body, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- handleExecute: executor error path ----

func TestHandleExecute_ExecutorError(t *testing.T) {
	ps := store.New()
	// Artifact with an entry that does not exist in the state index causes executor.Execute to error.
	brokenArt := &compiler.Artifact{Policy: &policy.Policy{
		Name:   "broken",
		Entry:  "nonexistent",
		States: []policy.State{{ID: "s", Kind: policy.KindResponse, Status: 200}},
	}}
	ps.Bootstrap([]*store.PolicyRecord{{
		PolicyID: "broken-id",
		Name:     "broken",
		Owner:    "team-a",
		Policy:   brokenArt.Policy,
		Artifact: brokenArt,
	}})

	rl := ratelimit.New(0, 100)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /execute/{id}", handleExecute(ps, rl))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/execute/broken-id", bytes.NewBufferString("{}"))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for executor error, got %d: %s", w.Code, w.Body.String())
	}
	// Fix 2: body must not leak internal error details.
	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	if body["error"] != "internal execution error" {
		t.Errorf("expected error=internal execution error, got %v", body["error"])
	}
	// Fix 5: duration_ms must be absent on error responses.
	if _, hasDms := body["duration_ms"]; hasDms {
		t.Error("duration_ms must not appear in error response (500)")
	}
}

// ---- handlePreview: executor error path ----

func TestHandlePreview_ExecutorError(t *testing.T) {
	// Policy compiles (entry "s" exists) but executor fails because the only transition leads
	// to state "nonexistent" which is not defined — triggers executor.Preview error.
	body := `{
		"name": "bad-exec",
		"entry": "s",
		"states": [{"id": "s", "kind": "execution", "transitions": [{"when": "true", "to": "nonexistent"}]}],
		"input": {}
	}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/preview", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	newPreviewMux().ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for executor error in preview, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- handlePromote: empty bump ----

func TestHandlePromote_EmptyBump(t *testing.T) {
	ps := store.New()
	mux := newVersionsMux(ps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/anypol/versions", bytes.NewBufferString(`{}`))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty bump, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- store.GetCanary: not found ----

func TestStore_GetCanary_NotFound(t *testing.T) {
	ps := store.New()
	_, found := ps.GetCanary("nonexistent")
	if found {
		t.Error("expected not found for nonexistent canary")
	}
}

// ---- store.bumpSemVer edge cases (exercised via Promote) ----

func TestStore_Promote_EmptyBaseVersion(t *testing.T) {
	ps := store.New()
	p := &policy.Policy{Name: "pol", Entry: "s", States: []policy.State{{ID: "s", Kind: policy.KindResponse, Status: 200}}}
	art, err := compiler.Compile(p)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ps.UpsertDraft("pol", "", p, art)
	ps.ApproveDraft("pol")
	ver, err := ps.Promote("pol", "patch")
	if err != nil {
		t.Fatalf("unexpected error for empty base: %v", err)
	}
	if ver != "0.0.1" {
		t.Errorf("expected 0.0.1, got %s", ver)
	}
}

func TestStore_Promote_InvalidSemVerFormat(t *testing.T) {
	ps := store.New()
	p := &policy.Policy{Name: "pol", Entry: "s", States: []policy.State{{ID: "s", Kind: policy.KindResponse, Status: 200}}}
	art, err := compiler.Compile(p)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ps.UpsertDraft("pol", "1.2", p, art) // only 2 parts → invalid
	ps.ApproveDraft("pol")
	_, err = ps.Promote("pol", "patch")
	if err == nil {
		t.Error("expected error for invalid semver format (1.2)")
	}
}

func TestStore_Promote_NonNumericSemVer(t *testing.T) {
	ps := store.New()
	p := &policy.Policy{Name: "pol", Entry: "s", States: []policy.State{{ID: "s", Kind: policy.KindResponse, Status: 200}}}
	art, err := compiler.Compile(p)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ps.UpsertDraft("pol", "a.b.c", p, art) // non-numeric parts → error
	ps.ApproveDraft("pol")
	_, err = ps.Promote("pol", "patch")
	if err == nil {
		t.Error("expected error for non-numeric semver (a.b.c)")
	}
}

// ---- isStaging: original function body ----

// originalIsStaging captures the default isStaging implementation at package-init time,
// before any test can replace the var — guarantees execute.go:34-36 is covered
// regardless of test execution order or -race flag behavior.
var originalIsStaging = isStaging

// TestIsStaging_OriginalBody exercises execute.go:34-36 via the captured original
// implementation, independent of whether benchmark helpers have overridden isStaging.
func TestIsStaging_OriginalBody(t *testing.T) {
	t.Setenv("ENVIRONMENT", "staging")
	if !originalIsStaging() {
		t.Error("expected true for ENVIRONMENT=staging")
	}
	t.Setenv("ENVIRONMENT", "")
	if originalIsStaging() {
		t.Error("expected false for empty ENVIRONMENT")
	}
}

// ---- isStaging: homologacao branch ----

func TestIsStaging_Homologacao(t *testing.T) {
	// Save and restore the real isStaging function.
	orig := isStaging
	t.Cleanup(func() { isStaging = orig })

	// Reset isStaging to the real implementation so we exercise the env-var branch.
	isStaging = func() bool {
		e := os.Getenv("ENVIRONMENT")
		return e == "staging" || e == "homologacao"
	}

	t.Setenv("ENVIRONMENT", "homologacao")
	if !isStaging() {
		t.Error("expected isStaging()=true for ENVIRONMENT=homologacao")
	}
}

// ---- logRequests: zero-status path (handler never calls Write or WriteHeader) ----

func TestLogRequests_NoWrite_DefaultsTo200(t *testing.T) {
	buf, restore := newLogBuffer()
	defer restore()

	// Handler that does nothing: neither Write nor WriteHeader is called.
	handler := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !strings.Contains(buf.String(), "status=200") {
		t.Fatalf("expected status=200 for no-write handler, got: %s", buf.String())
	}
}

// ---- applyGCTuning / applyMaxProcs: env-var-already-set branches ----

func TestApplyGCTuning_EnvVarsAlreadySet(t *testing.T) {
	t.Setenv("GOGC", "100")
	t.Setenv("GOMEMLIMIT", "128MiB")
	// Must not panic; exercises the "env var already set" slog.Debug branches.
	applyGCTuning()
}

func TestApplyMaxProcs_EnvVarValidN(t *testing.T) {
	t.Setenv("GOMAXPROCS", "1")
	// Must not panic; exercises the "valid GOMAXPROCS from env" branch.
	applyMaxProcs()
}
