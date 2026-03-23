// Package ratelimit implements a token-bucket rate limiter with lazy TTL eviction
// to prevent unbounded memory growth from long-tail of inactive clients.
//
// Design:
//   - In-memory sync.Map for zero-latency, no-external-dependency operation.
//   - Lazy eviction on bucket read: if the bucket hasn't been touched in evictionTTL
//     it is treated as fresh (and the old entry deleted).
//   - Periodic sweep goroutine catches any buckets that are never read again.
//
// For multi-instance deployments, swap out the in-memory store for a Redis-backed
// implementation using the same token-bucket Lua script documented in the design doc.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

const defaultEvictionTTL = 10 * time.Minute
const defaultSweepInterval = 5 * time.Minute

// bucket holds the token-bucket state for one client key.
type bucket struct {
	mu         sync.Mutex
	tokens     float64
	lastRefill time.Time
}

// TokenBucket is a concurrent, in-memory token-bucket rate limiter.
type TokenBucket struct {
	rate        float64 // tokens refilled per second
	burstSize   float64 // max tokens (bucket capacity)
	evictionTTL time.Duration
	store       sync.Map // key string → *bucket
}

// New creates a TokenBucket limiter.
//   - rate: tokens per second to refill (e.g., 10.0 for 10 req/s steady-state)
//   - burst: max tokens in the bucket at once (allows short burst above sustained rate)
func New(rate, burst float64) *TokenBucket {
	return &TokenBucket{
		rate:        rate,
		burstSize:   burst,
		evictionTTL: defaultEvictionTTL,
	}
}

// StartSweep launches a background goroutine that periodically removes buckets
// that have been inactive for longer than evictionTTL. Call this once at startup.
func (tb *TokenBucket) StartSweep(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(defaultSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tb.sweep()
			}
		}
	}()
}

// Allow checks if the given key has sufficient tokens and consumes one if so.
// Returns (true, remaining tokens) if allowed, (false, 0) if rejected.
func (tb *TokenBucket) Allow(key string) (allowed bool, remaining float64) {
	now := time.Now()

	// Load or initialise the bucket.
	val, _ := tb.store.LoadOrStore(key, &bucket{
		tokens:     tb.burstSize,
		lastRefill: now,
	})
	b := val.(*bucket)

	b.mu.Lock()
	defer b.mu.Unlock()

	// Lazy eviction: bucket not touched in evictionTTL → treat as fresh.
	if now.Sub(b.lastRefill) > tb.evictionTTL {
		b.tokens = tb.burstSize
		b.lastRefill = now
	}

	// Refill tokens proportional to elapsed time.
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * tb.rate
	if b.tokens > tb.burstSize {
		b.tokens = tb.burstSize
	}
	b.lastRefill = now

	if b.tokens < 1.0 {
		return false, 0
	}
	b.tokens--
	return true, b.tokens
}

// sweep deletes buckets whose lastRefill is older than evictionTTL.
func (tb *TokenBucket) sweep() {
	cutoff := time.Now().Add(-tb.evictionTTL)
	tb.store.Range(func(key, val interface{}) bool {
		b := val.(*bucket)
		b.mu.Lock()
		inactive := b.lastRefill.Before(cutoff)
		b.mu.Unlock()
		if inactive {
			tb.store.Delete(key)
		}
		return true
	})
}
