// Package registry provides active health checking for upstream service instances.
package registry

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

// HealthChecker runs background HTTP health checks for all registered services.
// It supports per-service start/stop so the service registry can evolve on hot reload
// without leaking goroutines for removed services.
type HealthChecker struct {
	registry *Registry
	client   *http.Client
	log      *zap.Logger
	mu       sync.Mutex
	cancels  map[string]context.CancelFunc // serviceName → cancel
}

// NewHealthChecker creates a HealthChecker. Call StartAll or StartForService to begin checks.
func NewHealthChecker(reg *Registry, log *zap.Logger) *HealthChecker {
	return &HealthChecker{
		registry: reg,
		client:   &http.Client{},
		log:      log,
		cancels:  make(map[string]context.CancelFunc),
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
		h.StartForService(ctx, svc)
	}
}

// StartForService starts health checking for a single service.
// Idempotent — if already running for this service, does nothing.
func (h *HealthChecker) StartForService(parentCtx context.Context, svc *ServiceEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, already := h.cancels[svc.Name]; already {
		return // already running
	}

	svcCtx, cancel := context.WithCancel(parentCtx)
	h.cancels[svc.Name] = cancel
	go h.runForService(svcCtx, svc)
}

// StopForService cancels the health check goroutine for a removed service.
// Safe to call if the service was never started.
func (h *HealthChecker) StopForService(serviceName string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if cancel, ok := h.cancels[serviceName]; ok {
		cancel()
		delete(h.cancels, serviceName)
		h.log.Info("health checker stopped for removed service", zap.String("service", serviceName))
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
