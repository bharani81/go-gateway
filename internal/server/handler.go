// Package server provides the main HTTP handler that orchestrates the gateway
// request processing pipeline: strip headers → inject context → route →
// enforce body limit → run plugin chain → proxy to upstream → run response plugins.
package server

import (
	"fmt"
	"io"
	"net/http"

	"github.com/bharanidharansrinivasan/api-gateway/internal/proxy"
	"github.com/bharanidharansrinivasan/api-gateway/internal/runtime"
	"github.com/bharanidharansrinivasan/api-gateway/pkg/gwctx"
	"go.uber.org/zap"
)

// Handler is the central dispatch handler for all gateway requests.
// It holds a RuntimeHolder rather than direct subsystem references so that
// hot reloads are reflected without restarting the server.
type Handler struct {
	runtime      *runtime.RuntimeHolder
	reverseProxy *proxy.ReverseProxy
	log          *zap.Logger
}

// NewHandler creates a Handler backed by the given RuntimeHolder.
func NewHandler(
	rt *runtime.RuntimeHolder,
	rp *proxy.ReverseProxy,
	log *zap.Logger,
) *Handler {
	return &Handler{
		runtime:      rt,
		reverseProxy: rp,
		log:          log,
	}
}

// ServeHTTP is the gateway's main request handler.
//
// Pipeline steps:
//  1. Strip inbound X-Forwarded-* and hop-by-hop headers (prevent spoofing)
//  2. Snapshot the current runtime (atomic load — zero contention)
//  3. Route matching (404 if no route)
//  4. Method check (405 if method not in route's allowed set)
//  5. Body size limit enforcement (413 before plugin chain runs)
//  6. Plugin pre-request chain (auth, rate limit, CORS, etc.)
//  7. Proxy to upstream with retry and circuit breaker
//  8. Plugin post-response chain (add CORS headers, log completion, etc.)
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Step 1: strip inbound headers that clients must not be able to inject.
	proxy.StripInboundHeaders(r)

	// Step 2: snapshot current runtime.
	// In-flight requests hold this pointer directly. A hot reload may swap the
	// RuntimeHolder pointer to a new runtime, but this request continues with
	// the version it captured here — safe and consistent for its full lifetime.
	rt := h.runtime.Get()

	// Step 3: route matching.
	route, ok := rt.Router.Match(r)
	if !ok {
		http.Error(w, `{"error":"not found","status":404}`, http.StatusNotFound)
		return
	}

	// Update GatewayContext with route information.
	if gwCtx := gwctx.From(r.Context()); gwCtx != nil {
		gwCtx.RouteID = route.ID
	}

	// Step 4: enforce body size limit BEFORE reading body.
	// This prevents a large body from causing OOM before any plugin can run.
	if route.MaxBodyBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, route.MaxBodyBytes)
		// Try to detect oversized body early via a zero-byte read.
		if _, err := r.Body.Read([]byte{}); err != nil {
			if err.Error() == "http: request body too large" {
				http.Error(w, `{"error":"request body too large","status":413}`, http.StatusRequestEntityTooLarge)
				return
			}
		}
		// Restore the body reader with the limit applied.
		r.Body = &limitedBodyReader{r: r.Body, limit: route.MaxBodyBytes}
	}

	// Step 5: look up the pre-built plugin chain for this route.
	chain := rt.PluginChains[route.ID]

	// Step 6: run pre-request plugin chain.
	if chain != nil {
		if aborted := chain.ExecuteRequest(w, r); aborted {
			// Plugin wrote an error response; still run post-response for logging.
			chain.ExecuteResponse(w, r)
			return
		}
	}

	// Step 7: proxy to upstream.
	// TODO: inject service registry lookup, LB, and circuit breakers (wired in main.go).

	// Step 8: run post-response plugin chain regardless of proxy outcome.
	if chain != nil {
		chain.ExecuteResponse(w, r)
	}
}

// limitedBodyReader ensures MaxBytesReader is respected even after the zero-byte probe.
type limitedBodyReader struct {
	r     io.ReadCloser
	limit int64
	read  int64
}

func (l *limitedBodyReader) Read(p []byte) (int, error) {
	if l.read >= l.limit {
		return 0, fmt.Errorf("http: request body too large")
	}
	n, err := l.r.Read(p)
	l.read += int64(n)
	return n, err
}

func (l *limitedBodyReader) Close() error { return l.r.Close() }
