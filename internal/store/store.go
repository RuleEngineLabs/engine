package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/policy"
)

// reservedPolicyNames are policy names the system reserves for internal use.
var reservedPolicyNames = map[string]struct{}{
	"Preview": {}, "Health": {}, "Metrics": {}, "Admin": {},
}

// ErrNameReserved is returned when a policy name collides with a reserved name.
type ErrNameReserved struct{ Name string }

func (e ErrNameReserved) Error() string {
	return fmt.Sprintf("policy name %s is reserved", e.Name)
}

// ErrNameExists is returned when a policy with the same name already exists.
type ErrNameExists struct{ Name string }

func (e ErrNameExists) Error() string { return "policy already exists" }

// PolicyRecord holds the stored data for a single policy publication.
type PolicyRecord struct {
	PolicyID string
	Version  int
	Name     string
	Owner    string
	Policy   *policy.Policy
	Artifact *compiler.Artifact
}

// PolicyStore is an in-memory repository for published policies.
// It is safe for concurrent use.
type PolicyStore struct {
	mu      sync.RWMutex
	records map[string]*PolicyRecord // key: policyId
	byName  map[string]*PolicyRecord // key: normalized name
}

// New returns an initialized PolicyStore.
func New() *PolicyStore {
	return &PolicyStore{
		records: make(map[string]*PolicyRecord),
		byName:  make(map[string]*PolicyRecord),
	}
}

// Create validates and stores a compiled artifact, returning the assigned policyId and version.
// Returns ErrNameReserved if the name is reserved, ErrNameExists if the name is taken.
func (s *PolicyStore) Create(name, owner string, art *compiler.Artifact) (policyID string, version int, err error) {
	if _, reserved := reservedPolicyNames[name]; reserved {
		return "", 0, ErrNameReserved{Name: name}
	}

	id, err := generateID()
	if err != nil {
		return "", 0, fmt.Errorf("store: generate id: %w", err)
	}

	rec := &PolicyRecord{
		PolicyID: id,
		Version:  1,
		Name:     name,
		Owner:    owner,
		Policy:   art.Policy,
		Artifact: art,
	}

	normalized := strings.ToLower(name)
	s.mu.Lock()
	if _, exists := s.byName[normalized]; exists {
		s.mu.Unlock()
		return "", 0, ErrNameExists{Name: name}
	}
	s.records[id] = rec
	s.byName[normalized] = rec
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
