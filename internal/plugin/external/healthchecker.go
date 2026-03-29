package external

import (
	"context"
	"time"

	"github.com/bharanidharansrinivasan/api-gateway/internal/observability"
	"go.uber.org/zap"
)

// PluginHealthChecker polls GET /healthz for every external plugin.
// On failure, it trips the plugin's circuit breaker so the gateway
// applies the configured on_error policy instead of waiting for a timeout.
type PluginHealthChecker struct {
	plugins []*ExternalPlugin
	metrics *observability.Metrics
	log     *zap.Logger
}

// NewPluginHealthChecker creates the checker for the given set of plugins.
func NewPluginHealthChecker(plugins []*ExternalPlugin, metrics *observability.Metrics, log *zap.Logger) *PluginHealthChecker {
	return &PluginHealthChecker{plugins: plugins, metrics: metrics, log: log}
}

// StartAll launches one background goroutine per external plugin.
// Goroutines stop when ctx is cancelled (gateway shutdown).
func (h *PluginHealthChecker) StartAll(ctx context.Context) {
	for _, p := range h.plugins {
		go h.runFor(ctx, p)
	}
}

// runFor is the per-plugin health check loop.
func (h *PluginHealthChecker) runFor(ctx context.Context, p *ExternalPlugin) {
	interval := 10 * time.Second // TODO: make configurable via ExternalPluginConfig.HealthInterval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			healthPath := "/healthz" // TODO: use p.cfg healthPath
			err := checkHealth(p.client, p.addr, healthPath)
			if err != nil {
				p.cb.RecordFailure()
				h.metrics.ExternalPluginHealth.WithLabelValues(p.manifest.Name).Set(0)
				h.log.Warn("external plugin health check failed",
					zap.String("plugin", p.manifest.Name),
					zap.Error(err),
				)
			} else {
				p.cb.RecordSuccess()
				h.metrics.ExternalPluginHealth.WithLabelValues(p.manifest.Name).Set(1)
			}
		}
	}
}
