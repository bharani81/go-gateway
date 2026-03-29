package runtime

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/bharanidharansrinivasan/api-gateway/internal/config"
	"github.com/bharanidharansrinivasan/api-gateway/internal/loadbalancer"
	"github.com/bharanidharansrinivasan/api-gateway/internal/observability"
	"github.com/bharanidharansrinivasan/api-gateway/internal/plugin"
	"github.com/bharanidharansrinivasan/api-gateway/internal/registry"
	"go.uber.org/zap"
)

// Reloader owns the hot reload loop. It subscribes to config change events,
// diffs the new config against the old one, rebuilds only what changed, and
// retires the old GatewayRuntime after a drain window.
type Reloader struct {
	loader       *config.Loader
	holder       *RuntimeHolder
	reg          *registry.Registry
	hChecker     *registry.HealthChecker
	lbs          map[string]loadbalancer.LoadBalancer
	pluginReg    *plugin.Registry
	metrics      *observability.Metrics
	log          *zap.Logger
	versionCount atomic.Uint64
}

// NewReloader creates a Reloader. Call Start() to begin watching for config changes.
func NewReloader(
	loader *config.Loader,
	holder *RuntimeHolder,
	reg *registry.Registry,
	hChecker *registry.HealthChecker,
	lbs map[string]loadbalancer.LoadBalancer,
	pluginReg *plugin.Registry,
	metrics *observability.Metrics,
	log *zap.Logger,
) *Reloader {
	return &Reloader{
		loader:    loader,
		holder:    holder,
		reg:       reg,
		hChecker:  hChecker,
		lbs:       lbs,
		pluginReg: pluginReg,
		metrics:   metrics,
		log:       log,
	}
}

// Start subscribes to config reload events and processes them asynchronously.
// Returns immediately; the goroutine runs until ctx is cancelled.
func (rl *Reloader) Start(ctx context.Context) {
	ch := make(chan *config.Config, 1)
	rl.loader.Subscribe(ch)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case newCfg, ok := <-ch:
				if !ok {
					return
				}
				rl.applyReload(ctx, newCfg)
			}
		}
	}()
}

// applyReload rebuilds the runtime from newCfg and atomically swaps it in.
func (rl *Reloader) applyReload(ctx context.Context, newCfg *config.Config) {
	ver := rl.versionCount.Add(1)
	rl.log.Info("applying hot reload", zap.Uint64("version", ver))

	// 1. Diff services: update the long-lived registry without resetting health state.
	rl.applyServiceDiff(ctx, newCfg)

	// 2. Rebuild route-derived subsystems (router + plugin chains only).
	newRT, err := Build(newCfg, rl.pluginReg, ver, rl.log)
	if err != nil {
		rl.log.Error("runtime rebuild failed — keeping current runtime",
			zap.Uint64("version", ver),
			zap.Error(err),
		)
		rl.metrics.ConfigReloadFailures.Inc()
		return
	}

	// 3. Atomic swap.
	oldRT := rl.holder.Swap(newRT)
	rl.metrics.RuntimeVersion.Set(float64(ver))
	rl.metrics.ConfigReloads.WithLabelValues("success").Inc()
	rl.log.Info("hot reload successful", zap.Uint64("version", ver))

	// 4. Retire the old runtime after a drain window.
	// In-flight requests that captured oldRT have at most DrainWindow to finish.
	go func() {
		time.Sleep(config.DrainWindow)
		if oldRT != nil {
			oldRT.Close()
			rl.log.Debug("retired old runtime", zap.Uint64("old_version", oldRT.Version))
		}
	}()
}

// applyServiceDiff updates the service registry and health checker to reflect
// newCfg without rebuilding from scratch. Health state of unchanged instances
// is preserved.
func (rl *Reloader) applyServiceDiff(ctx context.Context, newCfg *config.Config) {
	// Build set of service names in new config.
	newNames := make(map[string]struct{}, len(newCfg.Services))
	for _, svcCfg := range newCfg.Services {
		newNames[svcCfg.Name] = struct{}{}

		// Register/update service in registry.
		// registry.Register is idempotent (replaces if already exists).
		instances := make([]*registry.Instance, 0, len(svcCfg.Instances))
		for _, inst := range svcCfg.Instances {
			instances = append(instances, &registry.Instance{
				ID:      inst.ID,
				Address: inst.Address,
				Weight:  inst.Weight,
			})
		}
		entry := &registry.ServiceEntry{
			Name:       svcCfg.Name,
			LBStrategy: svcCfg.LBStrategy,
			Instances:  instances,
			HealthCheck: registry.HealthCheckCfg{
				Path:             svcCfg.HealthCheck.Path,
				Interval:         svcCfg.HealthCheck.Interval,
				Timeout:          svcCfg.HealthCheck.Timeout,
				FailureThreshold: svcCfg.HealthCheck.FailureThreshold,
				SuccessThreshold: svcCfg.HealthCheck.SuccessThreshold,
			},
		}
		rl.reg.Register(entry)
		// Start health checker for this service (idempotent if already running).
		rl.hChecker.StartForService(ctx, entry)

		// Update load balancer strategy if changed.
		switch svcCfg.LBStrategy {
		case "random":
			rl.lbs[svcCfg.Name] = loadbalancer.NewRandom()
		default:
			if _, exists := rl.lbs[svcCfg.Name]; !exists {
				rl.lbs[svcCfg.Name] = loadbalancer.NewRoundRobin()
			}
		}
	}

	// Stop health checkers for services that were removed in new config.
	for name := range rl.reg.ServiceNames() {
		if _, stillExists := newNames[name]; !stillExists {
			rl.hChecker.StopForService(name)
			rl.log.Info("removed service from registry on reload", zap.String("service", name))
		}
	}
}
