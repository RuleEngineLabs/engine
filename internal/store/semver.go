package store

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/policy"
)

// DraftStatus is the lifecycle status of a pending policy version.
type DraftStatus string

const (
	DraftStatusDraft    DraftStatus = "DRAFT"
	DraftStatusApproved DraftStatus = "APPROVED"
)

// DraftRecord holds a pending policy version awaiting promotion to STABLE.
type DraftRecord struct {
	PolicyName  string
	Status      DraftStatus
	BaseVersion string // current STABLE semver the draft was branched from, e.g. "1.2.0"
	Policy      *policy.Policy
	Artifact    *compiler.Artifact
}

// ErrDraftNotApproved is returned when a draft in non-APPROVED status is promoted.
type ErrDraftNotApproved struct{ Name string }

func (e ErrDraftNotApproved) Error() string {
	return "draft must be approved before promotion"
}

// UpsertDraft stores a new draft (or replaces an existing one) for the named policy,
// and appends a DRAFT entry to the version history.
func (s *PolicyStore) UpsertDraft(name, baseVersion string, p *policy.Policy, art *compiler.Artifact) {
	key := strings.ToLower(name)
	s.mu.Lock()
	s.drafts[key] = &DraftRecord{
		PolicyName:  name,
		Status:      DraftStatusDraft,
		BaseVersion: baseVersion,
		Policy:      p,
		Artifact:    art,
	}
	vNum := len(s.history[key]) + 1
	s.history[key] = append(s.history[key], &VersionRecord{
		PolicyName:  name,
		Version:     vNum,
		Status:      VersionStatusDraft,
		ContentHash: hashPolicy(p),
		CreatedAt:   time.Now(),
		Policy:      p,
	})
	s.mu.Unlock()
}

// GetDraft returns the current draft for the named policy, or false if none exists.
func (s *PolicyStore) GetDraft(name string) (*DraftRecord, bool) {
	key := strings.ToLower(name)
	s.mu.RLock()
	dr, ok := s.drafts[key]
	s.mu.RUnlock()
	return dr, ok
}

// ApproveDraft marks the draft for the named policy as APPROVED.
func (s *PolicyStore) ApproveDraft(name string) error {
	key := strings.ToLower(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	dr, ok := s.drafts[key]
	if !ok {
		return fmt.Errorf("no draft found for policy %q", name)
	}
	dr.Status = DraftStatusApproved
	return nil
}

// Promote promotes the APPROVED draft for the named policy to STABLE with the
// given SemVer bump (patch | minor | major), returning the new version string.
// Returns ErrDraftNotApproved if the draft is not in APPROVED status.
func (s *PolicyStore) Promote(name, bump string) (string, error) {
	key := strings.ToLower(name)
	s.mu.Lock()
	defer s.mu.Unlock()

	dr, ok := s.drafts[key]
	if !ok {
		return "", fmt.Errorf("no draft found for policy %q", name)
	}
	if dr.Status != DraftStatusApproved {
		return "", ErrDraftNotApproved{Name: name}
	}

	if bump == "major" {
		m := s.meta[key]
		if m == nil || m.CoexistenceWindowSeconds == 0 {
			return "", ErrCoexistenceWindowNotSet{Name: name}
		}
	}

	newVer, err := bumpSemVer(dr.BaseVersion, bump)
	if err != nil {
		return "", err
	}

	// Update the STABLE semver on the existing policy record if present.
	if rec, ok := s.byName[key]; ok {
		rec.StableVersion = newVer
	}

	// Update the most-recent DRAFT history entry to STABLE, or append a new one.
	updated := false
	for i := len(s.history[key]) - 1; i >= 0; i-- {
		if s.history[key][i].Status == VersionStatusDraft || s.history[key][i].Status == VersionStatusApproved {
			s.history[key][i].Status = VersionStatusStable
			s.history[key][i].SemVersion = newVer
			updated = true
			break
		}
	}
	if !updated {
		vNum := len(s.history[key]) + 1
		s.history[key] = append(s.history[key], &VersionRecord{
			PolicyName:  name,
			Version:     vNum,
			SemVersion:  newVer,
			Status:      VersionStatusStable,
			ContentHash: hashPolicy(dr.Policy),
			CreatedAt:   time.Now(),
			Policy:      dr.Policy,
		})
	}

	// Draft consumed; remove it.
	delete(s.drafts, key)
	return newVer, nil
}

func bumpSemVer(base, bump string) (string, error) {
	if base == "" {
		base = "0.0.0"
	}
	parts := strings.SplitN(base, ".", 3)
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid semver %q", base)
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	patch, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return "", fmt.Errorf("invalid semver %q", base)
	}

	switch bump {
	case "patch":
		patch++
	case "minor":
		minor++
		patch = 0
	case "major":
		major++
		minor = 0
		patch = 0
	default:
		return "", fmt.Errorf("invalid bump %q: must be patch, minor, or major", bump)
	}
	return fmt.Sprintf("%d.%d.%d", major, minor, patch), nil
}
