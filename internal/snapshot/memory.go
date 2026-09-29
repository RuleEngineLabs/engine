package snapshot

import (
	"context"
	"sync"

	"github.com/RuleEngineLabs/engine/internal/store"
)

// MemoryStore is an in-memory implementation of Store used in tests.
type MemoryStore struct {
	mu      sync.RWMutex
	records map[string]*store.PolicyRecord // key: policy name
	calls   int                            // number of LoadAll calls
}

// NewMemoryStore creates an empty MemoryStore.
func NewMemoryStore(records ...*store.PolicyRecord) *MemoryStore {
	m := &MemoryStore{records: make(map[string]*store.PolicyRecord)}
	for _, r := range records {
		m.records[r.Name] = r
	}
	return m
}

// LoadAll returns a snapshot of all stored records.
func (m *MemoryStore) LoadAll(_ context.Context) ([]*store.PolicyRecord, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	m.mu.RLock()
	out := make([]*store.PolicyRecord, 0, len(m.records))
	for _, r := range m.records {
		out = append(out, r)
	}
	m.mu.RUnlock()
	return out, nil
}

// Save persists (or replaces) the record for name.
func (m *MemoryStore) Save(_ context.Context, name string, rec *store.PolicyRecord) error {
	m.mu.Lock()
	m.records[name] = rec
	m.mu.Unlock()
	return nil
}

// Calls returns how many times LoadAll was invoked.
func (m *MemoryStore) Calls() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.calls
}

// Len returns the number of stored records.
func (m *MemoryStore) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.records)
}

// UnavailableLoader always returns ErrLoaderUnavailable — simulates DynamoDB down.
type UnavailableLoader struct{}

func (UnavailableLoader) LoadAll(_ context.Context) ([]*store.PolicyRecord, error) {
	return nil, ErrLoaderUnavailable
}
