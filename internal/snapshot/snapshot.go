package snapshot

import (
	"context"
	"errors"

	"github.com/RuleEngineLabs/engine/internal/store"
)

// Loader can load all persisted policy records (DynamoDB, S3, etc.).
type Loader interface {
	LoadAll(ctx context.Context) ([]*store.PolicyRecord, error)
}

// Writer persists a single policy record snapshot (typically S3).
type Writer interface {
	Save(ctx context.Context, name string, rec *store.PolicyRecord) error
}

// Store combines Loader and Writer for a full snapshot backend.
type Store interface {
	Loader
	Writer
}

// Boot tries to load all records from primary. If primary fails, it falls back
// to fallback. Returns an empty slice (not an error) if both return no records.
func Boot(ctx context.Context, primary, fallback Loader) ([]*store.PolicyRecord, error) {
	records, err := primary.LoadAll(ctx)
	if err == nil {
		return records, nil
	}
	return fallback.LoadAll(ctx)
}

// ErrLoaderUnavailable is returned when a loader's underlying storage is unreachable.
var ErrLoaderUnavailable = errors.New("loader unavailable")
