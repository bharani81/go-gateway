// Package external provides the gateway-side client for HTTP sidecar plugins.
// External plugins are separate HTTP servers that implement the sdk/plugin/v1 contract.
package external

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	pluginpkg "github.com/bharanidharansrinivasan/api-gateway/internal/plugin"
	"github.com/bharanidharansrinivasan/api-gateway/internal/observability"
	"github.com/bharanidharansrinivasan/api-gateway/pkg/circuitbreaker"
	"github.com/bharanidharansrinivasan/api-gateway/pkg/gwctx"
	v1 "github.com/bharanidharansrinivasan/api-gateway/sdk/plugin/v1"
	"go.uber.org/zap"
)

// PluginInfo holds the publicly exposed state of an ExternalPlugin.
type PluginInfo struct {
	Name         string
	Version      string
	SDKMajor     int
	Address      string
	OnError      string
	TimeoutMs    int64
	CircuitState string
}

// ExternalPlugin is a gateway Plugin that delegates to an HTTP sidecar process.
// It supports TCP and Unix domain socket addresses, configurable timeouts,
// per-plugin circuit breakers, and on_error failure policies.
type ExternalPlugin struct {
	manifest  v1.Manifest
	addr      string         // base address, e.g. "http://localhost:8082"
	cfg       map[string]any // route-level plugin config, forwarded in payload
	timeout   time.Duration
	onError   string         // "pass" | "reject" | "circuit-break"
	client    *http.Client   // dedicated, short-timeout, reused
	cb        *circuitbreaker.CircuitBreaker
	metrics   *observability.Metrics
	log       *zap.Logger
}

// Name implements plugin.Plugin.
func (p *ExternalPlugin) Name() string { return p.manifest.Name }

// Info returns the publicly observable state of this plugin.
func (p *ExternalPlugin) Info() PluginInfo {
	return PluginInfo{
		Name:         p.manifest.Name,
		Version:      p.manifest.Version,
		SDKMajor:     p.manifest.SDKMajorVersion,
		Address:      p.addr,
		OnError:      p.onError,
		TimeoutMs:    p.timeout.Milliseconds(),
		CircuitState: p.cb.State().String(),
	}
}

// ExecuteRequest implements plugin.Plugin.
func (p *ExternalPlugin) ExecuteRequest(w http.ResponseWriter, r *http.Request) error {
	return p.execute(w, r, "/execute/request")
}

// ExecuteResponse implements plugin.Plugin.
func (p *ExternalPlugin) ExecuteResponse(w http.ResponseWriter, r *http.Request) error {
	return p.execute(w, r, "/execute/response")
}

func (p *ExternalPlugin) execute(w http.ResponseWriter, r *http.Request, path string) error {
	// Fast path: circuit breaker open.
	if !p.cb.Allow() {
		p.metrics.ExternalPluginErrors.WithLabelValues(p.manifest.Name, "circuit_open").Inc()
		return p.applyOnError(fmt.Errorf("plugin %q circuit breaker open", p.manifest.Name))
	}

	payload := p.buildPayload(r)
	body, err := json.Marshal(payload)
	if err != nil {
		return p.applyOnError(fmt.Errorf("plugin payload marshal: %w", err))
	}

	callCtx, cancel := context.WithTimeout(r.Context(), p.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, p.addr+path, bytes.NewReader(body))
	if err != nil {
		return p.applyOnError(err)
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := p.client.Do(req)
	elapsed := time.Since(start).Seconds()
	p.metrics.ExternalPluginLatency.WithLabelValues(p.manifest.Name).Observe(elapsed)

	if err != nil {
		p.cb.RecordFailure()
		p.metrics.ExternalPluginErrors.WithLabelValues(p.manifest.Name, "call_error").Inc()
		return p.applyOnError(fmt.Errorf("plugin %q call failed: %w", p.manifest.Name, err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		p.cb.RecordFailure()
		p.metrics.ExternalPluginErrors.WithLabelValues(p.manifest.Name, "non_200").Inc()
		return p.applyOnError(fmt.Errorf("plugin %q returned HTTP %d", p.manifest.Name, resp.StatusCode))
	}

	p.cb.RecordSuccess()

	var pluginResp v1.PluginResponse
	if err := json.NewDecoder(resp.Body).Decode(&pluginResp); err != nil {
		return p.applyOnError(fmt.Errorf("plugin %q response decode: %w", p.manifest.Name, err))
	}

	// Inject any headers the plugin wants added to the upstream request.
	for k, v := range pluginResp.AddHeaders {
		r.Header.Set(k, v)
	}

	if pluginResp.Action == "abort" {
		statusCode := pluginResp.StatusCode
		if statusCode == 0 {
			statusCode = http.StatusForbidden
		}
		return &pluginpkg.AbortError{
			StatusCode: statusCode,
			Message:    pluginResp.Message,
		}
	}

	return nil
}

// applyOnError implements the on_error policy.
func (p *ExternalPlugin) applyOnError(err error) error {
	p.log.Warn("external plugin error",
		zap.String("plugin", p.manifest.Name),
		zap.String("policy", p.onError),
		zap.Error(err),
	)
	switch p.onError {
	case "pass":
		return nil // allow the request through
	case "reject", "circuit-break":
		return &pluginpkg.AbortError{
			StatusCode: http.StatusBadGateway,
			Message:    fmt.Sprintf("plugin %q unavailable", p.manifest.Name),
		}
	default:
		return nil
	}
}

// buildPayload constructs the RequestPayload from the current request context.
func (p *ExternalPlugin) buildPayload(r *http.Request) v1.RequestPayload {
	payload := v1.RequestPayload{
		Method:     r.Method,
		Path:       r.URL.Path,
		Query:      r.URL.RawQuery,
		Headers:    make(map[string]string, len(r.Header)),
		RemoteAddr: r.RemoteAddr,
		Config:     p.cfg,
	}
	if gwCtx := gwctx.From(r.Context()); gwCtx != nil {
		payload.RouteID = gwCtx.RouteID
		payload.UserID = gwCtx.UserID
	}
	for k, vals := range r.Header {
		if len(vals) > 0 {
			payload.Headers[k] = vals[0]
		}
	}
	return payload
}

// buildClient creates an http.Client for the given address.
// Supports "http://host:port" and "unix:///path/to/socket" addresses.
func buildClient(addr string, timeout time.Duration) *http.Client {
	transport := &http.Transport{}

	if strings.HasPrefix(addr, "unix://") {
		socketPath := strings.TrimPrefix(addr, "unix://")
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		}
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout + 5*time.Millisecond, // client timeout slightly over context deadline
	}
}
