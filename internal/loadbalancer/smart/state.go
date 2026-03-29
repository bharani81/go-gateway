package smart

import (
	"math"
	"sync/atomic"
	"time"
)

// InstanceState maintains the lock-free feedback metrics for a single upstream instance.
// Using EWMA (Exponentially Weighted Moving Average) ensures we track recent trends
// without keeping expensive sliding windows.
type InstanceState struct {
	// inflight tracks currently active requests assigned to this instance.
	inflight atomic.Int64

	// ewmaLatency tracks the exponential weighted moving average of latency in milliseconds.
	// Stored as uint64 bits since atomic operations on floats are not natively supported in
	// earlier Go versions, although Go 1.19+ has atomic.Uint64 which we use here via Float64bits.
	ewmaLatency atomic.Uint64

	// ewmaErrorRate tracks exponential moving average of error probability [0.0, 1.0].
	ewmaErrorRate atomic.Uint64
}

// NewInstanceState initializes starting state for an instance.
// Initializing with slightly optimistic defaults encourages exploration.
func NewInstanceState() *InstanceState {
	s := &InstanceState{}
	s.StoreLatency(50.0) // Assume a baseline 50ms to start
	s.StoreErrorRate(0.0)
	return s
}

func (s *InstanceState) LoadLatency() float64 {
	return math.Float64frombits(s.ewmaLatency.Load())
}

func (s *InstanceState) StoreLatency(lat float64) {
	s.ewmaLatency.Store(math.Float64bits(lat))
}

func (s *InstanceState) LoadErrorRate() float64 {
	return math.Float64frombits(s.ewmaErrorRate.Load())
}

func (s *InstanceState) StoreErrorRate(e float64) {
	s.ewmaErrorRate.Store(math.Float64bits(e))
}

func (s *InstanceState) Inflight() int64 {
	return s.inflight.Load()
}

// Record updates the internal EWMA models based on a completed request.
// alpha controls the weight of the newest data point vs historical.
// High alpha = reactive (noisy), Low alpha = stable (slow to respond to change).
func (s *InstanceState) Record(latency time.Duration, isErr bool, latAlpha, errAlpha float64) {
	s.inflight.Add(-1)

	// Update Latency EWMA
	latMs := float64(latency.Milliseconds())
	oldLat := s.LoadLatency()
	newLat := (latAlpha * latMs) + ((1.0 - latAlpha) * oldLat)
	s.StoreLatency(newLat)

	// Update Error EWMA (1.0 = error, 0.0 = success)
	errVal := 0.0
	if isErr {
		errVal = 1.0
	}
	oldErr := s.LoadErrorRate()
	newErr := (errAlpha * errVal) + ((1.0 - errAlpha) * oldErr)
	s.StoreErrorRate(newErr)
}

// RecordStart increments the inflight requests.
func (s *InstanceState) RecordStart() {
	s.inflight.Add(1)
}
