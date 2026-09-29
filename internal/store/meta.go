package store

import (
	"fmt"
	"strings"
)

// PolicyMeta holds policy-level configuration that is not part of the state machine.
type PolicyMeta struct {
	CoexistenceWindowSeconds int
}

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
