// Package loadbalancer provides a random instance selection strategy.
package loadbalancer

import (
	"math/rand"
	"time"

	"github.com/bharanidharansrinivasan/api-gateway/internal/registry"
)

// Random selects a healthy instance uniformly at random on each call.
// Prefer over RoundRobin for services where sticky distribution is undesirable
// or when running behind a proxy that already distributes evenly.
type Random struct{}

// NewRandom creates a new Random load balancer.
func NewRandom() *Random { return &Random{} }

// Next picks a random eligible instance.
// If all instances are skipped, returns ErrNoHealthyInstances.
func (rb *Random) Next(
	instances []*registry.Instance,
	isSkipped func(*registry.Instance) bool,
) (*registry.Instance, func(time.Duration, bool), error) {
	eligible := filterEligible(instances, isSkipped)
	if len(eligible) == 0 {
		return nil, nil, ErrNoHealthyInstances
	}
	done := func(latency time.Duration, isErr bool) {}
	return eligible[rand.Intn(len(eligible))], done, nil
}
