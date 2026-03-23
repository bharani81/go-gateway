// Package loadbalancer defines the LoadBalancer interface and provides
// concrete implementations for round-robin and random selection strategies.
package loadbalancer

import (
	"errors"

	"github.com/bharanidharansrinivasan/api-gateway/internal/registry"
)

// ErrNoHealthyInstances is returned when there are no healthy instances
// available for a service, including when all per-instance circuit breakers are open.
var ErrNoHealthyInstances = errors.New("no healthy upstream instances available")

// LoadBalancer selects one healthy instance from a service's instance pool.
// Implementations must be safe for concurrent use.
type LoadBalancer interface {
	// Next returns the next instance to route the request to, skipping any
	// instance for which isSkipped returns true (e.g., open per-instance circuit).
	Next(instances []*registry.Instance, isSkipped func(*registry.Instance) bool) (*registry.Instance, error)
}
