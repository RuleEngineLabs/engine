package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// S3Writer is the minimal write interface for publishing sandbox mappings to S3.
// Fulfilled by aws-sdk-go-v2 s3.Client in production and a MemWriter in tests.
type S3Writer interface {
	PutObject(ctx context.Context, bucket, key string, data []byte) error
}

// S3Publisher writes a single Mapping to the S3 sandbox bucket under a deterministic key.
// Key format: sandbox/overrides/{policyName}/{service}.json
// The S3 lifecycle rule (24h on prefix sandbox/overrides/) handles expiry — no engine code needed.
type S3Publisher struct {
	client S3Writer
	bucket string
}

// NewS3Publisher creates an S3Publisher.
func NewS3Publisher(client S3Writer, bucket string) *S3Publisher {
	return &S3Publisher{client: client, bucket: bucket}
}

// Publish serialises mapping and writes it to the deterministic S3 key.
func (p *S3Publisher) Publish(ctx context.Context, policyName, service string, mapping *Mapping) error {
	data, err := json.Marshal(mapping)
	if err != nil {
		return fmt.Errorf("sandbox: marshal mapping: %w", err)
	}
	key := overrideKey(policyName, service)
	if err := p.client.PutObject(ctx, p.bucket, key, bytes.TrimSpace(data)); err != nil {
		return fmt.Errorf("sandbox: S3 PutObject %s/%s: %w", p.bucket, key, err)
	}
	return nil
}

// OverrideKey returns the canonical S3 key for a sandbox mapping override.
func overrideKey(policyName, service string) string {
	return fmt.Sprintf("sandbox/overrides/%s/%s.json", policyName, service)
}
