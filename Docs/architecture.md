# Architecture overview

This document explores the structural components composing the API Gateway. The project embodies a separation of concerns modeling modern edge architecture separating the Data Plane from the Control Plane visually and logically.

## High-Level Architecture Diagram
```text
 ┌──────────────────────────────────────────────────────────────┐
 │                    API GATEWAY PROCESS                       │
 │                                                              │
 │   ┌──────────────────────┐        ┌──────────────────────┐   │
 │   │    Control Plane     │        │      Data Plane      │   │
 │   │  (Admin, Reloader)   │        │     (HTTP Server)    │   │
 │   └─────────┬────────────┘        └──────────┬───────────┘   │
 │             │ Config Swap                    │               │
 │             ▼                                ▼               │   [Redis]
 │   ┌──────────────────────┐        ┌──────────────────────┐   │     ▲
 │   │  Gateway Runtime     │◀───────┤   Proxy Engine &     ├─┼─────┘
 │   │ (Routes / Plugins)   │        │   Smart Router       │   │
 │   └─────────┬────────────┘        └──────────┬───────────┘   │
 │             │                                │               │
 └─────────────┼────────────────────────────────┼───────────────┘
  Watch /etc   │                                ▼ HTTP/TCP
 ┌─────────────▼────────┐            ┌──────────────────────┐
 │     gateway.yaml     │            │  Upstream Services   │
 └──────────────────────┘            └──────────────────────┘
```

## Component Breakdown

### 1. Gateway Runtime (`internal/runtime`)
The structural brain of the routing architecture. Due to hot-reload constraints, modifying live routes or active arrays produces data races.
- The runtime compiles `router`, `plugins`, and active `balancers` into a stateless immutable pointer `GatewayRuntime`. 
- The **Control Plane** watches disk events. When `/etc` manifests an update, a brand new `GatewayRuntime` replicates. 
- It leverages an `atomic.Pointer` swap to flip pointers atomically causing zero downtime and yielding the original runtime to a 30s `net/http` connection draining sunset.

### 2. The Router (`internal/router`)
Leverages a customized deterministic Radix Trie approach supporting `prefix`, `exact`, and `regex` evaluation strings enabling high-speed linear evaluations of payload parameters into concrete backend `Service` references dynamically.

### 3. The Plugin System (`internal/plugin`)
Interfaces dictating strict entry `ExecuteRequest` and exit `ExecuteResponse` signatures.
- Includes Builtins: JWT, Global CORS, Logger, and Token buckets natively avoiding process separation overheads.
- Includes Externals (`internal/plugin/external`): Inter-process communication leveraging an HTTP Sidecar design validating and evaluating arbitrary cross-language HTTP webhooks as structural native middleware, secured by fallback failure policies (`reject` / `pass`).

### 4. Rate Limiter (`pkg/ratelimit`)
Standardized sliding windows preventing server degradation dynamically across cluster domains:
- Local domains map to `TokenBuckets`.
- Spacial clusters target centralized Redis configurations running atomic `Lua` execution sequences natively within Redis memory avoiding distributed time-skew and locking contention reliably.
- Degrades flawlessly via localized hard timeouts into generic native token buckets securing infrastructure globally.

### 5. Smart Routing Engine (`internal/loadbalancer/smart`)
Instead of relying purely blindly on static round-robin iterations, it actively calculates live EWMA (Exponential Weighted Moving Averages) across 3 specific criteria mapping float weights dynamically representing load balancer capabilities: `Latency`, `Errors`, `Load (Inflight capacity)`. 
Utilizes an **Epsilon-greedy heuristic array** continuously adjusting mathematical load allocations avoiding infrastructure starvation intelligently.

### 6. Resilience Systems
All network boundaries (`Upstreams`, `External Plugins`, `Redis Limiters`) map to localized generic **Circuit Breakers**.
If a node repeatedly fails (passing thresholds), it opens, instantly slicing execution payloads yielding structural degradation elegantly (`503 Unavailable`) without allowing cascading network loops tracking down the proxy execution path realistically.
