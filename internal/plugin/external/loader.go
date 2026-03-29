package external

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bharanidharansrinivasan/api-gateway/internal/config"
	"github.com/bharanidharansrinivasan/api-gateway/internal/observability"
	"github.com/bharanidharansrinivasan/api-gateway/pkg/circuitbreaker"
	v1 "github.com/bharanidharansrinivasan/api-gateway/sdk/plugin/v1"
	"go.uber.org/zap"
)

// Load validates and wires up an ExternalPlugin from config.
//
// Steps:
//  1. Fetch GET /manifest and validate SDK major version compatibility.
//  2. Perform a startup health check (GET /healthz must return 200).
//  3. Return a ready-to-use ExternalPlugin with its own circuit breaker.
//
// Returns an error if any step fails — the gateway should refuse to start.
func Load(
	cfg config.ExternalPluginConfig,
	pluginCfg map[string]any,
	metrics *observability.Metrics,
	log *zap.Logger,
) (*ExternalPlugin, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 50 * time.Millisecond
	}

	onError := cfg.OnError
	if onError == "" {
		onError = "pass"
	}

	healthPath := cfg.HealthPath
	if healthPath == "" {
		healthPath = "/healthz"
	}

	// Normalize address — unix socket URLs need special handling in HTTP calls.
	httpAddr := cfg.Address
	if strings.HasPrefix(cfg.Address, "unix://") {
		// For HTTP calls over unix socket, use a dummy host.
		httpAddr = "http://plugin"
	}

	client := buildClient(cfg.Address, timeout)

	// Step 1: fetch and validate manifest.
	manifest, err := fetchManifest(client, httpAddr)
	if err != nil {
		return nil, fmt.Errorf("external plugin %q: manifest fetch failed: %w", cfg.Name, err)
	}

	if manifest.SDKMajorVersion != v1.SDKMajorVersion {
		return nil, fmt.Errorf(
			"external plugin %q: SDK major version mismatch — plugin=%d, gateway=%d (incompatible)",
			manifest.Name, manifest.SDKMajorVersion, v1.SDKMajorVersion,
		)
	}

	// Step 2: startup health check.
	if err := checkHealth(client, httpAddr, healthPath); err != nil {
		return nil, fmt.Errorf("external plugin %q: startup health check failed: %w", manifest.Name, err)
	}

	log.Info("external plugin loaded",
		zap.String("name", manifest.Name),
		zap.String("version", manifest.Version),
		zap.Int("sdk_major", manifest.SDKMajorVersion),
		zap.String("address", cfg.Address),
		zap.String("on_error", onError),
	)

	if metrics != nil {
		metrics.ExternalPluginHealth.WithLabelValues(manifest.Name).Set(1)
	}

	cb := circuitbreaker.New(circuitbreaker.Config{
		FailureThreshold: 3,
		SuccessThreshold: 2,
		ResetTimeout:     30 * time.Second,
	}, nil)

	return &ExternalPlugin{
		manifest: manifest,
		addr:     httpAddr,
		cfg:      pluginCfg,
		timeout:  timeout,
		onError:  onError,
		client:   client,
		cb:       cb,
		metrics:  metrics,
		log:      log,
	}, nil
}

// fetchManifest calls GET /manifest on the plugin sidecar.
func fetchManifest(client *http.Client, addr string) (v1.Manifest, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+"/manifest", nil)
	if err != nil {
		return v1.Manifest{}, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return v1.Manifest{}, fmt.Errorf("GET /manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return v1.Manifest{}, fmt.Errorf("GET /manifest returned HTTP %d", resp.StatusCode)
	}

	var manifest v1.Manifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return v1.Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	return manifest, nil
}

// checkHealth calls GET /healthz on the plugin sidecar.
func checkHealth(client *http.Client, addr, path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+path, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned HTTP %d (expected 200)", path, resp.StatusCode)
	}
	return nil
}
