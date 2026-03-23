// Package circuitbreaker implements a two-level circuit breaker for the gateway:
//
//   - Service-level CB: one per logical service; acts as a kill switch.
//   - Per-instance CB: one per (service, instance) pair; isolates bad pods.
//
// State machine:
//
//	failure_rate > threshold
//
// Closed ────────────────────────► Open
//
//	▲                              │  after reset_timeout
//	│  successes ≥ threshold       ▼
//	└──────────────────────── Half-Open (1 probe)
package circuitbreaker

import (
	"sync"
	"time"
)

// State represents the circuit breaker's current state.
type State int

const (
	StateClosed   State = iota
	StateOpen     State = iota
	StateHalfOpen State = iota
)

// String returns a human-readable state name for logging and metrics.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// Config holds circuit breaker thresholds. Zero values receive safe defaults.
type Config struct {
	// FailureThreshold is the number of consecutive failures before tripping to Open.
	FailureThreshold int
	// SuccessThreshold is the number of consecutive successes in Half-Open before closing.
	SuccessThreshold int
	// ResetTimeout is how long to wait in Open state before moving to Half-Open.
	ResetTimeout time.Duration
}

func (c *Config) withDefaults() Config {
	out := *c
	if out.FailureThreshold == 0 {
		out.FailureThreshold = 5
	}
	if out.SuccessThreshold == 0 {
		out.SuccessThreshold = 2
	}
	if out.ResetTimeout == 0 {
		out.ResetTimeout = 30 * time.Second
	}
	return out
}

// CircuitBreaker is a thread-safe state machine that tracks failures and
// short-circuits requests when an upstream is consistently failing.
type CircuitBreaker struct {
	cfg              Config
	mu               sync.Mutex
	state            State
	consecutiveFails int
	consecutiveOKs   int
	openedAt         time.Time
	// onTransition is called (if non-nil) whenever the state changes, for metrics.
	onTransition func(from, to State)
}

// New creates a CircuitBreaker with the provided config.
// onTransition is optional; pass nil if no callback is needed.
func New(cfg Config, onTransition func(from, to State)) *CircuitBreaker {
	return &CircuitBreaker{
		cfg:          cfg.withDefaults(),
		state:        StateClosed,
		onTransition: onTransition,
	}
}

// Allow returns true if a request should be allowed through.
//
//   - Closed:    always allowed.
//   - Open:      allowed only after ResetTimeout (transitions to Half-Open and allows
//     exactly one probe request).
//   - Half-Open: allows the single probe; all other requests return false until
//     the probe resolves via RecordSuccess or RecordFailure.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		if time.Since(cb.openedAt) >= cb.cfg.ResetTimeout {
			cb.transition(StateHalfOpen)
			return true // this is the probe request
		}
		return false
	case StateHalfOpen:
		// Only one probe request is allowed; all others are rejected until resolved.
		return false
	default:
		return false
	}
}

// RecordSuccess notifies the circuit breaker that the last request succeeded.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.consecutiveFails = 0
	cb.consecutiveOKs++
	if cb.state == StateHalfOpen && cb.consecutiveOKs >= cb.cfg.SuccessThreshold {
		cb.transition(StateClosed)
	}
}

// RecordFailure notifies the circuit breaker that the last request failed.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.consecutiveOKs = 0
	cb.consecutiveFails++
	switch cb.state {
	case StateClosed:
		if cb.consecutiveFails >= cb.cfg.FailureThreshold {
			cb.openedAt = time.Now()
			cb.transition(StateOpen)
		}
	case StateHalfOpen:
		// Probe failed — go back to Open and reset the timeout.
		cb.openedAt = time.Now()
		cb.transition(StateOpen)
	}
}

// State returns the current state without modifying it.
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// transition moves the circuit breaker to a new state and fires the callback.
// Must be called with cb.mu held.
func (cb *CircuitBreaker) transition(to State) {
	from := cb.state
	cb.state = to
	cb.consecutiveFails = 0
	cb.consecutiveOKs = 0
	if cb.onTransition != nil {
		cb.onTransition(from, to)
	}
}
