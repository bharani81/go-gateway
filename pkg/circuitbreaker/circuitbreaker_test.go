package circuitbreaker_test

import (
	"sync"
	"testing"
	"time"

	"github.com/bharanidharansrinivasan/api-gateway/pkg/circuitbreaker"
)

// newCB is a helper that creates a CB with a short reset timeout (100ms)
// so tests don't need to sleep 30 seconds.
func newCB(failThresh, succThresh int, resetTimeout time.Duration) *circuitbreaker.CircuitBreaker {
	return circuitbreaker.New(circuitbreaker.Config{
		FailureThreshold: failThresh,
		SuccessThreshold: succThresh,
		ResetTimeout:     resetTimeout,
	}, nil)
}

func TestClosedAllowsAll(t *testing.T) {
	cb := newCB(5, 2, 100*time.Millisecond)
	for i := 0; i < 100; i++ {
		if !cb.Allow() {
			t.Fatalf("iteration %d: expected Allow()=true in Closed state", i)
		}
	}
}

func TestInitialStateIsClosed(t *testing.T) {
	cb := newCB(5, 2, 100*time.Millisecond)
	if got := cb.State(); got != circuitbreaker.StateClosed {
		t.Fatalf("expected Closed, got %s", got)
	}
}

func TestTripsToOpenAfterThreshold(t *testing.T) {
	cb := newCB(3, 2, 100*time.Millisecond)
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != circuitbreaker.StateClosed {
		t.Fatal("should still be Closed before threshold")
	}
	cb.RecordFailure() // 3rd — should trip
	if cb.State() != circuitbreaker.StateOpen {
		t.Fatalf("expected Open after %d failures, got %s", 3, cb.State())
	}
}

func TestOpenRejectsAll(t *testing.T) {
	cb := newCB(1, 2, 10*time.Second) // long reset so we don't slip to HalfOpen
	cb.RecordFailure()                 // trips immediately
	for i := 0; i < 50; i++ {
		if cb.Allow() {
			t.Fatalf("iteration %d: expected Allow()=false in Open state", i)
		}
	}
}

func TestHalfOpenAfterResetTimeout(t *testing.T) {
	cb := newCB(1, 2, 50*time.Millisecond)
	cb.RecordFailure() // open
	time.Sleep(60 * time.Millisecond)
	if !cb.Allow() {
		t.Fatal("expected exactly one probe allowed after ResetTimeout")
	}
	if cb.State() != circuitbreaker.StateHalfOpen {
		t.Fatalf("expected HalfOpen, got %s", cb.State())
	}
}

func TestHalfOpenSecondRequestRejected(t *testing.T) {
	cb := newCB(1, 2, 50*time.Millisecond)
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)
	cb.Allow() // first probe consumed
	if cb.Allow() {
		t.Fatal("second request in HalfOpen should be rejected")
	}
}

func TestHalfOpenRecovery(t *testing.T) {
	cb := newCB(1, 2, 50*time.Millisecond)
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)
	cb.Allow() // probe
	cb.RecordSuccess()
	cb.RecordSuccess() // hit success threshold
	if cb.State() != circuitbreaker.StateClosed {
		t.Fatalf("expected Closed after recovery, got %s", cb.State())
	}
}

func TestHalfOpenProbeFailureGoesBackToOpen(t *testing.T) {
	cb := newCB(1, 2, 50*time.Millisecond)
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)
	cb.Allow() // probe
	cb.RecordFailure()
	if cb.State() != circuitbreaker.StateOpen {
		t.Fatalf("probe failure should re-open CB, got %s", cb.State())
	}
}

func TestSuccessInClosedResetsFailCount(t *testing.T) {
	cb := newCB(3, 2, 100*time.Millisecond)
	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess() // resets consecutive fail count
	cb.RecordFailure()
	cb.RecordFailure()
	// Only 2 more failures — should still be Closed (threshold is 3)
	if cb.State() != circuitbreaker.StateClosed {
		t.Fatalf("RecordSuccess should have reset fail counter, got %s", cb.State())
	}
}

func TestTransitionCallbackFired(t *testing.T) {
	var transitions []string
	cb := circuitbreaker.New(circuitbreaker.Config{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		ResetTimeout:     50 * time.Millisecond,
	}, func(from, to circuitbreaker.State) {
		transitions = append(transitions, from.String()+"->"+to.String())
	})

	cb.RecordFailure() // closed→open
	time.Sleep(60 * time.Millisecond)
	cb.Allow()         // open→half_open
	cb.RecordSuccess() // half_open→closed

	want := []string{"closed->open", "open->half_open", "half_open->closed"}
	if len(transitions) != len(want) {
		t.Fatalf("expected %d transitions, got %d: %v", len(want), len(transitions), transitions)
	}
	for i, w := range want {
		if transitions[i] != w {
			t.Errorf("transition[%d]: want %q, got %q", i, w, transitions[i])
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	cb := newCB(1000, 2, 10*time.Second) // high threshold so it doesn't trip prematurely
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				cb.Allow()
				cb.RecordSuccess()
				cb.RecordFailure()
			}
		}()
	}
	wg.Wait()
	// Just needs to not race — verified by -race flag
}

func TestStateStringValues(t *testing.T) {
	tests := []struct {
		state circuitbreaker.State
		want  string
	}{
		{circuitbreaker.StateClosed, "closed"},
		{circuitbreaker.StateOpen, "open"},
		{circuitbreaker.StateHalfOpen, "half_open"},
	}
	for _, tc := range tests {
		if got := tc.state.String(); got != tc.want {
			t.Errorf("State(%d).String() = %q, want %q", tc.state, got, tc.want)
		}
	}
}
