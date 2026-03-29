package external_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bharanidharansrinivasan/api-gateway/internal/config"
	"github.com/bharanidharansrinivasan/api-gateway/internal/observability"
	"github.com/bharanidharansrinivasan/api-gateway/internal/plugin/external"
	v1 "github.com/bharanidharansrinivasan/api-gateway/sdk/plugin/v1"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

func newMetrics() *observability.Metrics {
	return observability.NewMetrics(prometheus.NewRegistry())
}

func TestLoadSucceeds(t *testing.T) {
	// Mock the external sidecar plugin
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest":
			json.NewEncoder(w).Encode(v1.Manifest{
				Name:            "test-plugin",
				Version:         "1.0.0",
				SDKMajorVersion: 1, // Must match gateway's expectation
			})
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	cfg := config.ExternalPluginConfig{
		Name:    "test-plugin",
		Address: ts.URL,
		Timeout: 50 * time.Millisecond,
		OnError: "pass",
	}

	p, err := external.Load(cfg, nil, newMetrics(), zap.NewNop())
	if err != nil {
		t.Fatalf("expected load to succeed, got %v", err)
	}
	if p.Name() != "test-plugin" {
		t.Fatalf("expected name test-plugin, got %v", p.Name())
	}
}

func TestSDKMajorVersionMismatch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest":
			json.NewEncoder(w).Encode(v1.Manifest{
				Name:            "future-plugin",
				SDKMajorVersion: 99, // Future version!
			})
		}
	}))
	defer ts.Close()

	cfg := config.ExternalPluginConfig{
		Name:    "future-plugin",
		Address: ts.URL,
	}

	_, err := external.Load(cfg, nil, newMetrics(), zap.NewNop())
	if err == nil {
		t.Fatal("expected version mismatch to trigger an error")
	}
}

func TestStartupHealthCheckFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest":
			json.NewEncoder(w).Encode(v1.Manifest{Name: "sick-plugin", SDKMajorVersion: 1})
		case "/healthz":
			w.WriteHeader(http.StatusServiceUnavailable) // Health check fails
		}
	}))
	defer ts.Close()

	cfg := config.ExternalPluginConfig{
		Name:    "sick-plugin",
		Address: ts.URL,
	}

	_, err := external.Load(cfg, nil, newMetrics(), zap.NewNop())
	if err == nil {
		t.Fatal("expected failed health check to abort load")
	}
}

func TestOnErrorPassAllows(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest":
			json.NewEncoder(w).Encode(v1.Manifest{Name: "err-plugin", SDKMajorVersion: 1})
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/execute/request":
			w.WriteHeader(http.StatusInternalServerError) // Simulate crash
		}
	}))
	defer ts.Close()

	cfg := config.ExternalPluginConfig{
		Name:    "err-plugin",
		Address: ts.URL,
		OnError: "pass", // Should ignore the InternalServerError and pass through
	}
	p, _ := external.Load(cfg, nil, newMetrics(), zap.NewNop())

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()

	err := p.ExecuteRequest(rr, req)
	if err != nil {
		t.Fatalf("expected pass policy to swallow error, but got %v", err)
	}
}

func TestOnErrorRejectAborts(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest":
			json.NewEncoder(w).Encode(v1.Manifest{Name: "err-plugin", SDKMajorVersion: 1})
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/execute/request":
			w.WriteHeader(http.StatusInternalServerError) // Crash
		}
	}))
	defer ts.Close()

	cfg := config.ExternalPluginConfig{
		Name:    "err-plugin",
		Address: ts.URL,
		OnError: "reject", // Should abort request
	}
	p, _ := external.Load(cfg, nil, newMetrics(), zap.NewNop())

	err := p.ExecuteRequest(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if err == nil {
		t.Fatal("expected reject policy to return an error")
	}
}

func TestAbortActionReturnsCorrectStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest":
			json.NewEncoder(w).Encode(v1.Manifest{Name: "auth-plugin", SDKMajorVersion: 1})
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/execute/request":
			// Sidecar intentionally aborts request (e.g. Auth failure)
			json.NewEncoder(w).Encode(v1.PluginResponse{
				Action:     "abort",
				StatusCode: 403,
				Message:    "auth-plugin aborted request",
			})
		}
	}))
	defer ts.Close()

	cfg := config.ExternalPluginConfig{Name: "auth-plugin", Address: ts.URL, OnError: "reject"}
	p, _ := external.Load(cfg, nil, newMetrics(), zap.NewNop())

	err := p.ExecuteRequest(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if err == nil {
		t.Fatal("expected AbortError, got nil")
	}
	if err.Error() != "plugin abort: HTTP 403: auth-plugin aborted request" {
		t.Fatalf("unexpected error message: %v", err)
	}
}
