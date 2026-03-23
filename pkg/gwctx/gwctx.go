// Package gwctx defines GatewayContext — the single request-scoped metadata
// struct carried through the entire gateway pipeline via context.Context.
//
// Design rationale: instead of scattering multiple typed context keys across
// packages (which is error-prone and hard to test), GatewayContext consolidates
// all request-scoped values into one struct stored under a single private key.
// All subsystems call gwctx.From(ctx) to get the pointer, then read or write
// fields directly. This is safe because each HTTP request is served by exactly
// one goroutine — there is no concurrent write to the same struct.
package gwctx

import (
	"context"
	"net"
	"time"
)

// contextKey is an unexported type to prevent external packages from accidentally
// using the same key and colliding with our context value.
type contextKey struct{}

// CircuitState represents the state of a circuit breaker at dispatch time.
type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"
	CircuitOpen     CircuitState = "open"
	CircuitHalfOpen CircuitState = "half_open"
)

// GatewayContext holds all request-scoped metadata produced and consumed by
// different stages of the gateway pipeline.
type GatewayContext struct {
	// Set by entry middleware
	RequestID string
	TraceID   string
	ClientIP  net.IP
	StartTime time.Time

	// Set by router
	RouteID string

	// Set by JWT auth plugin
	AuthClaims map[string]interface{}
	UserID     string // extracted from auth claims sub claim

	// Set by circuit breaker check (before load balancer)
	CircuitState CircuitState

	// Set by load balancer
	SelectedInstance string // "host:port"
	ServiceName      string

	// Updated by proxy retry loop
	RetryCount int

	// Set on any failure path for access log enrichment
	FailureReason string
}

// NewContext creates a new context carrying a fresh GatewayContext.
func NewContext(parent context.Context, gwCtx *GatewayContext) context.Context {
	return context.WithValue(parent, contextKey{}, gwCtx)
}

// From retrieves the GatewayContext from ctx. Returns nil if not present.
// Callers must check for nil if the context origin is uncertain.
func From(ctx context.Context) *GatewayContext {
	v := ctx.Value(contextKey{})
	if v == nil {
		return nil
	}
	return v.(*GatewayContext)
}

// MustFrom retrieves GatewayContext from ctx and panics if not present.
// Use only in code paths that are guaranteed to run after context injection.
func MustFrom(ctx context.Context) *GatewayContext {
	gwCtx := From(ctx)
	if gwCtx == nil {
		panic("gwctx: GatewayContext not found in context — was entry middleware applied?")
	}
	return gwCtx
}
