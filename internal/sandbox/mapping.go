// Package sandbox provides WireMock-style sandbox mappings for preview write simulation.
// Mappings are loaded from S3 (or an in-memory store in tests) and matched by method,
// path, and optional body-field regex matchers.
package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrMappingNotFound is returned when no mapping matches the request.
var ErrMappingNotFound = errors.New("sandbox_mapping_not_found")

// ErrSandboxUnavailable is returned when the mapping source (e.g. S3) is unreachable.
var ErrSandboxUnavailable = errors.New("sandbox_unavailable")

// Mapping defines a WireMock-style response for a method+path combination.
type Mapping struct {
	Method   string            `json:"method"`             // HTTP method (POST, PUT, PATCH, DELETE)
	Path     string            `json:"path"`               // exact URL path
	Matchers map[string]string `json:"matchers,omitempty"` // body field → regex pattern
	Status   int               `json:"status"`             // response status code
	Body     json.RawMessage   `json:"body,omitempty"`     // JSON response body
	Headers  map[string]string `json:"headers,omitempty"`  // response headers
}

// Matches returns true if the mapping applies to the given method, path, and body fields.
func (m *Mapping) Matches(method, path string, body map[string]any) bool {
	if !strings.EqualFold(m.Method, method) {
		return false
	}
	if m.Path != path {
		return false
	}
	for field, pattern := range m.Matchers {
		val, ok := body[field]
		if !ok {
			return false
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return false
		}
		if !re.MatchString(fmt.Sprintf("%v", val)) {
			return false
		}
	}
	return true
}

// Response is the simulated result returned by a sandbox mapping.
type Response struct {
	Status  int
	Headers map[string]string
	Body    json.RawMessage
}

// Loader resolves sandbox mappings for write operations in preview mode.
type Loader interface {
	// Resolve finds the first mapping matching method+path+body and returns its response.
	// Returns ErrMappingNotFound when no mapping matches.
	// Returns ErrSandboxUnavailable when the backing store is unreachable.
	Resolve(ctx context.Context, method, path string, body map[string]any) (*Response, error)
}
