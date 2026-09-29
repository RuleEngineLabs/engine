// Package benchprovider supplies a self-contained HTTP fake for benchmark execution.
// It is intentionally decoupled from internal/shadowstub so benchmark and shadow
// concerns can evolve independently.
package benchprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
)

// Route is a fixed response served by the fake for a specific method + path.
type Route struct {
	Method  string // HTTP method; empty matches any
	Path    string // exact URL path
	Status  int    // HTTP status; defaults to 200
	Body    any    // JSON-serialisable response body
	Latency int    // simulated latency in milliseconds (unused in unit tests; for bench use)
}

// Provider is a self-contained HTTP fake for benchmark execution.
// It serves fixed routes and returns 404 for everything else.
type Provider struct {
	routes []*Route
	server *httptest.Server
}

// New creates and starts a Provider with the given routes.
// Call Close() when the benchmark is done.
func New(routes ...*Route) *Provider {
	p := &Provider{routes: routes}
	mux := http.NewServeMux()
	mux.HandleFunc("/", p.handle)
	p.server = httptest.NewServer(mux)
	return p
}

// URL returns the base URL of the fake server.
func (p *Provider) URL() string { return p.server.URL }

// Close shuts down the fake server.
func (p *Provider) Close() { p.server.Close() }

// Client returns an *http.Client pre-configured to talk to this provider.
func (p *Provider) Client() *http.Client { return p.server.Client() }

func (p *Provider) handle(w http.ResponseWriter, r *http.Request) {
	for _, route := range p.routes {
		if route.Path != r.URL.Path {
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
	http.Error(w, `{"error":"benchprovider: route not configured"}`, http.StatusNotFound)
}
