// Package middleware provides the panic recovery middleware.
package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"go.uber.org/zap"
)

// Recovery returns an http.Handler middleware that catches panics in downstream
// handlers and writes a 500 response instead of crashing the server process.
// The panic and stack trace are logged at ERROR level.
func Recovery(log *zap.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				stack := debug.Stack()
				log.Error("panic recovered in HTTP handler",
					zap.String("panic", fmt.Sprintf("%v", rec)),
					zap.ByteString("stack", stack),
					zap.String("method", r.Method),
					zap.String("path", r.URL.Path),
				)
				http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
