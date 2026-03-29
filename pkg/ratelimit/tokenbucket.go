// Package ratelimit implements a token-bucket rate limiter with lazy TTL eviction
// to prevent unbounded memory growth from long-tail of inactive clients.
//
// Design:
//   - In-memory sync.Map for zero-latency, no-external-dependency operation.
//   - Lazy eviction on bucket read: if the bucket hasn't been touched in evictionTTL
//     it is treated as fresh (and the old entry deleted).
//   - Periodic sweep goroutine catches any buckets that are never read again.
//   - Close() stops the sweep goroutine, preventing goroutine leaks on hot reload.
//
// For multi-instance deployments, use the Redis-backed sliding window implementation
// in redis.go, which shares the Limiter interface.
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
// It implements the Limiter interface and io.Closer.
type TokenBucket struct {
	rate        float64 // tokens refilled per second
	burstSize   float64 // max tokens (bucket capacity)
	evictionTTL time.Duration
	store       sync.Map  // key string → *bucket
	stopCh      chan struct{}
	stopOnce    sync.Once
}

// New creates a TokenBucket limiter.
//   - rate: tokens per second to refill (e.g., 10.0 for 10 req/s steady-state)
//   - burst: max tokens in the bucket at once (allows short burst above sustained rate)
func New(rate, burst float64) *TokenBucket {
	return &TokenBucket{
		rate:        rate,
		burstSize:   burst,
		evictionTTL: defaultEvictionTTL,
		stopCh:      make(chan struct{}),
	}
}

// StartSweep launches a background goroutine that periodically removes buckets
// that have been inactive for longer than evictionTTL. Call this once at startup.
// The goroutine stops when ctx is cancelled OR Close() is called — whichever comes first.
func (tb *TokenBucket) StartSweep(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(defaultSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tb.stopCh:
				return
			case <-ticker.C:
				tb.sweep()
			}
		}
	}()
}

// Close stops the background sweep goroutine.
// Safe to call multiple times. Used by Chain.Close() when a runtime is retired on hot reload.
func (tb *TokenBucket) Close() error {
	tb.stopOnce.Do(func() { close(tb.stopCh) })
	return nil
}

// Allow checks if the given key has sufficient tokens and consumes one if so.
// Returns (true, remaining tokens) if allowed, (false, 0) if rejected.
func (tb *TokenBucket) allow(key string) (allowed bool, remaining float64) {
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

// Allow implements the Limiter interface.
// ctx is used for cancellation — if ctx is done, returns (false, 0, ctx.Err()).
func (tb *TokenBucket) Allow(ctx context.Context, key string) (bool, float64, error) {
	if err := ctx.Err(); err != nil {
		return false, 0, err
	}
	allowed, rem := tb.allow(key)
	return allowed, rem, nil
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
