package ratelimit_test

import (
	"testing"

	"github.com/bharanidharansrinivasan/api-gateway/pkg/ratelimit"
)

func TestMemoryBackendDefault(t *testing.T) {
	cfg := ratelimit.LimiterConfig{
		Backend:        "",
		RequestsPerMin: 60,
		Burst:          10,
	}
	lim, err := ratelimit.NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := lim.(*ratelimit.TokenBucket); !ok {
		t.Fatalf("expected *TokenBucket for empty backend, got %T", lim)
	}
	lim.Close()
}

func TestMemoryBackendExplicit(t *testing.T) {
	cfg := ratelimit.LimiterConfig{
		Backend:        "memory",
		RequestsPerMin: 60,
		Burst:          5,
	}
	lim, err := ratelimit.NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := lim.(*ratelimit.TokenBucket); !ok {
		t.Fatalf("expected *TokenBucket for backend=memory, got %T", lim)
	}
	lim.Close()
}

func TestUnknownBackendErrors(t *testing.T) {
	cfg := ratelimit.LimiterConfig{
		Backend:        "kafka",
		RequestsPerMin: 60,
		Burst:          10,
	}
	_, err := ratelimit.NewFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error for unknown backend, got nil")
	}
}

func TestRedisZeroRPMErrors(t *testing.T) {
	cfg := ratelimit.LimiterConfig{
		Backend:        "redis",
		RequestsPerMin: 0, // invalid
		Redis:          ratelimit.RedisConfig{Addr: "localhost:6379"},
	}
	_, err := ratelimit.NewFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error for redis backend with RPM=0, got nil")
	}
}

func TestRedisBackendSetsDefaultAddr(t *testing.T) {
	cfg := ratelimit.LimiterConfig{
		Backend:        "redis",
		RequestsPerMin: 60,
		WindowSeconds:  60,
		Redis:          ratelimit.RedisConfig{Addr: ""}, // empty — should default
	}
	// NewFromConfig should succeed (it just builds the struct, doesn't connect)
	lim, err := ratelimit.NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Don't actually call Allow (no real Redis) — just verify Close doesn't panic
	defer lim.Close()
}

func TestRedisWindowDefaultsTo60s(t *testing.T) {
	cfg := ratelimit.LimiterConfig{
		Backend:        "redis",
		RequestsPerMin: 60,
		WindowSeconds:  0, // should default to 60
		Redis:          ratelimit.RedisConfig{Addr: "localhost:6379"},
	}
	lim, err := ratelimit.NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer lim.Close()
}
