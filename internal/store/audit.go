package store

import "time"

// RemovalAuditEntry records a single version-removal event.
type RemovalAuditEntry struct {
	PolicyName  string
	Version     string
	Reason      string
	RequestedBy string
	Timestamp   time.Time
}

// GetAuditLog returns a copy of all removal audit entries.
func (s *PolicyStore) GetAuditLog() []*RemovalAuditEntry {
	s.mu.RLock()
	out := make([]*RemovalAuditEntry, len(s.auditLog))
	copy(out, s.auditLog)
	s.mu.RUnlock()
	return out
}
