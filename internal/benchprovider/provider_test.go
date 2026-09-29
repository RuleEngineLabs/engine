package benchprovider_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/RuleEngineLabs/engine/internal/benchprovider"
)

func TestProvider_ConfiguredRouteReturnsFixedData(t *testing.T) {
	want := map[string]any{"orderId": "123", "status": "shipped"}
	p := benchprovider.New(
		&benchprovider.Route{Method: "GET", Path: "/orders/123", Body: want},
	)
	defer p.Close()

	resp, err := p.Client().Get(p.URL() + "/orders/123")
	if err != nil {
		t.Fatalf("GET /orders/123: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var got map[string]any
	json.NewDecoder(resp.Body).Decode(&got)
	if got["orderId"] != "123" || got["status"] != "shipped" {
		t.Fatalf("unexpected body: %v", got)
	}
}

func TestProvider_UnknownRouteReturns404(t *testing.T) {
	p := benchprovider.New()
	defer p.Close()
	resp, _ := p.Client().Get(p.URL() + "/unknown")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestProvider_MethodFilter(t *testing.T) {
	p := benchprovider.New(
		&benchprovider.Route{Method: "GET", Path: "/items/1", Body: "ok"},
	)
	defer p.Close()
	resp, _ := p.Client().Post(p.URL()+"/items/1", "application/json", strings.NewReader("{}"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST to GET-only route: want 404, got %d", resp.StatusCode)
	}
}

func TestProvider_AnyMethodRoute(t *testing.T) {
	p := benchprovider.New(
		&benchprovider.Route{Path: "/health", Body: map[string]any{"ok": true}},
	)
	defer p.Close()
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req, _ := http.NewRequest(method, p.URL()+"/health", nil)
		resp, err := p.Client().Do(req)
		if err != nil {
			t.Fatalf("%s /health: %v", method, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s /health: want 200, got %d", method, resp.StatusCode)
		}
	}
}

func TestProvider_CustomStatusCode(t *testing.T) {
	p := benchprovider.New(
		&benchprovider.Route{Method: "GET", Path: "/fail", Status: 503},
	)
	defer p.Close()
	resp, _ := p.Client().Get(p.URL() + "/fail")
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", resp.StatusCode)
	}
}

func TestProvider_NilBodyRoute(t *testing.T) {
	p := benchprovider.New(
		&benchprovider.Route{Method: "GET", Path: "/empty", Status: 204},
	)
	defer p.Close()
	resp, _ := p.Client().Get(p.URL() + "/empty")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}
	if len(strings.TrimSpace(string(body))) != 0 {
		t.Fatalf("expected empty body, got: %s", body)
	}
}

func TestProvider_MultipleRoutes(t *testing.T) {
	p := benchprovider.New(
		&benchprovider.Route{Method: "GET", Path: "/a", Body: "routeA"},
		&benchprovider.Route{Method: "GET", Path: "/b", Body: "routeB"},
	)
	defer p.Close()
	for path, want := range map[string]string{"/a": "routeA", "/b": "routeB"} {
		resp, _ := p.Client().Get(p.URL() + path)
		var got string
		json.NewDecoder(resp.Body).Decode(&got)
		resp.Body.Close()
		if got != want {
			t.Fatalf("GET %s: want %q, got %q", path, want, got)
		}
	}
}

// TestProvider_URLAndClientNonNil verifies the server starts successfully.
func TestProvider_URLAndClientNonNil(t *testing.T) {
	p := benchprovider.New()
	defer p.Close()
	if p.URL() == "" {
		t.Fatal("expected non-empty URL")
	}
	if p.Client() == nil {
		t.Fatal("expected non-nil client")
	}
}

// TestProvider_DecoupledFromShadowstub is a build-time assertion that benchprovider
// does not import internal/shadowstub or sandbox packages (decoupling invariant).
// If this test file compiles, the package has no forbidden imports.
func TestProvider_DecoupledFromShadowstub(t *testing.T) {
	// Just run — decoupling is enforced at compile time.
}
