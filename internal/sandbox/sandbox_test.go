package sandbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/RuleEngineLabs/engine/internal/sandbox"
)

// --- MemLoader tests ---

func TestMemLoader_MappingFound(t *testing.T) {
	body := json.RawMessage(`{"orderId":"123"}`)
	l := sandbox.NewMemLoader(
		&sandbox.Mapping{
			Method: "POST",
			Path:   "/orders",
			Status: 201,
			Body:   body,
			Headers: map[string]string{"Location": "/orders/123"},
		},
	)

	resp, err := l.Resolve(context.Background(), "POST", "/orders", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != 201 {
		t.Fatalf("want 201, got %d", resp.Status)
	}
	if resp.Headers["Location"] != "/orders/123" {
		t.Fatalf("want Location /orders/123, got %q", resp.Headers["Location"])
	}
}

func TestMemLoader_MappingNotFound(t *testing.T) {
	l := sandbox.NewMemLoader()
	_, err := l.Resolve(context.Background(), "DELETE", "/orders/123", nil)
	if !errors.Is(err, sandbox.ErrMappingNotFound) {
		t.Fatalf("want ErrMappingNotFound, got %v", err)
	}
}

func TestMemLoader_MethodFilter(t *testing.T) {
	l := sandbox.NewMemLoader(
		&sandbox.Mapping{Method: "POST", Path: "/orders", Status: 201},
	)
	_, err := l.Resolve(context.Background(), "PUT", "/orders", nil)
	if !errors.Is(err, sandbox.ErrMappingNotFound) {
		t.Fatalf("want ErrMappingNotFound for wrong method, got %v", err)
	}
}

func TestMemLoader_RegexBodyMatcher(t *testing.T) {
	l := sandbox.NewMemLoader(
		&sandbox.Mapping{
			Method:   "POST",
			Path:     "/orders",
			Matchers: map[string]string{"orderId": `^TEST-.*`},
			Status:   200,
			Body:     json.RawMessage(`{"matched":true}`),
		},
	)

	// Matching orderId
	resp, err := l.Resolve(context.Background(), "POST", "/orders", map[string]any{"orderId": "TEST-ABC123"})
	if err != nil {
		t.Fatalf("expected match, got: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("want 200, got %d", resp.Status)
	}

	// Non-matching orderId
	_, err = l.Resolve(context.Background(), "POST", "/orders", map[string]any{"orderId": "PROD-XYZ"})
	if !errors.Is(err, sandbox.ErrMappingNotFound) {
		t.Fatalf("want ErrMappingNotFound for non-matching regex, got %v", err)
	}
}

func TestMemLoader_MissingBodyField(t *testing.T) {
	l := sandbox.NewMemLoader(
		&sandbox.Mapping{
			Method:   "POST",
			Path:     "/orders",
			Matchers: map[string]string{"orderId": `^TEST-.*`},
			Status:   200,
		},
	)
	// Body doesn't contain orderId
	_, err := l.Resolve(context.Background(), "POST", "/orders", map[string]any{"other": "val"})
	if !errors.Is(err, sandbox.ErrMappingNotFound) {
		t.Fatalf("want ErrMappingNotFound when field missing, got %v", err)
	}
}

func TestMemLoader_MultipleMatchesFirstWins(t *testing.T) {
	l := sandbox.NewMemLoader(
		&sandbox.Mapping{Method: "POST", Path: "/orders", Status: 200, Body: json.RawMessage(`"first"`)},
		&sandbox.Mapping{Method: "POST", Path: "/orders", Status: 201, Body: json.RawMessage(`"second"`)},
	)
	resp, err := l.Resolve(context.Background(), "POST", "/orders", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("want first mapping (200), got %d", resp.Status)
	}
}

// --- S3Loader tests ---

type fakeS3Client struct {
	content string
	err     error
}

func (f *fakeS3Client) GetObject(_ context.Context, _, _ string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(strings.NewReader(f.content)), nil
}

func TestS3Loader_ResolvesFromS3(t *testing.T) {
	mappings := `[{"method":"POST","path":"/items","status":201,"body":{"id":"abc"}}]`
	client := &fakeS3Client{content: mappings}
	l := sandbox.NewS3Loader(client, "my-bucket", "sandbox/mappings.json", 5*time.Second)

	resp, err := l.Resolve(context.Background(), "POST", "/items", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != 201 {
		t.Fatalf("want 201, got %d", resp.Status)
	}
}

func TestS3Loader_NotFound(t *testing.T) {
	client := &fakeS3Client{content: `[]`}
	l := sandbox.NewS3Loader(client, "my-bucket", "sandbox/mappings.json", 5*time.Second)

	_, err := l.Resolve(context.Background(), "DELETE", "/orders/1", nil)
	if !errors.Is(err, sandbox.ErrMappingNotFound) {
		t.Fatalf("want ErrMappingNotFound, got %v", err)
	}
}

func TestS3Loader_S3Unavailable(t *testing.T) {
	client := &fakeS3Client{err: errors.New("connection refused")}
	l := sandbox.NewS3Loader(client, "my-bucket", "sandbox/mappings.json", 5*time.Second)

	_, err := l.Resolve(context.Background(), "POST", "/orders", nil)
	if !errors.Is(err, sandbox.ErrSandboxUnavailable) {
		t.Fatalf("want ErrSandboxUnavailable, got %v", err)
	}
}

func TestS3Loader_CachePreventsSecondFetch(t *testing.T) {
	calls := 0
	mappings := `[{"method":"POST","path":"/x","status":200}]`
	client := &countingS3Client{content: mappings, calls: &calls}
	l := sandbox.NewS3Loader(client, "b", "k", 10*time.Second)

	for i := 0; i < 3; i++ {
		l.Resolve(context.Background(), "POST", "/x", nil) //nolint:errcheck
	}
	if calls != 1 {
		t.Fatalf("expected 1 S3 fetch (cached), got %d", calls)
	}
}

type countingS3Client struct {
	content string
	calls   *int
}

func (c *countingS3Client) GetObject(_ context.Context, _, _ string) (io.ReadCloser, error) {
	*c.calls++
	return io.NopCloser(strings.NewReader(c.content)), nil
}

func TestS3Loader_CacheExpires(t *testing.T) {
	calls := 0
	mappings := `[{"method":"POST","path":"/x","status":200}]`
	client := &countingS3Client{content: mappings, calls: &calls}
	l := sandbox.NewS3Loader(client, "b", "k", 1*time.Millisecond) // very short TTL

	l.Resolve(context.Background(), "POST", "/x", nil) //nolint:errcheck
	time.Sleep(5 * time.Millisecond)
	l.Resolve(context.Background(), "POST", "/x", nil) //nolint:errcheck

	if calls < 2 {
		t.Fatalf("expected at least 2 S3 fetches after TTL expiry, got %d", calls)
	}
}
