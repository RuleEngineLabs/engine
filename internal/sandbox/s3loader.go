package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// S3Client is the minimal interface required to fetch mapping files from S3.
// Fulfilled by aws-sdk-go-v2 s3.Client in production and a fake in tests.
type S3Client interface {
	GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, error)
}

// S3Loader fetches WireMock mappings from an S3 object and caches them in memory.
// The cache is refreshed when it becomes older than TTL (default 5 seconds).
type S3Loader struct {
	client S3Client
	bucket string
	key    string // path to the JSON array of Mapping objects
	ttl    time.Duration

	mu       sync.RWMutex
	cached   []*Mapping
	loadedAt time.Time
}

// NewS3Loader creates an S3Loader.
// bucket and key identify the S3 object that contains a JSON array of Mapping.
// ttl controls how long the in-memory cache is valid (recommended: 5–10s).
func NewS3Loader(client S3Client, bucket, key string, ttl time.Duration) *S3Loader {
	if ttl <= 0 {
		ttl = 5 * time.Second
	}
	return &S3Loader{
		client: client,
		bucket: bucket,
		key:    key,
		ttl:    ttl,
	}
}

func (l *S3Loader) Resolve(ctx context.Context, method, path string, body map[string]any) (*Response, error) {
	mappings, err := l.load(ctx)
	if err != nil {
		return nil, ErrSandboxUnavailable
	}
	for _, m := range mappings {
		if m.Matches(method, path, body) {
			return &Response{Status: m.Status, Headers: m.Headers, Body: m.Body}, nil
		}
	}
	return nil, ErrMappingNotFound
}

func (l *S3Loader) load(ctx context.Context) ([]*Mapping, error) {
	l.mu.RLock()
	if l.cached != nil && time.Since(l.loadedAt) < l.ttl {
		c := l.cached
		l.mu.RUnlock()
		return c, nil
	}
	l.mu.RUnlock()

	l.mu.Lock()
	defer l.mu.Unlock()
	// Double-check after acquiring write lock.
	if l.cached != nil && time.Since(l.loadedAt) < l.ttl {
		return l.cached, nil
	}

	rc, err := l.client.GetObject(ctx, l.bucket, l.key)
	if err != nil {
		return nil, fmt.Errorf("sandbox: S3 GetObject %s/%s: %w", l.bucket, l.key, err)
	}
	defer rc.Close()

	var mappings []*Mapping
	if err := json.NewDecoder(rc).Decode(&mappings); err != nil {
		return nil, fmt.Errorf("sandbox: decode mappings from S3: %w", err)
	}
	l.cached = mappings
	l.loadedAt = time.Now()
	return mappings, nil
}
