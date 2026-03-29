package runtime_test

import (
	"net/http"
	"testing"

	"github.com/bharanidharansrinivasan/api-gateway/internal/config"
	"github.com/bharanidharansrinivasan/api-gateway/internal/plugin"
	"github.com/bharanidharansrinivasan/api-gateway/internal/runtime"
	"go.uber.org/zap"
)

// mockPlugin is a basic mock plugin that tracks invocations and returns predefined errors.
type mockPlugin struct{}

func (m *mockPlugin) Name() string { return "mock" }
func (m *mockPlugin) ExecuteRequest(w http.ResponseWriter, r *http.Request) error { return nil }
func (m *mockPlugin) ExecuteResponse(w http.ResponseWriter, r *http.Request) error { return nil }

func TestBuildCreatesChainsAndRouter(t *testing.T) {
	cfg := config.Config{
		Plugins: []config.PluginDef{
			{Name: "mock-plugin", Type: "builtin.mock"},
		},
		Routes: []config.RouteConfig{
			{
				ID:        "test-route",
				Path:      "/api",
				MatchType: "prefix",
				Plugins: []config.PluginRef{
					{Name: "mock-plugin", Order: 1},
				},
			},
		},
	}

	reg := plugin.NewRegistry()
	reg.Register("builtin.mock", func(_ map[string]interface{}) (plugin.Plugin, error) {
		return &mockPlugin{}, nil
	})

	rt, err := runtime.Build(&cfg, reg, 42, zap.NewNop())
	if err != nil {
		t.Fatalf("unexpected error building runtime: %v", err)
	}

	if rt.Version != 42 {
		t.Fatalf("expected version 42, got %d", rt.Version)
	}
	if rt.Router == nil {
		t.Fatal("expected non-nil router")
	}
	chains := rt.PluginChains
	if _, ok := chains["test-route"]; !ok {
		t.Fatal("expected chain to be built for test-route")
	}
}

func TestBuildFailsOnUnregisteredPlugin(t *testing.T) {
	cfg := config.Config{
		Plugins: []config.PluginDef{
			// The plugin is referenced here, but never registered in the mock registry below.
			{Name: "unknown-plugin", Type: "builtin.unknown"},
		},
		Routes: []config.RouteConfig{
			{
				ID:        "test-route",
				Path:      "/api",
				Plugins: []config.PluginRef{
					{Name: "unknown-plugin", Order: 1},
				},
			},
		},
	}

	reg := plugin.NewRegistry() // Empty registry
	_, err := runtime.Build(&cfg, reg, 1, zap.NewNop())
	if err == nil {
		t.Fatal("expected Build() to fail due to unknown plugin")
	}
}

func TestBuildFailsOnBadRegex(t *testing.T) {
	cfg := config.Config{
		Routes: []config.RouteConfig{
			{
				ID:        "bad-regex",
				Path:      "[invalid-regex",
				MatchType: "regex",
			},
		},
	}

	reg := plugin.NewRegistry()
	_, err := runtime.Build(&cfg, reg, 1, zap.NewNop())
	if err == nil {
		t.Fatal("expected Build() to fail due to invalid regex in router compilation")
	}
}
