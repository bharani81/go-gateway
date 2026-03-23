// Package middleware provides the context initialization middleware.
// It runs as the first middleware in the chain for every request and creates
// the GatewayContext that all downstream code depends on.
package middleware

import (
	"net"
	"net/http"
	"time"

	"github.com/bharanidharansrinivasan/api-gateway/pkg/gwctx"
	"github.com/google/uuid"
)

// InjectContext creates a GatewayContext for each request and stores it in
// the request context. It also handles W3C traceparent propagation so that
// distributed tracing works across service boundaries.
//
// Trust policy for X-Request-ID: if the header is present AND the request comes
// from a trusted IP range (e.g., internal load balancer), preserve it.
// Otherwise, generate a fresh UUID. This prevents external clients from
// injecting arbitrary request IDs into the log correlation chain.
func InjectContext(trustedRanges []*net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientIP, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				clientIP = r.RemoteAddr
			}
			ip := net.ParseIP(clientIP)

			// Generate or propagate Request ID.
			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" || !isTrusted(ip, trustedRanges) {
				requestID = uuid.New().String()
			}

			// Propagate or generate Trace ID from W3C traceparent.
			traceID := extractTraceID(r.Header.Get("Traceparent"))
			if traceID == "" {
				traceID = uuid.New().String()
			}

			gwCtx := &gwctx.GatewayContext{
				RequestID:    requestID,
				TraceID:      traceID,
				ClientIP:     ip,
				StartTime:    time.Now(),
				CircuitState: gwctx.CircuitClosed,
			}

			// Propagate request ID and trace parent outward to clients.
			w.Header().Set("X-Request-ID", requestID)

			ctx := gwctx.NewContext(r.Context(), gwCtx)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// isTrusted returns true if ip falls within any of the trusted CIDR ranges.
func isTrusted(ip net.IP, ranges []*net.IPNet) bool {
	for _, cidr := range ranges {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// extractTraceID parses the trace-id segment from a W3C traceparent header.
// Format: "00-{trace_id}-{parent_span_id}-{flags}"
// Returns empty string on any parse failure.
func extractTraceID(traceparent string) string {
	if traceparent == "" {
		return ""
	}
	parts := splitTraceparent(traceparent)
	if len(parts) != 4 || parts[1] == "" {
		return ""
	}
	return parts[1]
}

func splitTraceparent(s string) []string {
	parts := make([]string, 0, 4)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}
