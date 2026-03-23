// Package registry provides active health checking for upstream service instances.
package registry

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// HealthChecker runs background HTTP health checks for all registered services.
type HealthChecker struct {
	registry *Registry
	client   *http.Client
	log      *zap.Logger
}

// NewHealthChecker creates and immediately starts health checks for all services.
func NewHealthChecker(reg *Registry, log *zap.Logger) *HealthChecker {
	return &HealthChecker{
		registry: reg,
		client:   &http.Client{},
		log:      log,
	}
}

// StartAll launches one background goroutine per service.
// ctx cancellation stops all goroutines cleanly.
func (h *HealthChecker) StartAll(ctx context.Context) {
	h.registry.mu.RLock()
	services := make([]*ServiceEntry, 0, len(h.registry.services))
	for _, svc := range h.registry.services {
		services = append(services, svc)
	}
	h.registry.mu.RUnlock()

	for _, svc := range services {
		go h.runForService(ctx, svc)
	}
}

// runForService is the long-running goroutine that health-checks one service.
func (h *HealthChecker) runForService(ctx context.Context, svc *ServiceEntry) {
	cfg := svc.HealthCheck
	if cfg.Interval == 0 {
		cfg.Interval = 10 * time.Second
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 3 * time.Second
	}
	if cfg.FailureThreshold == 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.SuccessThreshold == 0 {
		cfg.SuccessThreshold = 2
	}

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			svc.mu.RLock()
			instances := make([]*Instance, len(svc.Instances))
			copy(instances, svc.Instances)
			svc.mu.RUnlock()

			for _, inst := range instances {
				go h.checkInstance(inst, svc.Name, cfg)
			}
		}
	}
}

// checkInstance performs a single HTTP GET health check against one instance.
func (h *HealthChecker) checkInstance(inst *Instance, svcName string, cfg HealthCheckCfg) {
	healthPath := cfg.Path
	if healthPath == "" {
		healthPath = "/health"
	}

	url := fmt.Sprintf("http://%s%s", inst.Address, healthPath)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := h.client.Do(req)

	if err != nil || resp.StatusCode >= 300 {
		if resp != nil {
			resp.Body.Close()
		}
		if inst.RecordFailure(cfg.FailureThreshold) {
			h.log.Warn("instance marked unhealthy",
				zap.String("service", svcName),
				zap.String("instance", inst.ID),
				zap.String("address", inst.Address),
			)
		}
		return
	}
	resp.Body.Close()

	if inst.RecordSuccess(cfg.SuccessThreshold) {
		h.log.Info("instance recovered to healthy",
			zap.String("service", svcName),
			zap.String("instance", inst.ID),
			zap.String("address", inst.Address),
		)
	}
}
