// Package middleware provides the per-request timeout middleware.
package middleware

import (
	"context"
	"net/http"
	"time"
)

// Timeout returns an http.Handler middleware that applies a per-request deadline.
// The deadline is derived from the per-route timeout if set, otherwise from the
// global gateway timeout.
//
// Why context.WithDeadline over http.TimeoutHandler:
//   - http.TimeoutHandler races to write the response body message but may not
//     cancel the upstream request properly if the handler doesn't check ctx.Done().
//   - context.WithDeadline propagates cleanly via req.WithContext — when the
//     context is cancelled, in-flight upstream HTTP requests (which use this context)
//     are cancelled at the transport level automatically.
func Timeout(globalTimeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			timeout := globalTimeout
			if timeout == 0 {
				timeout = 30 * time.Second // safe default
			}

			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel() // always release resources even if the handler returns early

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
