package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/policy"
	"github.com/RuleEngineLabs/engine/internal/store"
)

// newHTTPConfigMux registers execute for both GET and POST, used to test Cache-Control logic.
func newHTTPConfigMux(ps *store.PolicyStore) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /execute/{id}", handleExecute(ps, nil))
	mux.HandleFunc("POST /execute/{id}", handleExecute(ps, nil))
	return mux
}

// bootstrapResponsePolicy compiles a single-state response policy and stores it directly.
// Returns the PolicyID used for execute calls.
func bootstrapResponsePolicy(t *testing.T, ps *store.PolicyStore, name string, status int) string {
	t.Helper()
	p := &policy.Policy{
		Name:  name,
		Entry: "s",
		States: []policy.State{
			{ID: "s", Kind: policy.KindResponse, Status: status},
		},
	}
	art, err := compiler.Compile(p)
	if err != nil {
		t.Fatalf("compile %q: %v", name, err)
	}
	id := name + "-id"
	ps.Bootstrap([]*store.PolicyRecord{{
		PolicyID: id,
		Name:     name,
		Owner:    "team-a",
		Policy:   p,
		Artifact: art,
	}})
	return id
}

func TestHandleExecute_HTTPConfig_Timeout(t *testing.T) {
	block := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(block) // LIFO: runs first, unblocks goroutine before Close waits for it

	p := &policy.Policy{
		Name:  "slow-policy",
		Entry: "call",
		States: []policy.State{
			{
				ID:          "call",
				Kind:        policy.KindAPICall,
				URL:         slow.URL + "/block",
				Method:      "GET",
				Transitions: []policy.Transition{{When: "true", To: "done"}},
			},
			{ID: "done", Kind: policy.KindResponse, Status: 200},
		},
	}
	art, err := compiler.Compile(p)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	ps := store.New()
	ps.Bootstrap([]*store.PolicyRecord{{
		PolicyID: "slow-id",
		Name:     "slow-policy",
		Owner:    "team-a",
		Policy:   p,
		Artifact: art,
	}})
	if err := ps.SetMeta("slow-policy", &store.PolicyMeta{
		HTTP: store.HTTPConfig{TimeoutMs: 100},
	}); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	mux := newHTTPConfigMux(ps)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/execute/slow-id", bytes.NewBufferString("{}"))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on timeout, got %d: %s", w.Code, w.Body.String())
	}
	var res map[string]string
	json.NewDecoder(w.Body).Decode(&res)
	if res["error"] != "internal execution error" {
		t.Errorf("expected masked error, got %q", res["error"])
	}
}

func TestHandleExecute_HTTPConfig_RetryAfterOnMatch(t *testing.T) {
	ps := store.New()
	id := bootstrapResponsePolicy(t, ps, "retry-policy", http.StatusTooManyRequests)
	if err := ps.SetMeta("retry-policy", &store.PolicyMeta{
		HTTP: store.HTTPConfig{
			RetryOn:           []int{http.StatusTooManyRequests},
			RetryAfterSeconds: 60,
		},
	}); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	mux := newHTTPConfigMux(ps)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/execute/"+id, bytes.NewBufferString("{}"))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got != "60" {
		t.Errorf("expected Retry-After: 60, got %q", got)
	}
}

func TestHandleExecute_HTTPConfig_RetryAfterNoMatch(t *testing.T) {
	ps := store.New()
	id := bootstrapResponsePolicy(t, ps, "noretry-policy", http.StatusInternalServerError)
	if err := ps.SetMeta("noretry-policy", &store.PolicyMeta{
		HTTP: store.HTTPConfig{
			RetryOn:           []int{http.StatusTooManyRequests},
			RetryAfterSeconds: 60,
		},
	}); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	mux := newHTTPConfigMux(ps)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/execute/"+id, bytes.NewBufferString("{}"))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "" {
		t.Errorf("expected no Retry-After on status mismatch, got %q", got)
	}
}

func TestHandleExecute_HTTPConfig_CacheControlOnGET(t *testing.T) {
	ps := store.New()
	id := bootstrapResponsePolicy(t, ps, "cache-policy", http.StatusOK)
	if err := ps.SetMeta("cache-policy", &store.PolicyMeta{
		HTTP: store.HTTPConfig{CacheMaxAgeSeconds: 300},
	}); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	mux := newHTTPConfigMux(ps)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/execute/"+id, nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "max-age=300" {
		t.Errorf("expected Cache-Control: max-age=300 on GET, got %q", got)
	}
}

func TestHandleExecute_HTTPConfig_CacheControlAbsentOnPOST(t *testing.T) {
	ps := store.New()
	id := bootstrapResponsePolicy(t, ps, "cache-post-policy", http.StatusOK)
	if err := ps.SetMeta("cache-post-policy", &store.PolicyMeta{
		HTTP: store.HTTPConfig{CacheMaxAgeSeconds: 300},
	}); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	mux := newHTTPConfigMux(ps)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/execute/"+id, bytes.NewBufferString("{}"))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "" {
		t.Errorf("expected no Cache-Control on POST, got %q", got)
	}
}

func TestHandleExecute_HTTPConfig_PolicyStatusForwarded(t *testing.T) {
	ps := store.New()
	id := bootstrapResponsePolicy(t, ps, "status-policy", http.StatusUnprocessableEntity)

	mux := newHTTPConfigMux(ps)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/execute/"+id, bytes.NewBufferString("{}"))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 from policy state, got %d: %s", w.Code, w.Body.String())
	}
	var res map[string]any
	json.NewDecoder(w.Body).Decode(&res)
	if res["state"] != "s" {
		t.Errorf("expected state=s, got %v", res["state"])
	}
}

func TestHandleSetMeta_SetsHTTPConfig(t *testing.T) {
	ps := storeWithPolicy(t, "http-meta-pol")
	mux := newMetaMux(ps)

	body := `{"http":{"retry_on":[429,503],"retry_after_seconds":30,"timeout_ms":5000,"cache_max_age_seconds":120}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/policies/http-meta-pol/meta", bytes.NewBufferString(body))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	m := ps.GetMeta("http-meta-pol")
	if m == nil {
		t.Fatal("meta not stored")
	}
	if m.HTTP.RetryAfterSeconds != 30 {
		t.Errorf("expected RetryAfterSeconds=30, got %d", m.HTTP.RetryAfterSeconds)
	}
	if m.HTTP.TimeoutMs != 5000 {
		t.Errorf("expected TimeoutMs=5000, got %d", m.HTTP.TimeoutMs)
	}
	if m.HTTP.CacheMaxAgeSeconds != 120 {
		t.Errorf("expected CacheMaxAgeSeconds=120, got %d", m.HTTP.CacheMaxAgeSeconds)
	}
	if len(m.HTTP.RetryOn) != 2 || m.HTTP.RetryOn[0] != 429 || m.HTTP.RetryOn[1] != 503 {
		t.Errorf("expected RetryOn=[429,503], got %v", m.HTTP.RetryOn)
	}
}
