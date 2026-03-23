// Package plugin provides the chain executor that runs an ordered list of plugins
// for each gateway request (pre-proxy phase) and response (post-proxy phase).
package plugin

import (
	"fmt"
	"net/http"

	"go.uber.org/zap"
)

// Chain executes an ordered slice of plugins for a single request/response cycle.
type Chain struct {
	plugins []Plugin
	log     *zap.Logger
}

// NewChain creates a Chain from an ordered plugin slice.
func NewChain(plugins []Plugin, log *zap.Logger) *Chain {
	return &Chain{plugins: plugins, log: log}
}

// ExecuteRequest runs all plugins' ExecuteRequest methods in order.
//
// Fail-fast semantics: if a plugin returns an AbortError, the error response
// is written to w, and execution stops immediately. No upstream proxying occurs.
//
// If a plugin panics, the panic is recovered, a 500 is written to w, and
// the chain stops. This prevents one misbehaving plugin from crashing the server.
//
// Returns errAborted (non-nil) if the chain was stopped by any plugin.
func (c *Chain) ExecuteRequest(w http.ResponseWriter, r *http.Request) (aborted bool) {
	for _, p := range c.plugins {
		var err error
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					err = &AbortError{StatusCode: http.StatusInternalServerError, Message: "internal server error"}
					c.log.Error("plugin panicked during request",
						zap.String("plugin", p.Name()),
						zap.String("panic", fmt.Sprintf("%v", rec)),
					)
				}
			}()
			err = p.ExecuteRequest(w, r)
		}()

		if err != nil {
			if abortErr, ok := err.(*AbortError); ok {
				writeJSONError(w, abortErr.StatusCode, abortErr.Message)
			} else {
				c.log.Error("unexpected plugin error during request",
					zap.String("plugin", p.Name()),
					zap.Error(err),
				)
				writeJSONError(w, http.StatusInternalServerError, "internal server error")
			}
			return true
		}
	}
	return false
}

// ExecuteResponse runs all plugins' ExecuteResponse methods in order.
//
// Continue-on-error semantics: errors are logged as warnings but do NOT alter
// the response. Headers may already be written to the client by this point,
// so aborting would leave the connection in a corrupt state.
func (c *Chain) ExecuteResponse(w http.ResponseWriter, r *http.Request) {
	for _, p := range c.plugins {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					c.log.Warn("plugin panicked during response (ignoring)",
						zap.String("plugin", p.Name()),
						zap.String("panic", fmt.Sprintf("%v", rec)),
					)
				}
			}()
			if err := p.ExecuteResponse(w, r); err != nil {
				c.log.Warn("plugin error during response (ignoring)",
					zap.String("plugin", p.Name()),
					zap.Error(err),
				)
			}
		}()
	}
}

// writeJSONError writes a structured JSON error body if headers haven't been sent.
func writeJSONError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":%q,"status":%d}`, message, code)
}
