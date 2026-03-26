// Package builtin provides the token-bucket rate limiting plugin.
//
// Supports two modes:
//   - per-ip:   limits by client IP extracted from X-Real-IP or RemoteAddr.
//   - per-user: limits by authenticated user ID from GatewayContext.UserID
//     (requires JWT/auth plugin to run before this plugin).
//
// Stacking both modes is recommended: per-IP provides coarse abuse protection,
// per-user provides fair per-customer quotas.
package builtin

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	pluginpkg "github.com/bharanidharansrinivasan/api-gateway/internal/plugin"
	"github.com/bharanidharansrinivasan/api-gateway/pkg/gwctx"
	"github.com/bharanidharansrinivasan/api-gateway/pkg/ratelimit"
)

// RateLimitPlugin enforces per-IP or per-user rate limits using a token bucket.
type RateLimitPlugin struct {
	strategy string // per-ip | per-user
	limiter  *ratelimit.TokenBucket
}

// NewRateLimitPlugin constructs a RateLimitPlugin from route-level config.
func NewRateLimitPlugin(cfg map[string]interface{}) (pluginpkg.Plugin, error) {
		rpm := 60.0 // default: 60 requests per minute
		burst := 10.0
		strategy := "per-ip"

		if r, ok := cfg["requests_per_minute"].(int); ok {
			rpm = float64(r)
		} else if r, ok := cfg["requests_per_minute"].(float64); ok {
			rpm = r
		}
		if b, ok := cfg["burst"].(int); ok {
			burst = float64(b)
		}
		if s, ok := cfg["strategy"].(string); ok {
			strategy = s
		}

		ratePerSec := rpm / 60.0
		return &RateLimitPlugin{
			strategy: strategy,
			limiter:  ratelimit.New(ratePerSec, burst),
		}, nil
}

func (p *RateLimitPlugin) Name() string { return "rate-limit" }

// ExecuteRequest checks the rate limit for the current client.
// On rejection, it sets standard rate-limit headers before returning 429.
func (p *RateLimitPlugin) ExecuteRequest(w http.ResponseWriter, r *http.Request) error {
	key := p.bucketKey(r)
	allowed, remaining := p.limiter.Allow(key)

	// Always set informational headers regardless of outcome.
	w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%.0f", remaining))

	if !allowed {
		w.Header().Set("Retry-After", "60")
		if gwCtx := gwctx.From(r.Context()); gwCtx != nil {
			gwCtx.FailureReason = "rate_limit_exceeded"
		}
		return &pluginpkg.AbortError{
			StatusCode: http.StatusTooManyRequests,
			Message:    "rate limit exceeded",
		}
	}
	return nil
}

// ExecuteResponse is a no-op for rate limiting.
func (p *RateLimitPlugin) ExecuteResponse(_ http.ResponseWriter, _ *http.Request) error {
	return nil
}

// bucketKey builds the limiter key from the client's IP or user ID.
func (p *RateLimitPlugin) bucketKey(r *http.Request) string {
	routeID := "unknown"
	if gwCtx := gwctx.From(r.Context()); gwCtx != nil && gwCtx.RouteID != "" {
		routeID = gwCtx.RouteID
	}

	switch p.strategy {
	case "per-user":
		if gwCtx := gwctx.From(r.Context()); gwCtx != nil && gwCtx.UserID != "" {
			return fmt.Sprintf("%s:user:%s", routeID, gwCtx.UserID)
		}
		// Fall through to per-IP if user ID is not available.
		fallthrough
	default: // per-ip
		ip := r.Header.Get("X-Real-IP")
		if ip == "" {
			ip = r.RemoteAddr
			// Strip port from "host:port" address.
			if idx := strings.LastIndex(ip, ":"); idx != -1 {
				ip = ip[:idx]
			}
		}
		return fmt.Sprintf("%s:ip:%s", routeID, ip)
	}
}

// resetTime returns the Unix timestamp of the next token refill window.
func resetTime() string {
	return fmt.Sprintf("%d", time.Now().Add(time.Minute).Unix())
}
