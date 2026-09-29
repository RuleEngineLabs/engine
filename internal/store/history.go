package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

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

// hashPolicy returns the SHA-256 hex digest of the policy's JSON representation.
func hashPolicy(p any) string {
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}
