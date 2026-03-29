// Package registry manages the set of upstream service instances and their health state.
package registry

import (
	"sync"
	"time"
)

// HealthStatus represents the observed health of a single instance.
type HealthStatus int32

const (
	StatusHealthy   HealthStatus = iota
	StatusUnhealthy HealthStatus = iota
	StatusStarting  HealthStatus = iota
)

// Instance is a single upstream host:port pair with tracking state.
type Instance struct {
	ID      string
	Address string // "host:port"
	Weight  int

	mu               sync.Mutex
	status           HealthStatus
	consecutiveFails int
	consecutiveOKs   int
	lastChecked      time.Time
}

// IsHealthy returns true when the instance is in a healthy or starting state.
func (i *Instance) IsHealthy() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.status != StatusUnhealthy
}

// RecordSuccess registers a successful health check or proxy call.
// Returns true if the instance transitioned from Unhealthy → Healthy.
func (i *Instance) RecordSuccess(successThreshold int) (transitioned bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.consecutiveFails = 0
	i.consecutiveOKs++
	if i.status == StatusUnhealthy && i.consecutiveOKs >= successThreshold {
		i.status = StatusHealthy
		i.consecutiveOKs = 0
		return true
	}
	return false
}

// RecordFailure registers a failed health check or proxy call.
// Returns true if the instance transitioned from Healthy → Unhealthy.
func (i *Instance) RecordFailure(failThreshold int) (transitioned bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.consecutiveOKs = 0
	i.consecutiveFails++
	if i.status != StatusUnhealthy && i.consecutiveFails >= failThreshold {
		i.status = StatusUnhealthy
		i.consecutiveFails = 0
		return true
	}
	return false
}

// ServiceEntry holds all instances for a single logical service.
type ServiceEntry struct {
	Name        string
	LBStrategy  string
	HealthCheck HealthCheckCfg
	Instances   []*Instance
	mu          sync.RWMutex
}

// HealthCheckCfg is the health-check configuration for a service.
type HealthCheckCfg struct {
	Path             string
	Interval         time.Duration
	Timeout          time.Duration
	FailureThreshold int
	SuccessThreshold int
}

// HealthyInstances returns a snapshot of currently healthy instances.
// The snapshot is safe to iterate without holding the lock.
func (s *ServiceEntry) HealthyInstances() []*Instance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	healthy := make([]*Instance, 0, len(s.Instances))
	for _, inst := range s.Instances {
		if inst.IsHealthy() {
			healthy = append(healthy, inst)
		}
	}
	return healthy
}

// Registry maps logical service names to their ServiceEntry.
type Registry struct {
	mu       sync.RWMutex
	services map[string]*ServiceEntry
}

// New creates an empty Registry.
func New() *Registry {
	return &Registry{services: make(map[string]*ServiceEntry)}
}

// Register adds or replaces a service entry. Safe for concurrent use.
func (r *Registry) Register(entry *ServiceEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.services[entry.Name] = entry
}

// Lookup finds a service by name. Returns nil if not found.
func (r *Registry) Lookup(name string) *ServiceEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.services[name]
}

// ServiceNames returns a snapshot of all currently registered service names.
// Used by the Reloader to diff removed services on hot reload.
func (r *Registry) ServiceNames() map[string]struct{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make(map[string]struct{}, len(r.services))
	for name := range r.services {
		names[name] = struct{}{}
	}
	return names
}
