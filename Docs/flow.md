# Request Lifecycle Flow

This document outlines the step-by-step lifecycle of a request as it enters and traverses the API Gateway.

## High-Level Flow Visualization

```text
[Client]
   ▼ User Request
   ├─► [1] Gateway Server (Connection Accept, Timeouts)
       ▼
       ├─► [2] Global Middleware (Panic Recovery -> Context Injection)
           ▼
           ├─► [3] Router (Path Matching, Method Filtering)
               ▼
               ├─► [4] Plugin Chain (Authentication, Rate Limiting, CORS)
                   ▼ (If accepted)
                   ├─► [5] Smart Load Balancer (AI Scoring, Selection)
                       ▼
                       ├─► [6] Proxy Engine (Circuit Breaking, Forwarding)
                           ▼
                           ├─► [7] Upstream Microservice
                           ◄─┴ (Response Body Streamed)
                       ◄─┴ (Telemetry Metrics Captured)
                   ◄─┴ (Post-Execution Plugins)
           ◄─┴ (Response headers written)
       ◄─┴ (Connection Closed / Keep-Alive)
[Client]
```

## Step-By-Step Breakdown

### 1. Connection & Initial Gateway Handling
The client establishes a TCP connection. The core Go HTTP server reads the headers, subject to strict `read_header` timeout definitions, mitigating slowloris attacks.

### 2. Global Middleware
Before the route is even matched, the request passes through global gateway middleware:
- **Panic Recovery:** A deferred panic catcher prevents malformed logic from terminating the proxy.
- **Context injection:** A global `GatewayContext` structure is seeded into the `http.Request` context to carry user IDs, routing tracking IDs, and telemetry data.

### 3. The Router 
The gateway looks at its active, atomically hot-swappable `GatewayRuntime` Radix tree:
- Performs `Exact`, `Prefix`, or `Regex` path matching.
- Resolves the destined upstream logical `Service` target and the localized `Plugin Chain` meant for this route.

### 4. Plugin Chain Execution
Plugins execute sequentially based on their `order` configuration. If any plugin throws an `AbortError`, the chain breaks, returning immediately to the client (e.g. throwing `429 Too Many Requests`).

**Standard Ordering Flow:**
1. **Logger:** Tracks request initialization payload parameters.
2. **CORS:** Evaluates `OPTIONS` preflight headers and injects domain-origin assertions.
3. **Authentication (JWT):** Decodes the Bearer token, validates the HMAC/RSA signature, and embeds the user claims natively into the `GatewayContext`.
4. **Rate Limiting:** Utilizes the extracted User-ID from the JWT to slide the Redis limiting window. Rejects if quota is met.
5. **External Plugins:** Reaches out to external WAFs via Sidecar IPC sockets.

### 5. Smart Load Balancing Decision
If the plugins accept the payload, the request targets the `LoadBalancer`:
- The AI Smart Router evaluates the active EWMA telemetry scores of all healthy instances mapped to the upstream service.
- **Exploitation (90%):** Weighted algorithms favor the lowest latency, lowest error-rate nodes instantaneously.
- **Exploration (10%):** Epsilon-greedy injection selects completely randomly to evaluate if 'dead' nodes have recovered latency.
- It returns a target `Address` and a telemetry `done()` recording hook.

### 6. Reverse Proxy & Execution 
The `ReverseProxy` attempts to open an HTTP pool stream directly to the selected `Address`:
- Evaluates per-instance and global service **Circuit Breakers**.
- **Retry Logic:** If the stream hits a retryable status code (`502`, `503`, `504`) or connection refusal *and* the request is idempotent (`GET`, `PUT`), it retries exponentially until the global context deadline runs out.
- Pipes the exact `io.Reader` response body back to the client cleanly.

### 7. Telemetry & Cleanup
Once the stream concludes (or panics):
- The telemetry `done(latency, errorStatus)` hook evaluates. The Smart Load balancer adjusts the numeric AI scores internally.
- The `GatewayContext` dumps its final tracking arrays into the Prometheus metrics pool `gateway_request_duration_seconds`.
