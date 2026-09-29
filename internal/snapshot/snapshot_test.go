package snapshot_test

import (
	"context"
	"testing"

	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/policy"
	"github.com/RuleEngineLabs/engine/internal/snapshot"
	"github.com/RuleEngineLabs/engine/internal/store"
)

func makeRecord(t *testing.T, name string) *store.PolicyRecord {
	t.Helper()
	p := &policy.Policy{
		Name:  name,
		Entry: "s",
		States: []policy.State{
			{ID: "s", Kind: policy.KindResponse, Status: 200},
		},
	}
	art, err := compiler.Compile(p)
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
	return &store.PolicyRecord{
		PolicyID:      name + "-id",
		Version:       1,
		Name:          name,
		StableVersion: "1.0.0",
		Policy:        p,
		Artifact:      art,
	}
}

func TestBoot_PrimaryAvailable_S3NotRead(t *testing.T) {
	primary := snapshot.NewMemoryStore(
		makeRecord(t, "pol1"),
		makeRecord(t, "pol2"),
		makeRecord(t, "pol3"),
	)
	fallback := snapshot.NewMemoryStore() // should not be called

	records, err := snapshot.Boot(context.Background(), primary, fallback)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	if len(records) != 3 {
		t.Errorf("expected 3 records from primary, got %d", len(records))
	}
	if fallback.Calls() != 0 {
		t.Errorf("fallback should not be called when primary succeeds, got %d calls", fallback.Calls())
	}
}

func TestBoot_PrimaryUnavailable_FallbackUsed(t *testing.T) {
	primary := snapshot.UnavailableLoader{}
	fallback := snapshot.NewMemoryStore(
		makeRecord(t, "snap1"),
		makeRecord(t, "snap2"),
		makeRecord(t, "snap3"),
	)

	records, err := snapshot.Boot(context.Background(), primary, fallback)
	if err != nil {
		t.Fatalf("boot with fallback: %v", err)
	}
	if len(records) != 3 {
		t.Errorf("expected 3 records from fallback, got %d", len(records))
	}
}

func TestBoot_FallbackPopulatesStore(t *testing.T) {
	r1 := makeRecord(t, "ordersPolicy")
	r1.StableVersion = "1.2.0"

	primary := snapshot.UnavailableLoader{}
	fallback := snapshot.NewMemoryStore(r1)

	records, err := snapshot.Boot(context.Background(), primary, fallback)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}

	// Bootstrap into store
	ps := store.New()
	ps.Bootstrap(records)

	rec, ok := ps.GetByName("ordersPolicy")
	if !ok {
		t.Fatal("expected ordersPolicy in store after bootstrap")
	}
	if rec.StableVersion != "1.2.0" {
		t.Errorf("expected StableVersion=1.2.0, got %q", rec.StableVersion)
	}
}

func TestBoot_PromotionUpdatesSnapshot(t *testing.T) {
	ctx := context.Background()
	snap := snapshot.NewMemoryStore()

	// Create a policy record and persist it
	r := makeRecord(t, "ordersPolicy")
	r.StableVersion = "1.2.0"
	snap.Save(ctx, "ordersPolicy", r)

	// Simulate promotion: new record with bumped version
	updated := *r
	updated.StableVersion = "1.2.1"
	snap.Save(ctx, "ordersPolicy", &updated)

	// Reload from snapshot
	records, _ := snap.LoadAll(ctx)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].StableVersion != "1.2.1" {
		t.Errorf("expected 1.2.1 after snapshot update, got %q", records[0].StableVersion)
	}
}
