package ratelimit

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/bharanidharansrinivasan/api-gateway/pkg/circuitbreaker"
	"go.uber.org/zap"
)

// luaSlidingWindow is the Redis Lua script for atomic sliding window rate limiting.
//
// Design decisions:
//   - Uses redis.call('TIME') for the authoritative timestamp — never client clocks.
//     This eliminates clock skew issues across multiple gateway replicas.
//   - Uses INCR on a sequence key for unique member IDs — eliminates math.random()
//     collision risk where two concurrent requests could overwrite each other's ZADD entry.
//   - PEXPIRE sets TTL slightly over the window so Redis self-cleans orphaned keys.
//
// KEYS[1] = rate limit ZSET key (e.g., "rl:route1:ip:1.2.3.4")
// ARGV[1] = window size in milliseconds
// ARGV[2] = max requests per window
const luaSlidingWindow = `
local key     = KEYS[1]
local seqKey  = key .. ':seq'
local window  = tonumber(ARGV[1])
local limit   = tonumber(ARGV[2])

-- Use Redis server time (authoritative clock, no client skew)
local t        = redis.call('TIME')
local nowMs    = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local winStart = nowMs - window

-- Evict entries outside the sliding window
redis.call('ZREMRANGEBYSCORE', key, 0, winStart)

-- Count entries in the current window
local count = tonumber(redis.call('ZCARD', key))

if count < limit then
    -- Unique member: server timestamp + monotonically increasing counter
    local seq    = redis.call('INCR', seqKey)
    local member = nowMs .. ':' .. seq
    redis.call('ZADD', key, nowMs, member)
    -- TTL = window + 1s buffer so keys self-expire even if never queried again
    redis.call('PEXPIRE', key, window + 1000)
    redis.call('PEXPIRE', seqKey, window + 1000)
    return {1, limit - count - 1}   -- {allowed=1, remaining}
else
    return {0, 0}                    -- {allowed=0, remaining=0}
end
`

// redisClient is the minimal subset of go-redis Client we depend on.
// Using an interface makes it easy to mock in tests without the redis package.
type redisClient interface {
	Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redisCmd
	Close() error
}

// redisCmd mocks the result type we use from go-redis.
type redisCmd interface {
	Int64Slice() ([]int64, error)
}

// RedisLimiter implements Limiter using a Redis sliding window.
// On Redis failure, it degrades gracefully to the in-memory fallback.
type RedisLimiter struct {
	addr         string
	windowMs     int64
	limit        int64
	fallback     Limiter
	cb           *circuitbreaker.CircuitBreaker
	fallbackMode atomic.Bool // true = skip Redis, use fallback directly
	log          *zap.Logger

	// evalFn executes the Lua script. Injected for testability.
	evalFn func(ctx context.Context, key string) ([]int64, error)

	// closeFn closes the Redis client.
	closeFn func() error
}

// NewRedisLimiter creates a RedisLimiter.
// It uses go-redis under the hood but the constructor avoids an import cycle
// by accepting a pre-built eval function.
//
// In production, call NewRedisLimiterFromConfig which handles the go-redis setup.
func newRedisLimiter(
	addr string,
	windowMs, limit int64,
	evalFn func(ctx context.Context, key string) ([]int64, error),
	closeFn func() error,
	fallback Limiter,
	log *zap.Logger,
) *RedisLimiter {
	cb := circuitbreaker.New(circuitbreaker.Config{
		FailureThreshold: 5,
		SuccessThreshold: 2,
		ResetTimeout:     30 * time.Second,
	}, nil)

	return &RedisLimiter{
		addr:     addr,
		windowMs: windowMs,
		limit:    limit,
		fallback: fallback,
		cb:       cb,
		evalFn:   evalFn,
		closeFn:  closeFn,
		log:      log,
	}
}

// Allow implements Limiter. Checks rate limit via Redis sliding window.
// Falls back to in-memory on Redis failure.
func (r *RedisLimiter) Allow(ctx context.Context, key string) (bool, float64, error) {
	// Fast path: circuit breaker open → skip Redis, go directly to fallback.
	if r.fallbackMode.Load() || !r.cb.Allow() {
		if !r.fallbackMode.Load() {
			r.fallbackMode.Store(true)
			r.log.Warn("redis rate limiter circuit breaker open — using in-memory fallback",
				zap.String("addr", r.addr))
			// Schedule recovery probe after reset timeout.
			go func() {
				time.Sleep(30 * time.Second)
				r.fallbackMode.Store(false)
			}()
		}
		return r.fallback.Allow(ctx, key)
	}

	// Add a hard 45ms deadline (tighter than ReadTimeout) so Redis can't stall the hot path.
	callCtx, cancel := context.WithTimeout(ctx, 45*time.Millisecond)
	defer cancel()

	result, err := r.evalFn(callCtx, key)
	if err != nil {
		r.cb.RecordFailure()
		r.log.Warn("redis rate limiter error — falling back to in-memory",
			zap.String("addr", r.addr),
			zap.String("key", key),
			zap.Error(err),
		)
		return r.fallback.Allow(ctx, key)
	}

	r.cb.RecordSuccess()

	if len(result) < 2 {
		return false, 0, fmt.Errorf("ratelimit: unexpected redis result length %d", len(result))
	}
	return result[0] == 1, float64(result[1]), nil
}

// Close releases the Redis connection pool and the fallback limiter.
func (r *RedisLimiter) Close() error {
	if r.closeFn != nil {
		if err := r.closeFn(); err != nil {
			return err
		}
	}
	return r.fallback.Close()
}

// NewRedisLimiter is the production constructor that wires go-redis.
// Imported lazily to avoid import cycles. Called from factory.go.
func NewRedisLimiter(cfg RedisConfig, windowMs, limit int64, fallback Limiter) *RedisLimiter {
	// We use a lazy import pattern here to keep go-redis optional.
	// The actual go-redis wiring is in redis_client.go (generated by go get).
	return buildRedisLimiter(cfg, windowMs, limit, fallback)
}
