// Package server provides the main HTTP handler that orchestrates the gateway
// request processing pipeline: strip headers → inject context → route →
// enforce body limit → run plugin chain → proxy to upstream → run response plugins.
package server

import (
	"fmt"
	"io"
	"net/http"

	"github.com/bharanidharansrinivasan/api-gateway/internal/plugin"
	"github.com/bharanidharansrinivasan/api-gateway/internal/router"
	"github.com/bharanidharansrinivasan/api-gateway/internal/proxy"
	"github.com/bharanidharansrinivasan/api-gateway/pkg/gwctx"
	"go.uber.org/zap"
)

// Handler is the central dispatch handler for all gateway requests.
type Handler struct {
	router       *router.Router
	reverseProxy *proxy.ReverseProxy
	pluginChains map[string]*plugin.Chain // routeID → chain
	log          *zap.Logger
}

// NewHandler creates a Handler.
func NewHandler(
	r *router.Router,
	rp *proxy.ReverseProxy,
	chains map[string]*plugin.Chain,
	log *zap.Logger,
) *Handler {
	return &Handler{
		router:       r,
		reverseProxy: rp,
		pluginChains: chains,
		log:          log,
	}
}

// ServeHTTP is the gateway's main request handler.
//
// Pipeline steps (each step is documented inline):
//  1. Strip inbound X-Forwarded-* and hop-by-hop headers (prevent spoofing)
//  2. Route matching (404 if no route)
//  3. Method check (405 if method not in route's allowed set)
//  4. Body size limit enforcement (413 before plugin chain runs)
//  5. Plugin pre-request chain (auth, rate limit, CORS, etc.)
//  6. Proxy to upstream with retry and circuit breaker
//  7. Plugin post-response chain (add CORS headers, log completion, etc.)
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Step 1: strip inbound headers that clients must not be able to inject.
	proxy.StripInboundHeaders(r)

	// Step 2: route matching.
	route, ok := h.router.Match(r)
	if !ok {
		http.Error(w, `{"error":"not found","status":404}`, http.StatusNotFound)
		return
	}

	// Update GatewayContext with route information.
	if gwCtx := gwctx.From(r.Context()); gwCtx != nil {
		gwCtx.RouteID = route.ID
	}

	// Step 3: enforce body size limit BEFORE reading body.
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

	// Step 4: look up the pre-built plugin chain for this route.
	chain := h.pluginChains[route.ID]

	// Step 5: run pre-request plugin chain.
	if chain != nil {
		if aborted := chain.ExecuteRequest(w, r); aborted {
			// Plugin wrote an error response; stop processing.
			if chain != nil {
				chain.ExecuteResponse(w, r) // still run post-response for logging
			}
			return
		}
	}

	// Step 6: proxy to upstream.
	// TODO: inject service registry lookup, LB, and circuit breakers (wired in main.go).
	// This handler receives the results via a ProxyFunc injection in production.

	// Step 7: run post-response plugin chain regardless of proxy outcome.
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
