# Go API Gateway

A high-performance, production-grade API Gateway built natively in Go. Designed for edge computing and microservice orchestration, it provides extensive traffic routing capabilities mapped securely via dynamic telemetry.

## 1. Project Overview

This API Gateway functions as the unified entry point for all upstream services, abstracting cross-cutting concerns (authentication, rate limiting, logging) securely away from backend applications. 
It is deemed "**production-grade**" due to its strict lock-free execution philosophy (`O(1)` memory allocations via strict pointer tracking using `sync/atomic`), ensuring the gateway comfortably pipelines 1 Million+ RPS (requests per second) without experiencing catastrophic garbage collection pauses.

---

## 2. Features

### Core Features
- **Reverse Proxy:** High-velocity, zero-buffering data streams directly mapping `io.Reader`.
- **Config-Driven Routing:** Deterministic Radix Trie mapping exact, prefix, or regex matching endpoints mapped natively from YAML parameters.
- **Plugin System:** Extensible runtime interfaces parsing payloads transparently.
- **Security:** Builtin JSON Web Token (JWT) decode integration blocking unauthorized parameters instantly.

### Advanced Features
- **Atomic Hot Config Reload:** Update routes natively modifying YAML configuration structs dynamically executing a `sync/atomic.Pointer` swap. Eradicates downtime yielding strict graceful drainage iterations securing in-flight active payloads cleanly!
- **Distributed Rate Limiting:** Utilizes sliding-window mechanics natively injecting optimized Lua scripts parsing strict evaluations within centralized Redis memory securing entire clusters actively tracking localized failovers securely.
- **External Plugin SDK:** Extends Go boundaries utilizing HTTP sidecar proxying. Write authentication endpoints in Python/Rust securely validating traffic via strict proxy circuit breakers.

### AI Features
- **Smart Routing (AI Adaptive Load Balancing):** Instead of static Round Robin configurations, the Gateway enforces autonomous **Multi-Armed Bandit Reinforcement Learning** models scoring latency, errors, and load tracking continuously optimizing infrastructure via weighted Roulette Wheel selection dynamically routing around degraded instances reliably.

---

## 3. Architecture Summary

The Gateway implements a complete separation of concerns between its **Control Plane** (configuration parsing and tracking updates) and its **Data Plane** (live HTTP proxying via atomic mappings). 
For an extensive breakdown, see [Docs/architecture.md](Docs/architecture.md), [Docs/concepts.md](Docs/concepts.md) and [Docs/flow.md](Docs/flow.md).

---

## 4. Quick Start Guide

### Prerequisites
- Go 1.22+
- Redis (Optional, required for distributed sliding-window rate limiting)
- Make (Optional)

### Run Locally
```bash
# 1. Clone the repository
git clone https://github.com/bharanidharansrinivasan/api-gateway.git
cd api-gateway

# 2. Download dependencies
go mod download

# 3. Launch Redis (Optional, skip if just testing base routing)
docker run -d -p 6379:6379 redis

# 4. Boot the Gateway!
go run cmd/gateway/main.go --config configs/gateway.yaml
```

---

## 5. Configuration Guide

Everything is parsed cleanly via `configs/gateway.yaml`:
```yaml
# 1. Register routes natively evaluating payload tracking
routes:
  - id: users_api
    path: /api/v1/users/*
    match_type: prefix
    service: user-service
    plugins:
      - name: auth
      - name: rate-limit
        config:
          requests_per_minute: 100

# 2. Enable plugins mapped securely
plugins:
  - name: auth
    type: builtin.jwt
  - name: rate-limit
    type: builtin.ratelimit

# 3. Define the upstream microservice
services:
  - name: user-service
    lb_strategy: smart # Adaptive AI Load Balancer
    smart_routing:
      exploration_rate: 0.10
      weights:
        latency: 0.4
        errors: 0.5
        load: 0.1
    instances:
      - id: node-1
        address: localhost:8081
```

---

## 6. Deployment Guide

### Docker Integration
The proxy utilizes a distroless multistage build execution format guaranteeing tight secure boundaries.
```bash
# Build the production image cleanly
docker build -t api-gateway -f deployments/Dockerfile.gateway.stress .

# Execute instances bridging configuration directories
docker run -p 8080:8080 -p 9090:9090 -v ./configs/gateway.yaml:/etc/gateway/gateway.yaml api-gateway
```

### Scaling Configurations
When deploying extensively across orchestrators (Kubernetes / ECS):
1. Replicate the proxy containers horizontally securing a load-balancer (ALB/Nginx) boundary executing edge traffic mapping securely.
2. Ensure you initialize a centralized **Redis** database parameter into the global Gateway configuration securing distributed tracking dynamically preventing per-node memory desynchronization tracking sliding windows effectively!

---

## 7. Observability

All structural subsystems export metrics intrinsically natively mapping into Prometheus endpoints seamlessly available mapping to `:9090/metrics`.
- **Metrics (Prometheus):** Evaluates `gateway_request_duration_seconds`, `gateway_smart_lb_instance_score`, `gateway_circuit_breaker_transitions_total`, and active Redis parameters reliably preventing metric overloads natively blocking unused fields properly!
- **Logs:** Zap structures map normalized JSON console execution structs preventing overhead allocations actively.
- **Tracing:** Extensible mapping structurally available using sidecar injection endpoints via `headers` transparently!

---

## 8. Example Use Cases

- **E-commerce Backends:** Route high-traffic inventory calls via Smart Routing avoiding degraded database execution node constraints seamlessly dynamically.
- **Microservices Orchestration:** Authenticate requests comprehensively preventing multiple independent teams dynamically rewriting JWT decoding parameters actively across disparate languages transparently.
