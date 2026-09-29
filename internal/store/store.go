package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

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

// PolicyStore is an in-memory repository for published policies.
// It is safe for concurrent use.
type PolicyStore struct {
	mu      sync.RWMutex
	records map[string]*PolicyRecord
}

// New returns an initialized PolicyStore.
func New() *PolicyStore {
	return &PolicyStore{records: make(map[string]*PolicyRecord)}
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
	s.mu.Lock()
	s.records[id] = rec
	s.mu.Unlock()
	return id, 1, nil
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
