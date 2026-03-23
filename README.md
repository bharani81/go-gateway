# API Gateway — Production-grade Layer 7 Gateway in Go

A fully-featured, production-ready API Gateway built with Go. Designed for backend engineers who want to understand how systems like Kong, Envoy, and KrakenD work under the hood — and build one from scratch.

> **Recruiter note:** This project was designed and implemented top-down from a full architecture document before writing a single line of code. The architecture document lives in `docs/design/`. The commit history shows an incremental, test-driven-ready build sequence.

---

## Table of Contents

1. [What This Does](#what-this-does)
2. [High-Level Architecture](#high-level-architecture)
3. [Request Lifecycle](#request-lifecycle)
4. [Component Breakdown](#component-breakdown)
5. [Plugin System](#plugin-system)
6. [Circuit Breaker Design](#circuit-breaker-design)
7. [Rate Limiting Design](#rate-limiting-design)
8. [Configuration Reference](#configuration-reference)
9. [Getting Started](#getting-started)
10. [Running Locally with Docker](#running-locally-with-docker)
11. [Project Structure](#project-structure)
12. [Key Engineering Decisions](#key-engineering-decisions)
13. [Observability](#observability)
14. [Roadmap](#roadmap)

---

## What This Does

This gateway sits between external clients and your upstream microservices and provides:

| Feature | Implementation |
|---------|---------------|
| **Reverse proxying** | Streams requests and responses without buffering |
| **Config-driven routing** | Exact / prefix / regex path matching, hot-reload without restart |
| **JWT authentication** | HS256/RS256, 3 auth models (validate/passthrough/hybrid) |
| **Rate limiting** | Token bucket, per-IP and per-user, lazy TTL eviction |
| **Circuit breaker** | Two-level (service + per-instance), CB state in every access log |
| **Load balancing** | Round-robin (atomic counter) and random |
| **Active health checks** | HTTP GET, configurable interval/thresholds |
| **Plugin system** | Config-ordered, no Priority() footgun |
| **Observability** | Prometheus metrics, structured JSON logs (zap), W3C traceparent |
| **Load shedding** | Max concurrent requests semaphore, reject strategy |
| **Graceful shutdown** | 30-second drain window, SIGTERM/SIGINT handled |

---

## High-Level Architecture

```
                         ┌─────────────────────────────────────────────────┐
                         │              API GATEWAY PROCESS                │
                         │                                                 │
 ┌──────────┐  HTTPS     │  ┌──────────┐   ┌──────────┐  ┌────────────┐  │
 │  Client  │──────────► │  │  Server  │──►│  Router  │─►│  Plugin    │  │
 │(browser/ │            │  │  (8080)  │   │ (trie +  │  │  Chain     │  │
 │ mobile)  │            │  └──────────┘   │  regex)  │  │ (ordered)  │  │
 └──────────┘            │       │         └──────────┘  └────────────┘  │
                         │       │                              │          │
                         │  Load Shed                     ┌─────▼──────┐  │
                         │  Semaphore                     │  Reverse   │  │
                         │                                │   Proxy    │  │
                         │  ┌─────────────────────────┐  │ (+ retry)  │  │
                         │  │   GatewayContext         │  └────────────┘  │
                         │  │  ┌──────────────┐        │        │          │
                         │  │  │ RequestID    │        │  ┌─────▼──────┐  │  HTTP
                         │  │  │ TraceID      │        │  │  Load      │──┼──────►  Upstream
                         │  │  │ RetryCount   │        │  │  Balancer  │  │         Services
                         │  │  │ CircuitState │        │  └────────────┘  │
                         │  │  │ FailureReason│        │        │          │
                         │  │  └──────────────┘        │  ┌─────▼──────┐  │
                         │  └─────────────────────────┘  │  Circuit   │  │
                         │                                │  Breaker   │  │
                         │  ┌──────────────────────────┐  │(svc+inst.) │  │
                         │  │  Metrics Server (9090)   │  └────────────┘  │
                         │  │  /metrics  /healthz       │                  │
                         │  └──────────────────────────┘                  │
                         └─────────────────────────────────────────────────┘
```

---

## Request Lifecycle

Every HTTP request passes through these stages in order. Any stage can short-circuit the pipeline with an error response:

```
CLIENT REQUEST
     │
     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  1. LOAD SHEDDING                                                        │
│     Acquire semaphore slot (MaxConcurrentRequests).                      │
│     Full → 503 + Retry-After: 1.  Reject (not queue). Release on return │
└─────────────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  2. PANIC RECOVERY MIDDLEWARE                                            │
│     defer/recover around inner handler.  Any panic → log stack → 500.   │
└─────────────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  3. CONTEXT INJECTION MIDDLEWARE                                         │
│     Generate RequestID (UUID v4) and TraceID (from traceparent or new). │
│     Build GatewayContext, store in context.Context as single typed key. │
└─────────────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  4. TIMEOUT MIDDLEWARE                                                   │
│     context.WithDeadline(globalTimeout).  Propagates to upstream calls. │
│     Per-route override applied later.                                    │
└─────────────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  5. HEADER STRIP                                                         │
│     Strip all X-Forwarded-*, X-Real-IP, hop-by-hop headers.            │
│     Clients must NOT be able to inject forwarding headers.              │
└─────────────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  6. ROUTER MATCH                                                         │
│     Priority: exact (O(1) map) → prefix (longest-match sorted slice)    │
│             → regex (linear scan, max 20 routes enforced at startup).   │
│     No match → 404.                                                      │
└─────────────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  7. BODY SIZE LIMIT                                                      │
│     http.MaxBytesReader wraps r.Body BEFORE any plugin reads it.        │
│     Oversized body → 413 immediately. Prevents OOM before auth runs.    │
└─────────────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  8. PRE-REQUEST PLUGIN CHAIN                          FAIL-FAST         │
│     Plugins run in config-defined order (order field, no Priority()).    │
│     AbortError from any plugin → write error → stop chain.              │
│     Panic in plugin → recover → 500 → stop chain.                       │
│     Typical order: Logger → CORS → RateLimit → JWTAuth                  │
└─────────────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  9. CIRCUIT BREAKER CHECK                                                │
│     Service-level CB: Open → 503 immediately (no LB, no retry).         │
│     Per-instance CB: Open instances skipped by load balancer.           │
└─────────────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│ 10. LOAD BALANCER                                                        │
│     Select next healthy instance that is NOT skipped by per-instance CB │
│     Strategy: round-robin (atomic counter) or random.                   │
└─────────────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│ 11. PROXY TO UPSTREAM  (with retry loop)                                 │
│     Set X-Forwarded-For/Host/Proto authoritatively.                      │
│     Stream response body directly — never buffer full response.         │
│     On 502/503/504 or connection error (idempotent requests only):      │
│       - Record CB failure                                                │
│       - Increment RetryCount on GatewayContext                          │
│       - Wait exp. backoff + jitter (0ms, 100ms + rand[0,50ms])          │
│       - Check deadline: if < 200ms remaining → 504, stop                │
│       - Select next instance, check CB again                            │
│     Streaming started → no retry; log failure_reason=streaming_truncated│
└─────────────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│ 12. POST-RESPONSE PLUGIN CHAIN                    CONTINUE-ON-ERROR     │
│     Errors logged as WARN; headers may already be sent so cannot abort. │
│     Typical uses: add CORS headers, log access entry with all fields.   │
└─────────────────────────────────────────────────────────────────────────┘
     │
     ▼
CLIENT RESPONSE
```

---

## Component Breakdown

### Router (`internal/router/`)

Three-tier matching with deterministic priority:

| Tier | Data structure | Complexity | When to use |
|------|----------------|------------|-------------|
| Exact | `map[method:path]*Route` | O(1) | Specific endpoints like `/healthz` |
| Prefix | `[]*Route` sorted by length desc | O(routes) | API prefixes like `/api/v1/` |
| Regex | `[]*Route` linear scan | O(routes) | Complex patterns — max 20 enforced |

### Service Registry (`internal/registry/`)

Tracks instance health using a dual approach:
- **Active checks**: background HTTP GET goroutine per service.
- **Passive checks**: proxy failures call `RecordFailure()` immediately.

Both update the same `consecutiveFails` counter under a per-instance mutex. No global lock.

### Circuit Breaker (`pkg/circuitbreaker/`)

```
                     failure_threshold
Closed ─────────────────────────────────► Open
  ▲                                         │
  │ success_threshold                       │ after reset_timeout
  │                                         ▼
  └──────────────────────────────────── Half-Open
                                    (1 probe allowed)
```

**Two levels:**
- **Service-level**: one per logical service (`user-service`). Acts as a kill switch.
- **Per-instance**: one per pod (`user-svc-1`). Isolates individual bad replicas without tripping the service-level breaker.

### Load Balancer (`internal/loadbalancer/`)

The `isSkipped` predicate is injected into every `Next()` call, so the circuit breaker can mark instances as skipped without the LB package knowing about circuit breakers. Clean separation of concerns.

```go
// The LB never imports the circuitbreaker package.
lb.Next(instances, func(inst *registry.Instance) bool {
    return instanceCBs[inst.ID].State() == circuitbreaker.StateOpen
})
```

### GatewayContext (`pkg/gwctx/`)

Single struct stored under one typed context key. All subsystems call `gwctx.From(ctx)`:

```go
type GatewayContext struct {
    RequestID    string         // log correlation
    TraceID      string         // W3C distributed tracing
    ClientIP     net.IP         // for rate limiting
    StartTime    time.Time      // for latency calculation
    RouteID      string         // for per-route metrics
    AuthClaims   map[string]any // from JWT plugin
    UserID       string         // for per-user rate limiting
    CircuitState CircuitState   // snapshot for access log
    RetryCount   int            // incremented per retry attempt
    FailureReason string        // written by any failure path
}
```

---

## Plugin System

### Interface

```go
type Plugin interface {
    Name() string
    ExecuteRequest(w http.ResponseWriter, r *http.Request) error
    ExecuteResponse(w http.ResponseWriter, r *http.Request) error
}
```

**Why no `Priority()` method?**
A `Priority()` method creates hidden ordering conflicts: two plugins both return priority 5 and the behaviour is undefined. `order` in the route config is explicit and validated at startup — duplicate orders are a validation error, not a silent bug.

### Built-in Plugins

| Plugin | Pre-request | Post-response |
|--------|-------------|---------------|
| `builtin.logger` | Log inbound fields | Log access log with all GatewayContext fields |
| `builtin.cors` | Handle OPTIONS preflight (abort chain), validate Origin | Inject CORS headers |
| `builtin.jwt-auth` | Validate signature + claims, inject claims into context | No-op |
| `builtin.rate-limit` | Check token bucket, set X-RateLimit-Remaining | No-op |

### Auth Models (JWT Plugin)

| Model | Gateway behaviour | Use case |
|-------|-------------------|----------|
| `validate` | Full signature + claim validation. Forward decoded claims. | Gateway-enforced auth |
| `passthrough` | Forward Authorization header unchanged | Upstream handles validation |
| `hybrid` | Validate signature + exp/iss/aud. Forward decoded claims. | Common production pattern |

> **Important:** The gateway NEVER accesses a user database. It only verifies cryptographic signatures and standard claims. Identity is asserted by the token issuer.

---

## Circuit Breaker Design

```
STATE MACHINE (per service and per instance):

Closed ──── 5 consecutive failures ────► Open
  ▲                                        │
  │                                        │ 30 seconds
  │                                        ▼
  └─── 2 consecutive successes ────── Half-Open
                                     (1 probe request)
```

**Integration with the proxy retry loop:**

```
For each request:
  1. Check service-level CB → Open? → 503 immediately
  2. LB.Next() with isSkipped = (instance CB state == Open)
  3. No eligible instance? → Trip service CB → 503
  4. Proxy call fails? → RecordFailure on instance CB
     → Retry if idempotent and deadline allows
  5. Proxy call succeeds? → RecordSuccess on instance CB + service CB
```

> **v1 limitation:** Circuit breaker state is in-memory per gateway replica. Pod A's CB may be Open while Pod B's is Closed. Active health checks compensate: unhealthy pods are eventually removed from the LB pool regardless of CB state. Distributed CB via Redis is a v2 item.

---

## Rate Limiting Design

**Algorithm:** Token bucket — allows short bursts up to `burst` capacity, refills at `rate` tokens/second.

**Memory management:** Two-level eviction to prevent `sync.Map` from growing unbounded:
1. **Lazy eviction on read:** if bucket hasn't been touched in `eviction_ttl` (10m), treat as fresh.
2. **Periodic sweep (every 5m):** delete all buckets last-touched > eviction_ttl ago.

**High cardinality warning:** Per-user rate limiting with UUID user IDs can produce millions of unique keys. At scale (>1M unique users/day), switch to Redis-backed rate limiting.

---

## Configuration Reference

```yaml
gateway:
  port: 8080
  global_timeout: 30s
  max_concurrent_requests: 1000   # load shedding threshold

services:
  - name: my-service
    lb_strategy: round-robin       # "round-robin" | "random"
    health_check:
      path: /health
      interval: 10s
      failure_threshold: 3
      success_threshold: 2
    transport:                      # per-service connection pool tuning
      max_idle_conns_per_host: 20
    instances:
      - id: pod-1
        address: "10.0.0.5:8080"

plugins:
  - name: my-auth                  # logical name used in routes
    type: builtin.jwt-auth         # registered factory type

routes:
  - id: get-products
    methods: [GET]
    path: /api/v1/products
    match_type: prefix             # "exact" | "prefix" | "regex"
    service: my-service
    timeout: 10s
    max_body_bytes: 1048576        # 1MB — enforced before plugins run
    plugins:
      - name: my-auth
        order: 1                   # lower = runs first; duplicates are an error
        config:
          auth_model: hybrid
          secret: "${JWT_SECRET}"  # injected from environment
```

---

## Getting Started

### Prerequisites

- Go 1.22+
- Docker + Docker Compose (for local development)

### Build & Run Locally (without Docker)

```bash
# 1. Clone the repo
git clone https://github.com/bharanidharansrinivasan/api-gateway.git
cd api-gateway

# 2. Download dependencies
go mod download

# 3. Set required environment variables
export JWT_SECRET=my-dev-secret

# 4. Run the gateway
go run ./cmd/gateway --config configs/gateway.yaml --log-level debug

# Gateway    → http://localhost:8080
# Metrics    → http://localhost:9090/metrics
# Healthz    → http://localhost:9090/healthz
```

### Test the gateway

```bash
# Health check
curl http://localhost:9090/healthz

# Send a request (will route to user-service)
curl -H "Authorization: Bearer <jwt>" http://localhost:8080/api/v1/users

# Check metrics
curl http://localhost:9090/metrics | grep gateway_
```

---

## Running Locally with Docker

```bash
cd deployments

# Build and start all services (gateway + mock upstreams + prometheus)
export JWT_SECRET=dev-secret
docker compose up --build

# Tail logs
docker compose logs -f gateway

# Stop everything
docker compose down
```

**Hot reload:** edit `configs/gateway.yaml` while the gateway is running. Changes are picked up automatically within seconds via fsnotify. Invalid configs are rejected — the gateway keeps serving with the last valid config.

---

## Project Structure

```
api-gateway/
├── cmd/
│   └── gateway/
│       └── main.go              # Entry point: DI wiring + server start
│
├── internal/                    # Core code — not importable externally
│   ├── config/
│   │   ├── config.go            # Typed config structs
│   │   ├── loader.go            # YAML loading + atomic hot reload
│   │   └── validator.go         # Two-phase validation (schema + refs)
│   │
│   ├── router/
│   │   └── router.go            # Three-tier router (exact/prefix/regex)
│   │
│   ├── registry/
│   │   ├── registry.go          # Service registry + Instance health tracking
│   │   └── healthcheck.go       # Background HTTP health check goroutines
│   │
│   ├── loadbalancer/
│   │   ├── loadbalancer.go      # LoadBalancer interface
│   │   ├── roundrobin.go        # Atomic round-robin
│   │   └── random.go            # Random selection
│   │
│   ├── plugin/
│   │   ├── plugin.go            # Plugin interface + AbortError
│   │   ├── registry.go          # Factory registry
│   │   ├── chain.go             # Ordered chain executor
│   │   └── builtin/
│   │       ├── logger.go        # Structured access logging plugin
│   │       ├── cors.go          # CORS preflight + response headers
│   │       ├── jwt.go           # JWT validation (3 auth models)
│   │       └── ratelimit.go     # Token bucket (per-IP, per-user)
│   │
│   ├── proxy/
│   │   ├── headers.go           # Hop-by-hop stripping, X-Forwarded-* injection
│   │   ├── transport.go         # Per-service HTTP transport pool
│   │   └── proxy.go             # Reverse proxy with retry + CB integration
│   │
│   ├── middleware/
│   │   ├── recovery.go          # Panic recovery → 500
│   │   ├── context.go           # GatewayContext injection + W3C traceparent
│   │   └── timeout.go           # Per-request context deadline
│   │
│   ├── observability/
│   │   ├── logger.go            # Zap logger factory (JSON, RFC3339Nano)
│   │   └── metrics.go           # Prometheus metrics declarations
│   │
│   └── server/
│       ├── server.go            # HTTP server + graceful shutdown + load shedding
│       └── handler.go           # Main request pipeline dispatching
│
├── pkg/                         # Reusable packages (safe for external import)
│   ├── gwctx/
│   │   └── gwctx.go             # GatewayContext struct + context helpers
│   ├── circuitbreaker/
│   │   └── circuitbreaker.go    # Generic two-state circuit breaker
│   └── ratelimit/
│       └── tokenbucket.go       # Token bucket with lazy TTL eviction
│
├── configs/
│   └── gateway.yaml             # Annotated example configuration
│
├── deployments/
│   ├── Dockerfile               # Multi-stage build → distroless/nonroot
│   └── docker-compose.yml       # Gateway + mock upstreams + Prometheus
│
├── docs/
│   └── design/                  # Architecture documents (Parts 1–3)
│
├── go.mod
└── go.sum
```

---

## Key Engineering Decisions

| Decision | Rationale |
|----------|-----------|
| `GatewayContext` struct instead of scattered context keys | One typed key, one struct — all scattered values cause silent naming collisions and hard-to-test code |
| No `Priority()` on plugin interface | Config-driven `order` is explicit; a `Priority()` method creates hidden ordering conflicts with no compile-time detection |
| Reject (not queue) for load shedding | Queuing adds latency to ALL requests when capacity is hit; rejecting fast sends clear backpressure to the upstream LB |
| Per-service `http.Transport` pools | A shared pool lets one high-traffic service monopolize connections, starving others |
| Lazy TTL eviction + periodic sweep for rate limiter | Lazy eviction handles 99% of cases with zero overhead; the sweep catches stragglers |
| Two-level circuit breaker | Service-level CB = kill switch for total service failure; per-instance CB = isolates one bad pod without affecting the rest |
| atomic counter for round-robin | No mutex on the hot path; each goroutine gets a unique counter value via `AddUint64` |
| Distroless final image | No shell = no shell injection risk; no package manager = minimal attack surface |
| `context.WithDeadline` over `http.TimeoutHandler` | Deadline propagates to upstream HTTP calls via `req.WithContext(ctx)` — cancels transport-level I/O automatically |

---

## Observability

### Access Log (one JSON line per request)

```json
{
  "ts": "2026-03-23T22:00:00Z",
  "level": "info",
  "event": "request.complete",
  "request_id": "550e8400-e29b-41d4-a716-446655440000",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "method": "GET",
  "path": "/api/v1/users",
  "route_id": "get-users",
  "status": 200,
  "latency_ms": 47,
  "upstream": "10.0.0.5:8081",
  "upstream_latency_ms": 42,
  "client_ip": "203.0.113.1",
  "user_id": "usr_abc123",
  "retry_count": 1,
  "circuit_state": "closed",
  "failure_reason": ""
}
```

### Prometheus Metrics (`/metrics` on :9090)

| Metric | Type | Key labels |
|--------|------|------------|
| `gateway_requests_total` | Counter | route, method, status |
| `gateway_request_duration_seconds` | Histogram | route, method, status |
| `gateway_retries_total` | Counter | route, service, reason |
| `gateway_circuit_breaker_transitions_total` | Counter | service, instance, from, to |
| `gateway_load_shed_total` | Counter | — |
| `gateway_config_reload_total` | Counter | result |

> **Cardinality warning:** Do NOT add `trace_id` as a Prometheus label — it creates millions of unique label combinations and destroys query performance. Use exemplars to link histogram observations to trace IDs.

---

## Roadmap

| Feature | Priority | Notes |
|---------|----------|-------|
| Distributed rate limiting (Redis) | High | Required for multi-replica deployments |
| Distributed circuit breaker | Medium | Redis-backed with Lua atomic CAS |
| Admin API (REST) | Medium | Dynamic route/plugin management without file edits |
| Service discovery (Consul/k8s Endpoints) | Medium | Replace static instance lists |
| OpenTelemetry traces (full) | Medium | With sampling, OTLP export to Jaeger/Tempo |
| WebSocket proxy | Low | Requires connection tunneling |
| gRPC proxy | Low | Requires HTTP/2 + binary protocol awareness |
| Response caching | Low | Redis-backed, GET routes only |

---

## Contributing

1. Read the [architecture document](docs/design/) before making significant changes.
2. All config changes must update `internal/config/validator.go`.
3. New plugins must implement the `plugin.Plugin` interface exactly — no `Priority()`.
4. Use `gwctx.From(ctx)` to access request metadata; never add a new context key.

---

*Built with Go 1.22 · zap · Prometheus · golang-jwt · fsnotify*
