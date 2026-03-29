package smart

import (
	"math/rand"
	"time"

	"github.com/bharanidharansrinivasan/api-gateway/internal/loadbalancer"
	"github.com/bharanidharansrinivasan/api-gateway/internal/observability"
	"github.com/bharanidharansrinivasan/api-gateway/internal/registry"
)

// Weights configure the influence of each metric in the smart scoring algorithm.
type Weights struct {
	Latency float64
	Errors  float64
	Load    float64
}

// Config provides tuning boundaries for the AI load balancing algorithm.
type Config struct {
	Weights              Weights
	MaxTolerableLatency  float64 // In milliseconds, latencies above this score 0
	MaxConcurrentRequest float64 // Beyond this load, instance scores 0 for load feature
	ExplorationRate      float64 // Epsilon-greedy chance (0.0 to 1.0) to route randomly
	LatencyAlpha         float64 // EWMA weight for new latencies
	ErrorAlpha           float64 // EWMA weight for new errors
}

// DefaultConfig provides sensible defaults.
func DefaultConfig() Config {
	return Config{
		Weights: Weights{
			Latency: 0.3,
			Errors:  0.5,
			Load:    0.2,
		},
		MaxTolerableLatency:  1000.0, // 1 second
		MaxConcurrentRequest: 200.0,
		ExplorationRate:      0.10, // 10% of traffic tests random nodes
		LatencyAlpha:         0.1,  // Slow adjustments to latency
		ErrorAlpha:           0.2,  // Faster adjustments to errors
	}
}

// Balancer implements a Multi-Armed Bandit adaptive routing heuristic.
type Balancer struct {
	cfg     Config
	states  map[string]*InstanceState
	metrics *observability.Metrics
	service string
}

// New returns a newly configured smart Balancer.
// Since LoadBalancers are regenerated per gateway reload cycle, the map of instances is fixed initially.
func New(cfg Config, instances []*registry.Instance, metrics *observability.Metrics, service string) *Balancer {
	sb := &Balancer{
		cfg:     cfg,
		states:  make(map[string]*InstanceState, len(instances)),
		metrics: metrics,
		service: service,
	}
	for _, inst := range instances {
		sb.states[inst.ID] = NewInstanceState()
	}
	return sb
}

// Next selects the most optimal instance probabilistically (Roulette Wheel selection)
// based on realtime feedback telemetry normalized mathematically.
func (sb *Balancer) Next(instances []*registry.Instance, isSkipped func(*registry.Instance) bool) (*registry.Instance, func(time.Duration, bool), error) {
	eligible := make([]*registry.Instance, 0, len(instances))
	for _, inst := range instances {
		if inst.IsHealthy() && (isSkipped == nil || !isSkipped(inst)) {
			eligible = append(eligible, inst)
		}
	}

	if len(eligible) == 0 {
		return nil, nil, loadbalancer.ErrNoHealthyInstances
	}

	// 1. Check for Epsilon-Greedy Exploration trigger
	if rand.Float64() < sb.cfg.ExplorationRate || len(eligible) == 1 {
		// Exploitation skipped: purely randomly select an instance to keep "unattractive" nodes monitored over time.
		selected := eligible[rand.Intn(len(eligible))]
		if sb.metrics != nil {
			sb.metrics.SmartLBRoutingDecision.WithLabelValues(sb.service, selected.ID, "explored").Inc()
		}
		
		state := sb.getState(selected)
		state.RecordStart()
		done := func(lat time.Duration, err bool) {
			state.Record(lat, err, sb.cfg.LatencyAlpha, sb.cfg.ErrorAlpha)
		}
		return selected, done, nil
	}

	// 2. Exploit Phase: Calculate Scores
	totalScore := 0.0
	scores := make([]float64, len(eligible))

	for i, inst := range eligible {
		state := sb.getState(inst)
		
		latMs := state.LoadLatency()
		errRate := state.LoadErrorRate()
		inflight := float64(state.Inflight())

		// Normalization [0.0, 1.0]
		// Higher means better performance

		// Latency Score: 1.0 is 0ms, 0.0 is >= MaxTolerableLatency
		latScore := 1.0 - (latMs / sb.cfg.MaxTolerableLatency)
		if latScore < 0 {
			latScore = 0
		}

		// Error Score: 1.0 is 0% errors, 0.0 is 100% errors
		errScore := 1.0 - errRate
		if errScore < 0 {
			errScore = 0
		}

		// Load Score: 1.0 is 0 inflight, 0.0 is >= MaxConcurrentRequests
		loadScore := 1.0 - (inflight / sb.cfg.MaxConcurrentRequest)
		if loadScore < 0 {
			loadScore = 0
		}

		// Weighted Heuristic Linear Model
		score := (sb.cfg.Weights.Latency * latScore) +
			(sb.cfg.Weights.Errors * errScore) +
			(sb.cfg.Weights.Load * loadScore)

		// Ensures baseline minimum probability to prevent strict 0 starvation
		if score <= 0.01 {
			score = 0.01
		}

		if sb.metrics != nil {
			sb.metrics.SmartLBInstanceScore.WithLabelValues(sb.service, inst.ID).Set(score)
		}

		scores[i] = score
		totalScore += score
	}

	// 3. Selection Phase: Roulette Wheel (Weighted Random Distribution)
	// Example: A has score 0.8, B has 0.4. A is selected exactly twice as often statistically, smoothly dissipating load.
	target := rand.Float64() * totalScore
	accum := 0.0
	var selected *registry.Instance

	for i, inst := range eligible {
		accum += scores[i]
		if accum >= target {
			selected = inst
			break
		}
	}

	// Fallback due to decimal precision rounding
	if selected == nil {
		selected = eligible[len(eligible)-1]
	}

	if sb.metrics != nil {
		sb.metrics.SmartLBRoutingDecision.WithLabelValues(sb.service, selected.ID, "exploited").Inc()
	}

	// 4. Hook Telemetry Feedback Loop
	state := sb.getState(selected)
	state.RecordStart()

	done := func(lat time.Duration, isErr bool) {
		state.Record(lat, isErr, sb.cfg.LatencyAlpha, sb.cfg.ErrorAlpha)
	}

	return selected, done, nil
}

func (sb *Balancer) getState(inst *registry.Instance) *InstanceState {
	s, ok := sb.states[inst.ID]
	if !ok {
		// In case instances list changed drastically dynamically. 
		// Unlikely due to reload lifecycle overriding balancers per shift, but defensive.
		s = NewInstanceState()
		sb.states[inst.ID] = s
	}
	return s
}
