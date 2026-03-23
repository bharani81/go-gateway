// Package loadbalancer provides a round-robin load balancing strategy.
package loadbalancer

import (
	"sync/atomic"

	"github.com/bharanidharansrinivasan/api-gateway/internal/registry"
)

// RoundRobin selects instances in a circular fashion using an atomic counter.
// Each RoundRobin instance should be created per service to keep counters
// isolated and prevent one service's traffic from affecting another's.
type RoundRobin struct {
	counter uint64
}

// NewRoundRobin creates a new RoundRobin load balancer.
func NewRoundRobin() *RoundRobin {
	return &RoundRobin{}
}

// Next picks the next eligible instance using round-robin.
// Instances for which isSkipped returns true (e.g., open circuit) are excluded.
// If all instances are skipped, returns ErrNoHealthyInstances.
func (rr *RoundRobin) Next(
	instances []*registry.Instance,
	isSkipped func(*registry.Instance) bool,
) (*registry.Instance, error) {
	eligible := filterEligible(instances, isSkipped)
	if len(eligible) == 0 {
		return nil, ErrNoHealthyInstances
	}

	// Atomically increment and use modulo to select. This is safe for concurrent
	// requests: each goroutine gets a unique counter value.
	n := atomic.AddUint64(&rr.counter, 1)
	selected := eligible[(n-1)%uint64(len(eligible))]
	return selected, nil
}

// filterEligible returns the subset of instances that are healthy and not skipped.
func filterEligible(instances []*registry.Instance, isSkipped func(*registry.Instance) bool) []*registry.Instance {
	eligible := make([]*registry.Instance, 0, len(instances))
	for _, inst := range instances {
		if inst.IsHealthy() && (isSkipped == nil || !isSkipped(inst)) {
			eligible = append(eligible, inst)
		}
	}
	return eligible
}
