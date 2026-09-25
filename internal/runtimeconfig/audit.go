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

type AuditSink interface {
	Record(ctx context.Context, entry AuditEntry) error
}

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

type AuditWriter struct {
	mu       sync.Mutex
	path     string
	prevHash string
}

func NewAuditWriter(path string) *AuditWriter {
	return &AuditWriter{path: path}
}

func (w *AuditWriter) Record(_ context.Context, entry AuditEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	entry.PrevHash = w.prevHash
	entry.Hash = ""

	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("runtimeconfig: audit marshal: %w", err)
	}
	sum := sha256.Sum256(raw)
	entry.Hash = hex.EncodeToString(sum[:])

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
