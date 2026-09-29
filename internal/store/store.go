package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/policy"
)

// PolicyRecord holds the stored data for a single policy publication.
type PolicyRecord struct {
	PolicyID string
	Version  int
	Name     string
	Policy   *policy.Policy
	Artifact *compiler.Artifact
}

// versionPtr holds the current version atomically for a single policyId.
// Readers get a stable *PolicyRecord pointer regardless of concurrent writes.
type versionPtr struct {
	_ [64]byte // cache-line padding to avoid false sharing
	v atomic.Pointer[PolicyRecord]
}

// PolicyStore is an in-memory repository for published policies.
// It is safe for concurrent use.
type PolicyStore struct {
	mu       sync.RWMutex
	records  map[string]*PolicyRecord // key: policyId (all versions, latest wins)
	versions map[string]*versionPtr   // key: policyId → current version pointer
}

// New returns an initialized PolicyStore.
func New() *PolicyStore {
	return &PolicyStore{
		records:  make(map[string]*PolicyRecord),
		versions: make(map[string]*versionPtr),
	}
}

// Create stores a compiled artifact and returns the assigned policyId and version.
func (s *PolicyStore) Create(name string, art *compiler.Artifact) (policyID string, version int, err error) {
	id, err := generateID()
	if err != nil {
		return "", 0, fmt.Errorf("store: generate id: %w", err)
	}
	rec := &PolicyRecord{
		PolicyID: id,
		Version:  1,
		Name:     name,
		Policy:   art.Policy,
		Artifact: art,
	}
	vp := &versionPtr{}
	vp.v.Store(rec)

	s.mu.Lock()
	s.records[id] = rec
	s.versions[id] = vp
	s.mu.Unlock()
	return id, 1, nil
}

// Publish atomically publishes a new version of an existing policy.
// Returns the new version number. The caller must also invalidate the
// old artifact from the ArtifactCache (blue-green handoff).
func (s *PolicyStore) Publish(policyID string, art *compiler.Artifact) (version int, err error) {
	s.mu.Lock()
	prev, ok := s.records[policyID]
	if !ok {
		s.mu.Unlock()
		return 0, fmt.Errorf("policy %q not found", policyID)
	}
	newVer := prev.Version + 1
	rec := &PolicyRecord{
		PolicyID: policyID,
		Version:  newVer,
		Name:     prev.Name,
		Policy:   art.Policy,
		Artifact: art,
	}
	s.records[policyID] = rec
	vp := s.versions[policyID]
	s.mu.Unlock()

	// Atomic swap: in-flight requests that loaded the old pointer complete safely.
	vp.v.Store(rec)
	return newVer, nil
}

// Current returns the current (latest) record for policyId via the atomic pointer.
func (s *PolicyStore) Current(policyID string) (*PolicyRecord, error) {
	s.mu.RLock()
	vp, ok := s.versions[policyID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("policy %q not found", policyID)
	}
	return vp.v.Load(), nil
}

// Get retrieves a policy record by policyId.
func (s *PolicyStore) Get(policyID string) (*PolicyRecord, error) {
	s.mu.RLock()
	rec, ok := s.records[policyID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("policy %q not found", policyID)
	}
	return rec, nil
}

// List returns all stored policy records.
func (s *PolicyStore) List() []*PolicyRecord {
	s.mu.RLock()
	out := make([]*PolicyRecord, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r)
	}
	s.mu.RUnlock()
	return out
}

func generateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:4]) + "-" +
		hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" +
		hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:]), nil
}
