package store

import (
	"fmt"
	"strings"
	"time"
)

// CanaryStatus is the lifecycle state of a canary deployment.
type CanaryStatus string

const (
	CanaryStatusActive    CanaryStatus = "ACTIVE"
	CanaryStatusCompleted CanaryStatus = "COMPLETED"
	CanaryStatusCancelled CanaryStatus = "CANCELLED"
)

// CanaryRecord holds the state of a canary deployment for a single policy.
type CanaryRecord struct {
	PolicyName       string
	CandidateVersion string
	Percent          int
	TTLSeconds       int
	Status           CanaryStatus
	CreatedAt        time.Time
	ExpiresAt        time.Time
	// GsiActiveStatus mirrors Status while ACTIVE; empty when COMPLETED or CANCELLED.
	// Represents the sparse GSI attribute that enables efficient active-canary queries.
	GsiActiveStatus string
	CancelReason    string
}

// ErrCanaryInvalidPercent is returned when percent is outside 1–99.
type ErrCanaryInvalidPercent struct{ Percent int }

func (e ErrCanaryInvalidPercent) Error() string {
	return fmt.Sprintf("percent %d is out of range: must be 1–99", e.Percent)
}

// ErrCanaryAlreadyActive is returned when a canary is already active for the policy.
type ErrCanaryAlreadyActive struct{ Name string }

func (e ErrCanaryAlreadyActive) Error() string {
	return fmt.Sprintf("canary already active for policy %q", e.Name)
}

// ErrCanaryNotFound is returned when no active canary exists for the policy.
type ErrCanaryNotFound struct{ Name string }

func (e ErrCanaryNotFound) Error() string {
	return fmt.Sprintf("no active canary for policy %q", e.Name)
}

// StartCanary initializes a new canary deployment for the named policy.
// Returns ErrCanaryInvalidPercent for percent outside 1–99.
// Returns ErrCanaryAlreadyActive if a canary is already active.
func (s *PolicyStore) StartCanary(name, candidateVersion string, percent, ttlSeconds int) (*CanaryRecord, error) {
	if percent < 1 || percent > 99 {
		return nil, ErrCanaryInvalidPercent{Percent: percent}
	}

	key := strings.ToLower(name)
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.canaries[key]; ok && existing.Status == CanaryStatusActive {
		if existing.ExpiresAt.After(now) {
			return nil, ErrCanaryAlreadyActive{Name: name}
		}
		// Auto-expire
		existing.Status = CanaryStatusCompleted
		existing.GsiActiveStatus = ""
	}

	rec := &CanaryRecord{
		PolicyName:       name,
		CandidateVersion: candidateVersion,
		Percent:          percent,
		TTLSeconds:       ttlSeconds,
		Status:           CanaryStatusActive,
		CreatedAt:        now,
		ExpiresAt:        now.Add(time.Duration(ttlSeconds) * time.Second),
		GsiActiveStatus:  string(CanaryStatusActive),
	}
	s.canaries[key] = rec
	return rec, nil
}

// GetCanary returns the canary record for the named policy (may be COMPLETED or CANCELLED).
// It auto-expires ACTIVE canaries whose TTL has passed.
func (s *PolicyStore) GetCanary(name string) (*CanaryRecord, bool) {
	key := strings.ToLower(name)
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.canaries[key]
	if !ok {
		return nil, false
	}
	if rec.Status == CanaryStatusActive && !rec.ExpiresAt.After(time.Now()) {
		rec.Status = CanaryStatusCompleted
		rec.GsiActiveStatus = ""
	}
	return rec, true
}

// GetActiveCanary returns the active canary for the named policy, or false if none.
func (s *PolicyStore) GetActiveCanary(name string) (*CanaryRecord, bool) {
	rec, ok := s.GetCanary(name)
	if !ok || rec.Status != CanaryStatusActive {
		return nil, false
	}
	return rec, true
}

// ErrCanaryExtendReduce is returned when extend is called with a lower percent than current.
type ErrCanaryExtendReduce struct{ Current, Requested int }

func (e ErrCanaryExtendReduce) Error() string {
	return "extend only increases traffic — use cancel to rollback"
}

// ExtendCanary increases the traffic percentage of an active canary.
// Returns ErrCanaryNotFound if no active canary exists.
// Returns ErrCanaryExtendReduce if percent is not greater than the current value.
func (s *PolicyStore) ExtendCanary(name string, percent int) (*CanaryRecord, error) {
	key := strings.ToLower(name)
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.canaries[key]
	if !ok || rec.Status != CanaryStatusActive || !rec.ExpiresAt.After(time.Now()) {
		if ok && rec.Status == CanaryStatusActive && !rec.ExpiresAt.After(time.Now()) {
			rec.Status = CanaryStatusCompleted
			rec.GsiActiveStatus = ""
		}
		return nil, ErrCanaryNotFound{Name: name}
	}

	if percent <= rec.Percent {
		return nil, ErrCanaryExtendReduce{Current: rec.Percent, Requested: percent}
	}

	rec.Percent = percent
	return rec, nil
}

// CancelCanary cancels an active canary deployment, reverting 100% traffic to STABLE.
// Returns ErrCanaryNotFound if no active canary exists.
func (s *PolicyStore) CancelCanary(name, reason string) (*CanaryRecord, error) {
	key := strings.ToLower(name)
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.canaries[key]
	if !ok || rec.Status != CanaryStatusActive || !rec.ExpiresAt.After(time.Now()) {
		if ok && rec.Status == CanaryStatusActive && !rec.ExpiresAt.After(time.Now()) {
			rec.Status = CanaryStatusCompleted
			rec.GsiActiveStatus = ""
		}
		return nil, ErrCanaryNotFound{Name: name}
	}

	rec.Status = CanaryStatusCancelled
	rec.GsiActiveStatus = ""
	rec.CancelReason = reason
	return rec, nil
}
