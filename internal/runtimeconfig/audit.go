package runtimeconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// AuditSink accepts audit entries. Implementations must be safe for concurrent use.
type AuditSink interface {
	Record(ctx context.Context, entry AuditEntry) error
}

// AuditEntry is one JSON line in the append-only audit log.
// Hash covers all fields of this entry except Hash itself; PrevHash chains to the previous entry.
type AuditEntry struct {
	EventID     string         `json:"eventId"`
	Seq         uint64         `json:"seq,omitempty"`
	Action      string         `json:"action"`
	Scope       string         `json:"scope"`
	Before      *RuntimeConfig `json:"before,omitempty"`
	After       *RuntimeConfig `json:"after,omitempty"`
	RequestedBy string         `json:"requestedBy"`
	Auth        string         `json:"auth,omitempty"`
	ApprovedBy  string         `json:"approvedBy,omitempty"`
	Reason      string         `json:"reason,omitempty"`
	SourceIP    string         `json:"sourceIP,omitempty"`
	RequestedAt time.Time      `json:"requestedAt"`
	AppliedAt   *time.Time     `json:"appliedAt,omitempty"`
	ExpiresAt   *time.Time     `json:"expiresAt,omitempty"`
	RevertedAt  *time.Time     `json:"revertedAt,omitempty"`
	InstanceID  string         `json:"instanceId"`
	Result      string         `json:"result"`
	PrevHash    string         `json:"prevHash"`
	Hash        string         `json:"hash,omitempty"`
}

// AuditWriter appends JSON-newline entries to a file, chaining each entry's SHA-256
// into the next entry's PrevHash field. Writes are serialised by an internal mutex;
// the file is opened per write so crashes leave no partial lines.
type AuditWriter struct {
	mu       sync.Mutex
	path     string
	prevHash string
}

// NewAuditWriter creates an AuditWriter that appends to path (created if absent, mode 0600).
func NewAuditWriter(path string) *AuditWriter {
	return &AuditWriter{path: path}
}

// Record implements AuditSink. It appends entry as a single JSON line and updates
// the internal hash chain. The method is safe for concurrent use.
func (w *AuditWriter) Record(_ context.Context, entry AuditEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	entry.PrevHash = w.prevHash
	entry.Hash = ""

	// First marshal: compute hash over the entry without its own hash value.
	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("runtimeconfig: audit marshal: %w", err)
	}
	sum := sha256.Sum256(raw)
	entry.Hash = hex.EncodeToString(sum[:])

	// Second marshal: write the complete entry including its hash.
	raw, err = json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("runtimeconfig: audit marshal (with hash): %w", err)
	}
	raw = append(raw, '\n')

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("runtimeconfig: audit open %q: %w", w.path, err)
	}
	if _, writeErr := f.Write(raw); writeErr != nil {
		_ = f.Close()
		return fmt.Errorf("runtimeconfig: audit write: %w", writeErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		return fmt.Errorf("runtimeconfig: audit close: %w", closeErr)
	}

	w.prevHash = entry.Hash
	return nil
}
