// Package runtime holds the live, hot-swappable gateway subsystems.
//
// Only route-derived state lives here (Router + PluginChains).
// The service registry and health checker are long-lived singletons
// that receive config diffs on reload — never rebuilt from scratch.
package runtime

import (
	"sync/atomic"

	"github.com/bharanidharansrinivasan/api-gateway/internal/plugin"
	"github.com/bharanidharansrinivasan/api-gateway/internal/router"
)

// GatewayRuntime holds all subsystems derived from the route configuration.
// A new GatewayRuntime is built on every successful hot reload and atomically
// swapped into the RuntimeHolder. In-flight requests hold a direct pointer to
// the old runtime; the old runtime is closed after a drain window.
type GatewayRuntime struct {
	// Version is a monotonically increasing counter incremented on each reload.
	// Useful for correlating logs and metrics to a specific config generation.
	Version uint64

	// Router matches incoming requests to routes.
	Router *router.Router

	// PluginChains maps route ID → ordered plugin chain for that route.
	PluginChains map[string]*plugin.Chain
}

// Close tears down all plugin chains in this runtime.
// Safe to call concurrently with in-flight requests — those requests hold their
// own direct pointer to the runtime and are not affected by this Close call.
//
// Callers should wait for an in-flight drain window before calling Close.
func (rt *GatewayRuntime) Close() {
	for _, chain := range rt.PluginChains {
		chain.Close()
	}
}

// RuntimeHolder is a thread-safe atomic pointer to the current GatewayRuntime.
// It uses sync/atomic.Pointer[T] (Go 1.19+) for type-safe, race-detector-friendly access.
type RuntimeHolder struct {
	current atomic.Pointer[GatewayRuntime]
}

// NewRuntimeHolder creates a RuntimeHolder seeded with an initial runtime.
func NewRuntimeHolder(initial *GatewayRuntime) *RuntimeHolder {
	h := &RuntimeHolder{}
	h.current.Store(initial)
	return h
}

// Get returns the current GatewayRuntime. Safe for concurrent use.
// The returned pointer is valid for the lifetime of the caller's request.
func (h *RuntimeHolder) Get() *GatewayRuntime {
	return h.current.Load()
}

// Swap atomically installs a new GatewayRuntime and returns the old one.
// The caller is responsible for closing the old runtime after a drain window.
func (h *RuntimeHolder) Swap(rt *GatewayRuntime) *GatewayRuntime {
	return h.current.Swap(rt)
}
