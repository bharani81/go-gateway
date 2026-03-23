// Package observability registers all Prometheus metrics for the gateway.
//
// Metrics are registered once at startup. Subsystems reference these variables
// directly rather than calling prometheus.MustRegister themselves, to ensure
// all metrics are declared in one place and make cardinality decisions visible.
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all gateway Prometheus instruments.
type Metrics struct {
	// Request metrics
	RequestsTotal    *prometheus.CounterVec
	RequestDuration  *prometheus.HistogramVec

	// Upstream metrics
	UpstreamDuration *prometheus.HistogramVec
	UpstreamErrors   *prometheus.CounterVec

	// Plugin metrics
	PluginExecutions *prometheus.CounterVec

	// Concurrency and load shedding
	ActiveRequests *prometheus.GaugeVec
	LoadShed       prometheus.Counter

	// Circuit breaker metrics
	CircuitBreakerState       *prometheus.GaugeVec
	CircuitBreakerTransitions *prometheus.CounterVec

	// Retry metrics
	RetriesTotal  *prometheus.CounterVec
	RetrySuccess  *prometheus.CounterVec

	// Rate limiting
	RateLimitHits *prometheus.CounterVec

	// Config reload metrics
	ConfigReloads         *prometheus.CounterVec
	ConfigReloadDuration  *prometheus.HistogramVec
}

// latencyBuckets matches the design document's recommended histogram buckets.
var latencyBuckets = []float64{
	0.005, 0.010, 0.025, 0.050, 0.100, 0.250, 0.500, 1.0, 2.5, 5.0, 10.0,
}

// NewMetrics creates and registers all gateway Prometheus metrics.
// promauto.With registers metrics with the provided registry (default: prometheus.DefaultRegisterer).
func NewMetrics(reg prometheus.Registerer) *Metrics {
	factory := promauto.With(reg)

	return &Metrics{
		RequestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total inbound requests handled by the gateway.",
		}, []string{"route", "method", "status"}),

		RequestDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gateway_request_duration_seconds",
			Help:    "End-to-end request latency including upstream and plugin time.",
			Buckets: latencyBuckets,
		}, []string{"route", "method", "status"}),

		UpstreamDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gateway_upstream_request_duration_seconds",
			Help:    "Upstream-only request latency (excludes plugin overhead).",
			Buckets: latencyBuckets,
		}, []string{"service", "instance"}),

		UpstreamErrors: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_upstream_errors_total",
			Help: "Total upstream errors by type (connection_error, 502, 503, 504).",
		}, []string{"service", "instance", "error_type"}),

		PluginExecutions: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_plugin_executions_total",
			Help: "Plugin execution outcomes (success, abort, error).",
		}, []string{"plugin", "result"}),

		ActiveRequests: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gateway_active_requests",
			Help: "Current number of in-flight requests.",
		}, []string{}),

		LoadShed: factory.NewCounter(prometheus.CounterOpts{
			Name: "gateway_load_shed_total",
			Help: "Total requests rejected by the load shedding semaphore.",
		}),

		CircuitBreakerState: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gateway_circuit_breaker_state",
			Help: "Current circuit breaker state: 0=closed, 1=half_open, 2=open.",
		}, []string{"service", "instance"}),

		CircuitBreakerTransitions: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_circuit_breaker_transitions_total",
			Help: "Total circuit breaker state transitions.",
		}, []string{"service", "instance", "from", "to"}),

		RetriesTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_retries_total",
			Help: "Total retry attempts by reason (connection_error, upstream_502, etc.).",
		}, []string{"route", "service", "reason"}),

		RetrySuccess: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_retry_success_total",
			Help: "Retries that ultimately produced a successful upstream response.",
		}, []string{"route", "service"}),

		RateLimitHits: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_rate_limit_hits_total",
			Help: "Total requests rejected by rate limiting.",
		}, []string{"route", "strategy"}),

		ConfigReloads: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_config_reload_total",
			Help: "Config reload outcomes: success, validation_error, io_error.",
		}, []string{"result"}),

		ConfigReloadDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gateway_config_reload_duration_seconds",
			Help:    "Time taken to read, parse, and validate the config file.",
			Buckets: prometheus.DefBuckets,
		}, []string{"result"}),
	}
}
