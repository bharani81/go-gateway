// Package proxy provides the per-service HTTP transport pool (TransportRegistry).
//
// A single shared http.Transport is a resource contention point when services
// have very different traffic profiles. TransportRegistry maintains one transport
// per logical service, configured with per-service concurrency settings.
package proxy

import (
	"net/http"
	"sync"
	"time"

	"github.com/bharanidharansrinivasan/api-gateway/internal/config"
)

// defaultTransportConfig is used when no per-service transport options are set.
var defaultTransportConfig = config.TransportConfig{
	MaxIdleConns:        100,
	MaxIdleConnsPerHost: 10,
	IdleConnTimeout:     90 * time.Second,
	TLSHandshakeTimeout: 10 * time.Second,
}

// TransportRegistry holds one *http.Transport per service and creates them on demand.
type TransportRegistry struct {
	mu         sync.RWMutex
	transports map[string]*http.Transport
}

// NewTransportRegistry initializes an empty registry.
func NewTransportRegistry() *TransportRegistry {
	return &TransportRegistry{transports: make(map[string]*http.Transport)}
}

// Register builds and stores a transport for the given service.
// Should be called once per service at gateway startup.
func (r *TransportRegistry) Register(serviceName string, cfg config.TransportConfig) {
	t := buildTransport(cfg)
	r.mu.Lock()
	r.transports[serviceName] = t
	r.mu.Unlock()
}

// Get returns the transport for the named service.
// Returns the default transport if the service has no specific configuration.
func (r *TransportRegistry) Get(serviceName string) http.RoundTripper {
	r.mu.RLock()
	t, ok := r.transports[serviceName]
	r.mu.RUnlock()
	if ok {
		return t
	}
	return buildTransport(defaultTransportConfig)
}

// buildTransport creates a tuned *http.Transport from the provided config.
func buildTransport(cfg config.TransportConfig) *http.Transport {
	idleConn := cfg.IdleConnTimeout
	if idleConn == 0 {
		idleConn = defaultTransportConfig.IdleConnTimeout
	}
	tlsTimeout := cfg.TLSHandshakeTimeout
	if tlsTimeout == 0 {
		tlsTimeout = defaultTransportConfig.TLSHandshakeTimeout
	}
	maxIdle := cfg.MaxIdleConns
	if maxIdle == 0 {
		maxIdle = defaultTransportConfig.MaxIdleConns
	}
	maxIdlePerHost := cfg.MaxIdleConnsPerHost
	if maxIdlePerHost == 0 {
		maxIdlePerHost = defaultTransportConfig.MaxIdleConnsPerHost
	}

	return &http.Transport{
		MaxIdleConns:        maxIdle,
		MaxIdleConnsPerHost: maxIdlePerHost,
		MaxConnsPerHost:     cfg.MaxConnsPerHost,
		IdleConnTimeout:     idleConn,
		TLSHandshakeTimeout: tlsTimeout,
		// ForceAttemptHTTP2 enables HTTP/2 when the upstream supports it.
		ForceAttemptHTTP2: true,
	}
}
