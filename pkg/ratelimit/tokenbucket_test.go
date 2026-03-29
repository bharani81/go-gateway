package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/bharanidharansrinivasan/api-gateway/pkg/ratelimit"
)

// compile-time check: *TokenBucket must satisfy Limiter
var _ ratelimit.Limiter = (*ratelimit.TokenBucket)(nil)

func TestAllowConsumesToken(t *testing.T) {
	tb := ratelimit.New(1, 1) // 1 token/s, burst=1
	ctx := context.Background()

	allowed, _, _ := tb.Allow(ctx, "key")
	if !allowed {
		t.Fatal("first request should be allowed (bucket starts full)")
	}
	allowed, _, _ = tb.Allow(ctx, "key")
	if allowed {
		t.Fatal("second request should be rejected (burst exhausted)")
	}
}

func TestBurstCapacity(t *testing.T) {
	burst := 10.0
	tb := ratelimit.New(0.1, burst) // very slow refill, burst=10
	ctx := context.Background()

	for i := 0; i < int(burst); i++ {
		allowed, _, _ := tb.Allow(ctx, "burst-key")
		if !allowed {
			t.Fatalf("request %d should be allowed within burst capacity", i+1)
		}
	}
	allowed, _, _ := tb.Allow(ctx, "burst-key")
	if allowed {
		t.Fatal("request beyond burst should be rejected")
	}
}

func TestRefillOverTime(t *testing.T) {
	ratePerSec := 10.0
	tb := ratelimit.New(ratePerSec, 1) // burst=1
	ctx := context.Background()

	tb.Allow(ctx, "refill-key") // exhaust the single token
	time.Sleep(150 * time.Millisecond) // wait for ~1.5 tokens to refill (only 1 can be held)
	allowed, _, _ := tb.Allow(ctx, "refill-key")
	if !allowed {
		t.Fatal("token should have refilled after sleep")
	}
}

func TestPerKeyIsolation(t *testing.T) {
	tb := ratelimit.New(1, 1)
	ctx := context.Background()

	tb.Allow(ctx, "key-a") // exhaust key-a
	allowed, _, _ := tb.Allow(ctx, "key-b")
	if !allowed {
		t.Fatal("key-b should not be affected by key-a exhaustion")
	}
}

func TestRemainingDecrements(t *testing.T) {
	tb := ratelimit.New(1, 3) // burst=3
	ctx := context.Background()

	_, rem0, _ := tb.Allow(ctx, "rem-key")
	_, rem1, _ := tb.Allow(ctx, "rem-key")
	if rem1 >= rem0 {
		t.Fatalf("remaining should decrease: first=%v, second=%v", rem0, rem1)
	}
}

func TestCancelledContextRejected(t *testing.T) {
	tb := ratelimit.New(100, 100)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, _, err := tb.Allow(ctx, "any-key")
	if err == nil {
		t.Fatal("cancelled context should return an error")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	tb := ratelimit.New(10, 10)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Close() panicked on second call: %v", r)
		}
	}()
	tb.Close()
	tb.Close() // sync.Once — must not panic
}

func TestStartSweepAndClose(t *testing.T) {
	tb := ratelimit.New(10, 10)
	ctx, cancel := context.WithCancel(context.Background())
	tb.StartSweep(ctx)
	cancel() // stop the sweep goroutine via ctx

	// Also close explicitly — should stop the stopCh goroutine path
	if err := tb.Close(); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}
}

func BenchmarkTokenBucketAllow(b *testing.B) {
	tb := ratelimit.New(float64(b.N)+1, float64(b.N)+1)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tb.Allow(ctx, "bench-key")
	}
}

func BenchmarkTokenBucketAllowParallel(b *testing.B) {
	tb := ratelimit.New(float64(b.N)+1, float64(b.N)+1)
	ctx := context.Background()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tb.Allow(ctx, "bench-parallel-key")
		}
	})
}
