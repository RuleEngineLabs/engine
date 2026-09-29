// Package shadowstub provides a self-contained HTTP stub for shadow-mode testing.
// It is intentionally decoupled from the benchmark provider fake and sandbox connections
// so changes to either do not affect shadow behaviour.
package shadowstub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
)

// Route is a fixed response configured for a specific method + path pattern.
type Route struct {
	Method string // HTTP method, e.g. "GET"; empty matches any method
	Path   string // exact path, e.g. "/orders/123"
	Status int    // HTTP status to return; defaults to 200
	Body   any    // JSON-serialisable response body
}

// Stub is a self-contained HTTP stub server for shadow candidate execution.
// It serves configured routes with fixed responses and returns 404 for everything else.
// Stub has no dependency on benchmark providers or sandbox connection packages.
type Stub struct {
	routes []*Route
	server *httptest.Server
}

// New creates and starts a Stub with the given routes.
// Call Close() when done to release resources.
func New(routes ...*Route) *Stub {
	s := &Stub{routes: routes}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)
	s.server = httptest.NewServer(mux)
	return s
}

// URL returns the base URL of the stub server (e.g. "http://127.0.0.1:PORT").
func (s *Stub) URL() string {
	return s.server.URL
}

// Close shuts down the stub server.
func (s *Stub) Close() {
	s.server.Close()
}

// Client returns an *http.Client pre-configured to talk to this stub.
func (s *Stub) Client() *http.Client {
	return s.server.Client()
}

func (s *Stub) handle(w http.ResponseWriter, r *http.Request) {
	for _, route := range s.routes {
		if !matchPath(route.Path, r.URL.Path) {
			continue
		}
		if route.Method != "" && !strings.EqualFold(route.Method, r.Method) {
			continue
		}
		status := route.Status
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if route.Body != nil {
			json.NewEncoder(w).Encode(route.Body)
		}
		return
	}
	http.Error(w, `{"error":"stub: route not configured"}`, http.StatusNotFound)
}

// matchPath returns true when path matches the route pattern.
// Only exact matches are supported; pattern must equal path exactly.
func matchPath(pattern, path string) bool {
	return pattern == path
}
