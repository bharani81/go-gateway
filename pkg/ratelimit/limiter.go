// Package ratelimit defines the Limiter interface shared by the in-memory
// token bucket and the Redis sliding window backends.
package ratelimit

import (
	"context"
	"io"
)

// Limiter is the interface implemented by all rate limiting backends.
// The rate limit plugin depends only on this interface, not on concrete implementations.
type Limiter interface {
	// Allow checks if the given key has quota remaining and consumes one token if so.
	// key should encode the rate limit strategy (e.g., "route1:ip:1.2.3.4").
	// Returns (allowed, remainingTokens, error).
	// error is non-nil only when both the primary and fallback backends fail.
	Allow(ctx context.Context, key string) (allowed bool, remaining float64, err error)

	// Close releases any resources held by the limiter (e.g., sweep goroutines,
	// Redis connections). Called by Chain.Close() on runtime retirement.
	io.Closer
}
