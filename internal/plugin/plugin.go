// Package plugin defines the Plugin interface and the AbortError type used
// by plugins to signal that a request should be halted and an HTTP error returned.
package plugin

import (
	"fmt"
	"net/http"
)

// Plugin is the interface implemented by every gateway plugin.
//
// Ordering contract: execution order within a route is determined exclusively
// by the `order` field in the route's plugin config. There is no Priority()
// method — that would create hidden ordering conflicts across implementations.
type Plugin interface {
	// Name returns the plugin's registered name (e.g., "jwt-auth").
	Name() string

	// ExecuteRequest runs before the request is forwarded to the upstream.
	// Return an AbortError to short-circuit the chain and write an HTTP error.
	// Any other non-nil error is treated as an unexpected failure and maps to 500.
	ExecuteRequest(w http.ResponseWriter, r *http.Request) error

	// ExecuteResponse runs after the upstream response is received.
	// Errors here are logged at WARN level but do NOT alter the response —
	// headers may already be written by the time this executes.
	ExecuteResponse(w http.ResponseWriter, r *http.Request) error
}

// AbortError signals that the plugin chain should stop and the given HTTP status
// code + message should be written to the client.
// It is NOT an unexpected error; it is the intended signal for auth failures,
// rate limit hits, CORS rejections, etc.
type AbortError struct {
	StatusCode int
	Message    string
}

// Error implements the error interface.
func (e *AbortError) Error() string {
	return fmt.Sprintf("plugin abort: HTTP %d: %s", e.StatusCode, e.Message)
}

// IsAbort returns true if the error is an AbortError.
func IsAbort(err error) bool {
	_, ok := err.(*AbortError)
	return ok
}
