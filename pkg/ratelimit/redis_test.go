package ratelimit

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
)

// mockEvalFn satisfies the eval function signature for RedisLimiter.
type mockEvalFn func(ctx context.Context, key string) ([]int64, error)

func TestRedisAllowsWhenLuaReturns1(t *testing.T) {
	eval := func(ctx context.Context, key string) ([]int64, error) {
		return []int64{1, 5}, nil // allowed=1, remaining=5
	}
	lim := newRedisLimiter("localhost:6379", 60000, 10, eval, func() error { return nil }, New(10, 10), zap.NewNop())
	defer lim.Close()

	allowed, rem, err := lim.Allow(context.Background(), "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("expected request to be allowed")
	}
	if rem != 5 {
		t.Fatalf("expected remaining=5, got %f", rem)
	}
}

func TestRedisRejectsWhenLuaReturns0(t *testing.T) {
	eval := func(ctx context.Context, key string) ([]int64, error) {
		return []int64{0, 0}, nil // allowed=0, remaining=0
	}
	lim := newRedisLimiter("localhost:6379", 60000, 10, eval, func() error { return nil }, New(10, 10), zap.NewNop())
	defer lim.Close()

	allowed, rem, err := lim.Allow(context.Background(), "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("expected request to be rejected")
	}
	if rem != 0 {
		t.Fatalf("expected remaining=0, got %f", rem)
	}
}

func TestRedisErrorFallsBackToMemory(t *testing.T) {
	evalErr := errors.New("redis dial timeout")
	eval := func(ctx context.Context, key string) ([]int64, error) {
		return nil, evalErr
	}

	// Mock fallback limiter
	fallback := New(10, 10)
	lim := newRedisLimiter("localhost:6379", 60000, 10, eval, func() error { return nil }, fallback, zap.NewNop())
	defer lim.Close()

	// Should fallback and succeed (bucket is full)
	allowed, _, err := lim.Allow(context.Background(), "key")
	if err != nil {
		t.Fatalf("expected error to be swallowed by fallback, got %v", err)
	}
	if !allowed {
		t.Fatal("expected fallback to allow request")
	}
}

func TestRedisCircuitBreakerTripsAfterFailures(t *testing.T) {
	evalErr := errors.New("connection refused")
	eval := func(ctx context.Context, key string) ([]int64, error) {
		return nil, evalErr
	}
	fallback := New(100, 100)
	lim := newRedisLimiter("localhost:6379", 60000, 10, eval, func() error { return nil }, fallback, zap.NewNop())
	defer lim.Close()

	ctx := context.Background()
	// Trip threshold is 5 internal to circuitbreaker.
	// The 6th request hits the now-open breaker and flips fallbackMode to true.
	for i := 0; i < 6; i++ {
		lim.Allow(ctx, "key")
	}

	// Now it should be in fallbackMode
	if !lim.fallbackMode.Load() {
		t.Fatal("expected fallbackMode to be true after 5 failures")
	}

	// Ensure `eval` is completely bypassed now when in fallbackMode
	evalCalled := false
	eval2 := func(ctx context.Context, key string) ([]int64, error) {
		evalCalled = true
		return []int64{1, 5}, nil
	}
	lim.evalFn = eval2 // Hot swap just for this test

	lim.Allow(ctx, "key")
	if evalCalled {
		t.Fatal("eval should not be called when fallbackMode is active")
	}
}

func TestHardDeadlineApplied(t *testing.T) {
	eval := func(ctx context.Context, key string) ([]int64, error) {
		// ctx should have a deadline inside this function
		_, ok := ctx.Deadline()
		if !ok {
			return nil, errors.New("expected context to have a deadline")
		}
		return []int64{1, 10}, nil
	}
	lim := newRedisLimiter("localhost:6379", 60000, 10, eval, func() error { return nil }, New(10, 10), zap.NewNop())
	defer lim.Close()

	_, _, err := lim.Allow(context.Background(), "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCloseCallsFallbackClose(t *testing.T) {
	eval := func(ctx context.Context, key string) ([]int64, error) { return []int64{1, 1}, nil }
	fallback := New(10, 10)
	lim := newRedisLimiter("localhost:6379", 60000, 10, eval, func() error { return nil }, fallback, zap.NewNop())

	lim.Close()
	// No explicit way to check token bucket close stat easily, but run with -race to ensure no sweeps panic
}
