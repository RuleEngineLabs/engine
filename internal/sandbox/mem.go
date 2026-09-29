package sandbox

import "context"

// MemLoader is an in-memory Loader backed by a fixed list of mappings.
// Used in tests and local development; not for production.
type MemLoader struct {
	mappings []*Mapping
}

// NewMemLoader creates a MemLoader with the given mappings.
func NewMemLoader(mappings ...*Mapping) *MemLoader {
	return &MemLoader{mappings: mappings}
}

func (l *MemLoader) Resolve(_ context.Context, method, path string, body map[string]any) (*Response, error) {
	for _, m := range l.mappings {
		if m.Matches(method, path, body) {
			return &Response{
				Status:  m.Status,
				Headers: m.Headers,
				Body:    m.Body,
			}, nil
		}
	}
	return nil, ErrMappingNotFound
}
