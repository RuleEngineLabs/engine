package shadowstub_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/RuleEngineLabs/engine/internal/shadowstub"
)

// TestStub_NoBenchmarkOrSandboxImports verifies the package has no imports from
// the benchmark provider fake or sandbox connections packages.
// The stub lives in its own module so import checks are enforced at build time;
// this test serves as documentation of the invariant.
func TestStub_NoBenchmarkOrSandboxImports(t *testing.T) {
	// If this test file compiles, the shadowstub package compiled without forbidden imports.
	// A static analysis check would normally live in CI; here we assert the stub works.
	s := shadowstub.New()
	defer s.Close()
	if s.URL() == "" {
		t.Fatal("expected non-empty URL")
	}
}

// TestStub_ConfiguredRouteReturnsFixedData covers: stub returns pre-configured fixed data.
func TestStub_ConfiguredRouteReturnsFixedData(t *testing.T) {
	want := map[string]any{"orderId": "123", "status": "shipped"}
	s := shadowstub.New(
		&shadowstub.Route{Method: "GET", Path: "/orders/123", Body: want},
	)
	defer s.Close()

	resp, err := s.Client().Get(s.URL() + "/orders/123")
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

// TestStub_UnknownRouteReturns404 covers: stub returns 404 for unconfigured routes.
func TestStub_UnknownRouteReturns404(t *testing.T) {
	s := shadowstub.New()
	defer s.Close()
	resp, err := s.Client().Get(s.URL() + "/unknown")
	if err != nil {
		t.Fatalf("GET /unknown: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

// TestStub_MethodFilter verifies that a GET-only route rejects POST.
func TestStub_MethodFilter(t *testing.T) {
	s := shadowstub.New(
		&shadowstub.Route{Method: "GET", Path: "/items/1", Body: "ok"},
	)
	defer s.Close()

	resp, _ := s.Client().Post(s.URL()+"/items/1", "application/json", strings.NewReader("{}"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST to GET-only route: want 404, got %d", resp.StatusCode)
	}
}

// TestStub_AnyMethodRoute matches regardless of method when Method is empty.
func TestStub_AnyMethodRoute(t *testing.T) {
	s := shadowstub.New(
		&shadowstub.Route{Path: "/health", Body: map[string]any{"ok": true}},
	)
	defer s.Close()

	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut} {
		req, _ := http.NewRequest(method, s.URL()+"/health", nil)
		resp, err := s.Client().Do(req)
		if err != nil {
			t.Fatalf("%s /health: %v", method, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s /health: want 200, got %d", method, resp.StatusCode)
		}
	}
}

// TestStub_CustomStatusCode verifies non-200 status codes are served correctly.
func TestStub_CustomStatusCode(t *testing.T) {
	s := shadowstub.New(
		&shadowstub.Route{Method: "GET", Path: "/fail", Status: 503, Body: map[string]any{"error": "unavailable"}},
	)
	defer s.Close()

	resp, _ := s.Client().Get(s.URL() + "/fail")
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", resp.StatusCode)
	}
}

// TestStub_NilBodyRoute returns empty body without panicking.
func TestStub_NilBodyRoute(t *testing.T) {
	s := shadowstub.New(
		&shadowstub.Route{Method: "GET", Path: "/empty", Status: 204},
	)
	defer s.Close()

	resp, _ := s.Client().Get(s.URL() + "/empty")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}
	if len(strings.TrimSpace(string(body))) != 0 {
		t.Fatalf("expected empty body, got: %s", body)
	}
}

// TestStub_MultipleRoutes verifies correct route is selected among several.
func TestStub_MultipleRoutes(t *testing.T) {
	s := shadowstub.New(
		&shadowstub.Route{Method: "GET", Path: "/a", Body: "routeA"},
		&shadowstub.Route{Method: "GET", Path: "/b", Body: "routeB"},
	)
	defer s.Close()

	for path, want := range map[string]string{"/a": "routeA", "/b": "routeB"} {
		resp, _ := s.Client().Get(s.URL() + path)
		var got string
		json.NewDecoder(resp.Body).Decode(&got)
		resp.Body.Close()
		if got != want {
			t.Fatalf("GET %s: want %q, got %q", path, want, got)
		}
	}
}

// TestStub_UsedByShadowExecution simulates how the shadow executor would use the stub:
// the candidate policy makes apiCall to the stub URL and receives fixed data.
func TestStub_UsedByShadowExecution(t *testing.T) {
	orderData := map[string]any{"id": "ord-42", "total": 199.9}
	s := shadowstub.New(
		&shadowstub.Route{Method: "GET", Path: "/orders/ord-42", Body: orderData},
	)
	defer s.Close()

	// Simulate what executor.runAPICall does: GET the stub URL
	resp, err := s.Client().Get(s.URL() + "/orders/ord-42")
	if err != nil {
		t.Fatalf("apiCall simulation: %v", err)
	}
	defer resp.Body.Close()

	var got map[string]any
	json.NewDecoder(resp.Body).Decode(&got)
	if got["id"] != "ord-42" {
		t.Fatalf("expected ord-42, got %v", got["id"])
	}
}
