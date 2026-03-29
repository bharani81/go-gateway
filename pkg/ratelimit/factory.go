package ratelimit

import (
	"fmt"
	"time"
)

// LimiterConfig controls which backend the rate limiter uses and how.
type LimiterConfig struct {
	// Backend selects the implementation: "memory" (default) or "redis".
	Backend string

	// Redis holds connection settings, used only when Backend == "redis".
	Redis RedisConfig

	// RequestsPerMin is the sustained request rate (tokens refilled per minute).
	RequestsPerMin float64

	// Burst is the max burst capacity above the sustained rate (memory backend only).
	Burst float64

	// WindowSeconds is the sliding window duration for the Redis backend.
	WindowSeconds int

	// FallbackPolicy controls what happens when BOTH Redis and the in-memory
	// fallback fail. "pass" (default) allows the request through; "reject"
	// returns a 429.
	FallbackPolicy string
}

// RedisConfig holds connection parameters for the Redis rate limiter backend.
type RedisConfig struct {
	Addr         string        // "host:port", default "localhost:6379"
	Password     string
	DB           int
	DialTimeout  time.Duration // default 2s
	ReadTimeout  time.Duration // default 50ms — keeps rate check on the hot path fast
	WriteTimeout time.Duration // default 50ms
	PoolSize     int           // default = 10 * GOMAXPROCS
	MinIdleConns int           // keep warm connections available
}

// NewFromConfig creates a Limiter based on cfg.
//   - Backend == "memory" (or empty): in-memory token bucket (zero dependencies).
//   - Backend == "redis": Redis sliding window with in-memory fallback.
func NewFromConfig(cfg LimiterConfig) (Limiter, error) {
	ratePerSec := cfg.RequestsPerMin / 60.0
	burst := cfg.Burst
	if burst <= 0 {
		burst = 10
	}

	memLimiter := New(ratePerSec, burst)

	switch cfg.Backend {
	case "redis":
		if cfg.Redis.Addr == "" {
			cfg.Redis.Addr = "localhost:6379"
		}
		if cfg.WindowSeconds <= 0 {
			cfg.WindowSeconds = 60
		}
		windowMs := int64(cfg.WindowSeconds) * 1000
		limit := int64(cfg.RequestsPerMin / 60.0 * float64(cfg.WindowSeconds))
		if limit <= 0 {
			return nil, fmt.Errorf("ratelimit: redis backend requires requests_per_minute > 0")
		}
		return NewRedisLimiter(cfg.Redis, windowMs, limit, memLimiter), nil

	case "memory", "":
		return memLimiter, nil

	default:
		return nil, fmt.Errorf("ratelimit: unknown backend %q (valid: memory, redis)", cfg.Backend)
	}
}
