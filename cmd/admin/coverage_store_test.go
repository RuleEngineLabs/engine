package main

import (
	"testing"

	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/policy"
	"github.com/RuleEngineLabs/engine/internal/store"
)

func compileSimple(t *testing.T, name string) *compiler.Artifact {
	t.Helper()
	p := &policy.Policy{
		Name:  name,
		Entry: "s",
		States: []policy.State{{ID: "s", Kind: policy.KindResponse, Status: 200}},
	}
	art, err := compiler.Compile(p)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return art
}

func TestStore_Publish(t *testing.T) {
	ps := store.New()
	art := compileSimple(t, "mypol")
	id, v1, err := ps.Create("mypol", "team-a", art)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if v1 != 1 {
		t.Errorf("expected version=1, got %d", v1)
	}

	art2 := compileSimple(t, "mypol")
	v2, err := ps.Publish(id, art2)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if v2 != 2 {
		t.Errorf("expected version=2, got %d", v2)
	}
}

func TestStore_Publish_NotFound(t *testing.T) {
	ps := store.New()
	_, err := ps.Publish("nonexistent-id", compileSimple(t, "x"))
	if err == nil {
		t.Error("expected error for non-existent policy")
	}
}

func TestStore_Current(t *testing.T) {
	ps := store.New()
	art := compileSimple(t, "curpol")
	id, _, err := ps.Create("curpol", "team-b", art)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	rec, err := ps.Current(id)
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if rec.PolicyID != id {
		t.Errorf("expected id=%s, got %s", id, rec.PolicyID)
	}

	// After publish, current should reflect new version
	art2 := compileSimple(t, "curpol")
	ps.Publish(id, art2)
	rec2, err := ps.Current(id)
	if err != nil {
		t.Fatalf("current after publish: %v", err)
	}
	if rec2.Version != 2 {
		t.Errorf("expected version=2 after publish, got %d", rec2.Version)
	}
}

func TestStore_Current_NotFound(t *testing.T) {
	ps := store.New()
	_, err := ps.Current("nonexistent")
	if err == nil {
		t.Error("expected error for non-existent policy")
	}
}

func TestStore_List(t *testing.T) {
	ps := store.New()
	ps.Create("polA", "team", compileSimple(t, "polA"))
	ps.Create("polB", "team", compileSimple(t, "polB"))

	recs := ps.List()
	if len(recs) != 2 {
		t.Errorf("expected 2 records, got %d", len(recs))
	}
}

func TestStore_ErrCanaryAlreadyActive_Error(t *testing.T) {
	err := store.ErrCanaryAlreadyActive{Name: "testPolicy"}
	if err.Error() == "" {
		t.Error("expected non-empty error string")
	}
}

func TestStore_bumpSemVer_InvalidBump(t *testing.T) {
	ps := store.New()
	art := compileSimple(t, "bumppol")
	ps.Create("bumppol", "team", art)

	// Need an approved draft to call Promote
	p := &policy.Policy{Name: "bumppol", Entry: "s", States: []policy.State{{ID: "s", Kind: policy.KindResponse, Status: 200}}}
	artD, _ := compiler.Compile(p)
	ps.UpsertDraft("bumppol", "1.0.0", p, artD)
	ps.ApproveDraft("bumppol")

	_, err := ps.Promote("bumppol", "invalid")
	if err == nil {
		t.Error("expected error for invalid bump type")
	}
}

func TestStore_ApproveDraft_NoDraft(t *testing.T) {
	ps := store.New()
	err := ps.ApproveDraft("nonexistent")
	if err == nil {
		t.Error("expected error for non-existent draft")
	}
}

func TestStore_Promote_NoDraft(t *testing.T) {
	ps := store.New()
	_, err := ps.Promote("nonexistent", "patch")
	if err == nil {
		t.Error("expected error for non-existent draft")
	}
}

func TestStore_ExtendCanary_AlreadyExpired(t *testing.T) {
	ps := store.New()
	ps.Bootstrap([]*store.PolicyRecord{{PolicyID: "x", Name: "pol", Owner: "team"}})
	ps.StartCanary("pol", "1.2.0", 10, 0) // TTL=0 → immediately expired
	_, err := ps.ExtendCanary("pol", 50)
	if err == nil {
		t.Error("expected ErrCanaryNotFound for expired canary")
	}
}

func TestStore_CancelCanary_AlreadyExpired(t *testing.T) {
	ps := store.New()
	ps.Bootstrap([]*store.PolicyRecord{{PolicyID: "x", Name: "pol", Owner: "team"}})
	ps.StartCanary("pol", "1.2.0", 10, 0) // TTL=0 → immediately expired
	_, err := ps.CancelCanary("pol", "test")
	if err == nil {
		t.Error("expected ErrCanaryNotFound for expired canary")
	}
}

func TestStore_GetVersion_SemVerNotFound(t *testing.T) {
	ps := store.New()
	_, found := ps.GetVersion("nonexistent", "1.0.0")
	if found {
		t.Error("expected not found for nonexistent version")
	}
}

func TestStore_GetVersion_IntegerNotFound(t *testing.T) {
	ps := store.New()
	_, found := ps.GetVersion("nonexistent", "99")
	if found {
		t.Error("expected not found")
	}
}

// TestStore_UpsertDraft_UnmarshalableData covers history.go:129 — the `return ""`
// inside hashPolicy when json.Marshal fails. State.Data is `any`, so passing a chan
// value triggers json.UnsupportedTypeError; UpsertDraft still stores the draft with
// an empty ContentHash (no panic or error is surfaced to callers).
func TestStore_UpsertDraft_UnmarshalableData(t *testing.T) {
	ps := store.New()
	p := &policy.Policy{
		Name:  "chpol",
		Entry: "s",
		States: []policy.State{
			{ID: "s", Kind: policy.KindResponse, Status: 200, Data: make(chan int)},
		},
	}
	ps.UpsertDraft("chpol", "", p, nil)
	if _, ok := ps.GetDraft("chpol"); !ok {
		t.Fatal("expected draft to be stored despite unmarshalable Data field")
	}
}

func TestStore_SetMeta_NotFound(t *testing.T) {
	ps := store.New()
	err := ps.SetMeta("nonexistent", &store.PolicyMeta{})
	if err == nil {
		t.Error("expected error for nonexistent policy")
	}
}
