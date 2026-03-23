// Package builtin provides the structured request/response logging plugin.
package builtin

import (
	"net/http"
	"time"

	"github.com/bharanidharansrinivasan/api-gateway/pkg/gwctx"
	"go.uber.org/zap"
)

// LoggerPlugin records a structured access log entry at request start (INFO) and
// request completion (INFO with timing). It logs from GatewayContext so that
// retry_count, circuit_state, failure_reason, and trace_id are always present.
type LoggerPlugin struct {
	log *zap.Logger
}

// NewLoggerPlugin creates a LoggerPlugin. config map is not used; the plugin
// receives the shared logger from the gateway, not from per-route config.
func NewLoggerPlugin(log *zap.Logger) func(map[string]interface{}) (interface{ Name() string }, error) {
	return func(_ map[string]interface{}) (interface{ Name() string }, error) {
		return &LoggerPlugin{log: log}, nil
	}
}

func (p *LoggerPlugin) Name() string { return "request-logger" }

// ExecuteRequest logs the inbound request at debug level and stamps the start time
// onto GatewayContext (it is set by entry middleware, so this is a no-op if already set).
func (p *LoggerPlugin) ExecuteRequest(_ http.ResponseWriter, r *http.Request) error {
	gwCtx := gwctx.From(r.Context())
	if gwCtx == nil {
		return nil
	}
	p.log.Debug("request received",
		zap.String("request_id", gwCtx.RequestID),
		zap.String("trace_id", gwCtx.TraceID),
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("client_ip", gwCtx.ClientIP.String()),
	)
	return nil
}

// ExecuteResponse logs the completed request with all GatewayContext fields.
// This runs after the upstream response is received (or after a failure).
func (p *LoggerPlugin) ExecuteResponse(_ http.ResponseWriter, r *http.Request) error {
	gwCtx := gwctx.From(r.Context())
	if gwCtx == nil {
		return nil
	}
	latency := time.Since(gwCtx.StartTime)
	p.log.Info("request complete",
		zap.String("request_id", gwCtx.RequestID),
		zap.String("trace_id", gwCtx.TraceID),
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("route_id", gwCtx.RouteID),
		zap.String("client_ip", gwCtx.ClientIP.String()),
		zap.String("user_id", gwCtx.UserID),
		zap.String("upstream", gwCtx.SelectedInstance),
		zap.Int64("latency_ms", latency.Milliseconds()),
		zap.Int("retry_count", gwCtx.RetryCount),
		zap.String("circuit_state", string(gwCtx.CircuitState)),
		zap.String("failure_reason", gwCtx.FailureReason),
	)
	return nil
}
