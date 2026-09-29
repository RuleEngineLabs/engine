package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RuleEngineLabs/engine/internal/auth"
	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/policy"
	"github.com/RuleEngineLabs/engine/internal/store"
)

var testAuthSecret = []byte("test-approve-secret")

func newApproveMux(ps *store.PolicyStore, v auth.Verifier) *http.ServeMux {
	mux := http.NewServeMux()
	wrapped := func(h http.HandlerFunc) http.Handler {
		if v != nil {
			return auth.Middleware(v, h)
		}
		return h
	}
	mux.Handle("POST /policies/{name}/versions/{version}/approve",
		wrapped(handleApproveDraft(ps)))
	mux.Handle("PATCH /policies/{name}/meta",
		wrapped(handleSetMeta(ps)))
	return mux
}

func makeApproveStore(t *testing.T, name, owner string) *store.PolicyStore {
	t.Helper()
	ps := store.New()
	p := &policy.Policy{
		Name:  name,
		Entry: "s",
		States: []policy.State{
			{ID: "s", Kind: policy.KindResponse, Status: 200},
		},
	}
	art, err := compiler.Compile(p)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, _, err := ps.Create(name, owner, art); err != nil {
		t.Fatalf("create: %v", err)
	}
	ps.UpsertDraft(name, "1.0.0", p, art)
	return ps
}

func bearerHeader(groups []string) string {
	return "Bearer " + auth.MakeTestToken(testAuthSecret, groups, time.Now().Add(time.Hour))
}

func TestHandleApproveDraft_ApproverAuthorized(t *testing.T) {
	ps := makeApproveStore(t, "ordersPolicy", "team-payments")
	// Set alice@team as approver
	ps.SetMeta("ordersPolicy", &store.PolicyMeta{Approvers: []string{"alice@team"}})

	v := &auth.HMACVerifier{Secret: testAuthSecret}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/versions/1/approve", bytes.NewBufferString("{}"))
	r.Header.Set("Authorization", bearerHeader([]string{"alice@team"}))
	newApproveMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "APPROVED" {
		t.Errorf("expected status=APPROVED, got %q", resp["status"])
	}
}

func TestHandleApproveDraft_EmptyApprovers_OwnerCanApprove(t *testing.T) {
	ps := makeApproveStore(t, "ordersPolicy", "team-payments")
	// No approvers set → owner fallback

	v := &auth.HMACVerifier{Secret: testAuthSecret}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/versions/1/approve", bytes.NewBufferString("{}"))
	r.Header.Set("Authorization", bearerHeader([]string{"team-payments"}))
	newApproveMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleApproveDraft_NonApprover_Forbidden(t *testing.T) {
	ps := makeApproveStore(t, "ordersPolicy", "team-payments")
	ps.SetMeta("ordersPolicy", &store.PolicyMeta{Approvers: []string{"alice@team"}})

	v := &auth.HMACVerifier{Secret: testAuthSecret}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/versions/1/approve", bytes.NewBufferString("{}"))
	r.Header.Set("Authorization", bearerHeader([]string{"bob@team"}))
	newApproveMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleApproveDraft_GlobalOperator_Authorized(t *testing.T) {
	ps := makeApproveStore(t, "ordersPolicy", "team-payments")
	ps.SetMeta("ordersPolicy", &store.PolicyMeta{Approvers: []string{"alice@team"}})

	v := &auth.HMACVerifier{Secret: testAuthSecret}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/policies/ordersPolicy/versions/1/approve", bytes.NewBufferString("{}"))
	r.Header.Set("Authorization", bearerHeader([]string{auth.GlobalOperatorGroup}))
	newApproveMux(ps, v).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("global operator: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}
