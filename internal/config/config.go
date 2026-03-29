// Package config defines the typed configuration structures for the API Gateway.
// All subsystems receive their configuration as typed structs, never as raw maps.
package config

import "time"

// Config is the root configuration structure loaded from gateway.yaml.
type Config struct {
	Gateway         GatewayConfig         `yaml:"gateway"`
	Services        []ServiceConfig        `yaml:"services"`
	Routes          []RouteConfig          `yaml:"routes"`
	Plugins         []PluginDef            `yaml:"plugins"`
	ExternalPlugins []ExternalPluginConfig `yaml:"external_plugins"`
}

// GatewayConfig holds server-level settings.
type GatewayConfig struct {
	Port          int            `yaml:"port"`
	TLS           TLSConfig      `yaml:"tls"`
	Timeouts      TimeoutConfig  `yaml:"timeouts"`
	GlobalTimeout time.Duration  `yaml:"global_timeout"`
	MaxConcurrent int            `yaml:"max_concurrent_requests"`
}

// TLSConfig holds TLS certificate configuration.
type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// TimeoutConfig maps to Go's http.Server timeout fields.
type TimeoutConfig struct {
	ReadHeader time.Duration `yaml:"read_header"`
	Read       time.Duration `yaml:"read"`
	Write      time.Duration `yaml:"write"`
	Idle       time.Duration `yaml:"idle"`
}

// ServiceConfig defines a logical upstream service and its instances.
type ServiceConfig struct {
	Name         string             `yaml:"name"`
	LBStrategy   string             `yaml:"lb_strategy"` // round-robin | random | smart
	SmartRouting SmartRoutingConfig `yaml:"smart_routing"`
	HealthCheck  HealthCheckConfig  `yaml:"health_check"`
	Instances   []InstanceConfig  `yaml:"instances"`
	Transport   TransportConfig   `yaml:"transport"`
}

// TransportConfig allows per-service connection pool tuning.
type TransportConfig struct {
	MaxIdleConns        int           `yaml:"max_idle_conns"`
	MaxIdleConnsPerHost int           `yaml:"max_idle_conns_per_host"`
	MaxConnsPerHost     int           `yaml:"max_conns_per_host"`
	IdleConnTimeout     time.Duration `yaml:"idle_conn_timeout"`
	TLSHandshakeTimeout time.Duration `yaml:"tls_handshake_timeout"`
}

// HealthCheckConfig controls active health checking behaviour.
type HealthCheckConfig struct {
	Path             string        `yaml:"path"`
	Interval         time.Duration `yaml:"interval"`
	Timeout          time.Duration `yaml:"timeout"`
	FailureThreshold int           `yaml:"failure_threshold"`
	SuccessThreshold int           `yaml:"success_threshold"`
}

// InstanceConfig is a single upstream host:port pair.
type InstanceConfig struct {
	ID      string `yaml:"id"`
	Address string `yaml:"address"` // "host:port"
	Weight  int    `yaml:"weight"`
}

// RouteConfig maps an inbound path pattern to a service and its plugin list.
type RouteConfig struct {
	ID           string        `yaml:"id"`
	Methods      []string      `yaml:"methods"`
	Path         string        `yaml:"path"`
	MatchType    string        `yaml:"match_type"`    // exact | prefix | regex
	StripPrefix  bool          `yaml:"strip_prefix"`
	Service      string        `yaml:"service"`
	Timeout      time.Duration `yaml:"timeout"`
	MaxBodyBytes int64         `yaml:"max_body_bytes"`
	Plugins      []PluginRef   `yaml:"plugins"`
}

// PluginRef is an ordered reference to a plugin within a route.
type PluginRef struct {
	Name   string                 `yaml:"name"`
	Order  int                    `yaml:"order"`
	Config map[string]interface{} `yaml:"config"`
}

// PluginDef registers a named plugin type available to all routes.
type PluginDef struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"` // e.g., "builtin.logger"
}

// ExternalPluginConfig is the per-plugin configuration for HTTP sidecar plugins.
type ExternalPluginConfig struct {
	Name           string        `yaml:"name"`
	Address        string        `yaml:"address"`         // "http://localhost:8082" or "unix:///tmp/plugin.sock"
	Timeout        time.Duration `yaml:"timeout"`         // default 50ms
	OnError        string        `yaml:"on_error"`        // "pass" | "reject" | "circuit-break"
	HealthPath     string        `yaml:"health_path"`     // default "/healthz"
	HealthInterval time.Duration `yaml:"health_interval"` // default 10s
}

// SmartRoutingConfig configures the Multi-Armed Bandit load balancer parameters.
type SmartRoutingConfig struct {
	ExplorationRate      float64       `yaml:"exploration_rate"`
	MaxTolerableLatency  time.Duration `yaml:"max_tolerable_latency"`
	MaxConcurrentRequest float64       `yaml:"max_concurrent_requests"`
	Weights              struct {
		Latency float64 `yaml:"latency"`
		Errors  float64 `yaml:"errors"`
		Load    float64 `yaml:"load"`
	} `yaml:"weights"`
}
