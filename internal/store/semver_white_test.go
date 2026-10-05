package store

import "testing"

// TestPromote_NoHistoryEntry covers semver.go:127-137 — the !updated branch in
// Promote that appends a new VersionRecord when no DRAFT/APPROVED history entry
// exists for the policy. Reachable only by directly injecting an approved draft
// without going through UpsertDraft (which always creates a history entry).
func TestPromote_NoHistoryEntry(t *testing.T) {
	ps := New()
	ps.mu.Lock()
	ps.drafts["nohist"] = &DraftRecord{
		PolicyName: "nohist",
		Status:     DraftStatusApproved,
	}
	ps.mu.Unlock()

	ver, err := ps.Promote("nohist", "patch")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if ver != "0.0.1" {
		t.Errorf("expected 0.0.1, got %s", ver)
	}
}
