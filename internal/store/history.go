package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ErrCannotDeleteStable is returned when attempting to delete a STABLE version.
type ErrCannotDeleteStable struct{ Name, Version string }

func (e ErrCannotDeleteStable) Error() string {
	return "cannot delete STABLE version with active consumers"
}

// VersionStatus represents the lifecycle state of a stored version.
type VersionStatus string

const (
	VersionStatusDraft    VersionStatus = "DRAFT"
	VersionStatusApproved VersionStatus = "APPROVED"
	VersionStatusStable   VersionStatus = "STABLE"
	VersionStatusRemoved  VersionStatus = "REMOVED"
)

// VersionRecord holds metadata for a single historical version of a policy.
type VersionRecord struct {
	PolicyName  string
	Version     int    // monotonic integer counter
	SemVersion  string // semver string, e.g. "1.2.1"; empty for drafts
	Status      VersionStatus
	ContentHash string
	Author      string
	CreatedAt   time.Time
	RemovalNote string // non-empty when Status == REMOVED
	Policy      any    // raw policy snapshot (for metadata display)
}

// AddHistory appends a new version record to the history for the named policy.
func (s *PolicyStore) AddHistory(name string, rec *VersionRecord) {
	key := strings.ToLower(name)
	s.mu.Lock()
	s.history[key] = append(s.history[key], rec)
	s.mu.Unlock()
}

// GetVersions returns the full version history (all statuses) for the named policy.
func (s *PolicyStore) GetVersions(name string) ([]*VersionRecord, bool) {
	key := strings.ToLower(name)
	s.mu.RLock()
	recs, ok := s.history[key]
	out := make([]*VersionRecord, len(recs))
	copy(out, recs)
	s.mu.RUnlock()
	return out, ok
}

// GetVersion returns a single version record by its semver string or integer string ("2").
// Semver strings (containing ".") are matched by SemVersion; integer strings match by Version counter.
func (s *PolicyStore) GetVersion(name, version string) (*VersionRecord, bool) {
	key := strings.ToLower(name)
	s.mu.RLock()
	recs := s.history[key]
	s.mu.RUnlock()

	isSemVer := strings.Contains(version, ".")
	for _, r := range recs {
		if isSemVer {
			if r.SemVersion == version {
				return r, true
			}
		} else {
			if fmt.Sprintf("%d", r.Version) == version {
				return r, true
			}
		}
	}
	return nil, false
}

// MarkVersionRemoved marks the specified version as REMOVED with the given reason.
// Returns ErrCannotDeleteStable if the version is STABLE.
// requestedBy is recorded in the audit log.
func (s *PolicyStore) MarkVersionRemoved(name, version, reason, requestedBy string) error {
	key := strings.ToLower(name)
	s.mu.Lock()
	defer s.mu.Unlock()

	recs := s.history[key]
	isSemVer := strings.Contains(version, ".")
	var found *VersionRecord
	for _, r := range recs {
		if isSemVer {
			if r.SemVersion == version {
				found = r
				break
			}
		} else {
			if fmt.Sprintf("%d", r.Version) == version {
				found = r
				break
			}
		}
	}
	if found == nil {
		return fmt.Errorf("version %q not found for policy %q", version, name)
	}
	if found.Status == VersionStatusStable {
		return ErrCannotDeleteStable{Name: name, Version: version}
	}
	found.Status = VersionStatusRemoved
	found.RemovalNote = reason
	s.auditLog = append(s.auditLog, &RemovalAuditEntry{
		PolicyName:  name,
		Version:     version,
		Reason:      reason,
		RequestedBy: requestedBy,
		Timestamp:   time.Now(),
	})
	return nil
}

// hashPolicy returns the SHA-256 hex digest of the policy's JSON representation.
func hashPolicy(p any) string {
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}
