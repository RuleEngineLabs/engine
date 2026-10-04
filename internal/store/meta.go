package store

import (
	"fmt"
	"strings"
)

// HTTPConfig holds per-policy HTTP behaviour overrides applied by handleExecute.
// All fields are opt-in; zero value means "use default / disabled".
type HTTPConfig struct {
	// RetryOn lists HTTP status codes for which a Retry-After header is added.
	RetryOn []int `json:"retry_on,omitempty"`
	// RetryAfterSeconds is the value written to Retry-After when the status matches RetryOn.
	RetryAfterSeconds int `json:"retry_after_seconds,omitempty"`
	// TimeoutMs cuts the execution context after this many milliseconds (0 = no limit).
	TimeoutMs int `json:"timeout_ms,omitempty"`
	// CacheMaxAgeSeconds sets Cache-Control: max-age=N on successful GET responses (0 = off).
	CacheMaxAgeSeconds int `json:"cache_max_age_seconds,omitempty"`
}

// PolicyMeta holds policy-level configuration that is not part of the state machine.
type PolicyMeta struct {
	CoexistenceWindowSeconds int
	// Approvers lists the Cognito group identifiers allowed to approve drafts.
	// If empty, the policy owner (PolicyRecord.Owner) is the sole approver.
	Approvers []string
	// HTTP configures per-policy HTTP behaviour (retry, timeout, cache).
	HTTP HTTPConfig
}

// ErrApprovalForbidden is returned when the caller is not in the approvers list.
type ErrApprovalForbidden struct{ Name string }

func (e ErrApprovalForbidden) Error() string { return "forbidden: not in approvers list" }

// ErrCoexistenceWindowNotSet is returned when a MAJOR SemVer bump is attempted
// on a policy that has not declared a coexistence window.
type ErrCoexistenceWindowNotSet struct{ Name string }

func (e ErrCoexistenceWindowNotSet) Error() string { return "coexistence_window_not_set" }

// SetMeta persists meta for the named policy (creates if absent).
func (s *PolicyStore) SetMeta(name string, meta *PolicyMeta) error {
	key := strings.ToLower(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byName[key]; !ok {
		return fmt.Errorf("policy %q not found", name)
	}
	s.meta[key] = meta
	return nil
}

// GetMeta returns the meta for the named policy (nil if not set).
func (s *PolicyStore) GetMeta(name string) *PolicyMeta {
	s.mu.RLock()
	m := s.meta[strings.ToLower(name)]
	s.mu.RUnlock()
	return m
}

// ApproveDraftByVersion approves the current draft for the named policy if the
// caller (identified by callerGroups from a JWT) is authorized:
//   - callerGroups contains "policy-operators" (global operator) → always authorized
//   - meta.Approvers is non-empty → caller must be in the approvers list
//   - meta.Approvers is empty → caller must be the policy owner (PolicyRecord.Owner)
func (s *PolicyStore) ApproveDraftByVersion(name string, callerGroups []string) error {
	key := strings.ToLower(name)
	s.mu.Lock()
	defer s.mu.Unlock()

	dr, ok := s.drafts[key]
	if !ok {
		return fmt.Errorf("no draft found for policy %q", name)
	}

	// Global operator always authorized
	for _, g := range callerGroups {
		if g == "policy-operators" {
			dr.Status = DraftStatusApproved
			return nil
		}
	}

	m := s.meta[key]
	var allowedGroups []string
	if m != nil && len(m.Approvers) > 0 {
		allowedGroups = m.Approvers
	} else {
		// Fallback: policy owner
		if rec, ok := s.byName[key]; ok {
			allowedGroups = []string{rec.Owner}
		}
	}

	for _, g := range callerGroups {
		for _, allowed := range allowedGroups {
			if g == allowed {
				dr.Status = DraftStatusApproved
				return nil
			}
		}
	}
	return ErrApprovalForbidden{Name: name}
}
