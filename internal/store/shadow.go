package store

import (
	"fmt"
	"strings"
	"time"

	"github.com/RuleEngineLabs/engine/internal/compiler"
)

// ShadowStatus is the lifecycle state of a shadow deployment.
type ShadowStatus string

const (
	ShadowStatusActive  ShadowStatus = "ACTIVE"
	ShadowStatusStopped ShadowStatus = "STOPPED"
)

// ShadowRecord holds the state of a shadow deployment for a single policy.
// The candidate artifact is stored so execute can run it without a second lookup.
type ShadowRecord struct {
	PolicyName        string
	CandidateVersion  string
	CandidateArtifact *compiler.Artifact
	Status            ShadowStatus
	CreatedAt         time.Time
}

// ShadowDivergence records a single divergent output between STABLE and candidate.
// CandidateError is non-empty when the candidate returned an error instead of a result.
type ShadowDivergence struct {
	Timestamp        time.Time
	PolicyName       string
	StableVersion    string
	CandidateVersion string
	StableOutput     any
	CandidateOutput  any
	CandidateError   string
}

// ErrShadowAlreadyActive is returned when a shadow is already active for the policy.
type ErrShadowAlreadyActive struct{ Name string }

func (e ErrShadowAlreadyActive) Error() string {
	return fmt.Sprintf("shadow already active for policy %q", e.Name)
}

// ErrShadowNotFound is returned when no active shadow exists for the policy.
type ErrShadowNotFound struct{ Name string }

func (e ErrShadowNotFound) Error() string {
	return fmt.Sprintf("no active shadow for policy %q", e.Name)
}

// StartShadow activates shadow mode for the named policy, comparing STABLE against
// the supplied candidate artifact labelled by candidateVersion.
// Returns ErrShadowAlreadyActive if a shadow is already running.
func (s *PolicyStore) StartShadow(name, candidateVersion string, art *compiler.Artifact) (*ShadowRecord, error) {
	key := strings.ToLower(name)
	s.shadowMu.Lock()
	defer s.shadowMu.Unlock()

	if existing, ok := s.shadows[key]; ok && existing.Status == ShadowStatusActive {
		return nil, ErrShadowAlreadyActive{Name: name}
	}

	rec := &ShadowRecord{
		PolicyName:        name,
		CandidateVersion:  candidateVersion,
		CandidateArtifact: art,
		Status:            ShadowStatusActive,
		CreatedAt:         time.Now(),
	}
	s.shadows[key] = rec
	return rec, nil
}

// StopShadow deactivates the shadow for the named policy.
// Returns ErrShadowNotFound if no active shadow exists.
func (s *PolicyStore) StopShadow(name string) (*ShadowRecord, error) {
	key := strings.ToLower(name)
	s.shadowMu.Lock()
	defer s.shadowMu.Unlock()

	rec, ok := s.shadows[key]
	if !ok || rec.Status != ShadowStatusActive {
		return nil, ErrShadowNotFound{Name: name}
	}
	rec.Status = ShadowStatusStopped
	return rec, nil
}

// GetActiveShadow returns the active shadow for the named policy, or false if none.
func (s *PolicyStore) GetActiveShadow(name string) (*ShadowRecord, bool) {
	key := strings.ToLower(name)
	s.shadowMu.RLock()
	defer s.shadowMu.RUnlock()

	rec, ok := s.shadows[key]
	if !ok || rec.Status != ShadowStatusActive {
		return nil, false
	}
	return rec, true
}

// LogDivergence appends a divergence record to the shadow log for the named policy.
func (s *PolicyStore) LogDivergence(d *ShadowDivergence) {
	key := strings.ToLower(d.PolicyName)
	s.shadowMu.Lock()
	s.divergenceLog[key] = append(s.divergenceLog[key], d)
	s.shadowMu.Unlock()
}

// GetDivergences returns all logged divergences for the named policy.
func (s *PolicyStore) GetDivergences(name string) []*ShadowDivergence {
	key := strings.ToLower(name)
	s.shadowMu.RLock()
	log := s.divergenceLog[key]
	out := make([]*ShadowDivergence, len(log))
	copy(out, log)
	s.shadowMu.RUnlock()
	return out
}
