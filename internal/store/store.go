package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

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
	PolicyID      string
	Version       int
	Name          string
	Owner         string
	StableVersion string // semver of the current STABLE release, e.g. "1.2.1"
	Policy        *policy.Policy
	Artifact      *compiler.Artifact
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
	byName   map[string]*PolicyRecord // key: normalized name (lowercase)
	versions map[string]*versionPtr   // key: policyId → current version pointer
	drafts   map[string]*DraftRecord  // key: normalized name → pending draft
	meta     map[string]*PolicyMeta   // key: normalized name → policy meta config
}

// New returns an initialized PolicyStore.
func New() *PolicyStore {
	return &PolicyStore{
		records:  make(map[string]*PolicyRecord),
		byName:   make(map[string]*PolicyRecord),
		versions: make(map[string]*versionPtr),
		drafts:   make(map[string]*DraftRecord),
		meta:     make(map[string]*PolicyMeta),
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
	vp := &versionPtr{}
	vp.v.Store(rec)

	s.mu.Lock()
	if _, exists := s.byName[normalized]; exists {
		s.mu.Unlock()
		return "", 0, ErrNameExists{Name: name}
	}
	s.records[id] = rec
	s.byName[normalized] = rec
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
		Owner:    prev.Owner,
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

// GetByName retrieves the latest policy record by name (case-insensitive).
func (s *PolicyStore) GetByName(name string) (*PolicyRecord, bool) {
	s.mu.RLock()
	rec, ok := s.byName[strings.ToLower(name)]
	s.mu.RUnlock()
	return rec, ok
}

// Bootstrap bulk-loads records from a trusted source (snapshot) into the store,
// bypassing reserved-name and uniqueness checks. Existing records are overwritten.
func (s *PolicyStore) Bootstrap(records []*PolicyRecord) {
	s.mu.Lock()
	for _, rec := range records {
		vp := &versionPtr{}
		vp.v.Store(rec)
		s.records[rec.PolicyID] = rec
		s.byName[strings.ToLower(rec.Name)] = rec
		s.versions[rec.PolicyID] = vp
	}
	s.mu.Unlock()
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
