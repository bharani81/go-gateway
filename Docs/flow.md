# Request Lifecycle Flow

This document details the complete step-by-step lifecycle of a request as it enters and traverses the API Gateway.

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
Plugins execute sequentially based on their `order` configuration. If any plugin throws an `AbortError`, the chain breaks, returning immediately to the client (e.g., throwing `429 Too Many Requests`).

**Standard Execution Order:**
1. Logger
2. CORS
3. Authentication (JWT)
4. Rate Limiting
5. External Plugins (Sidecars)

---

## Dedicated Sub-System Flows

### A. Auth Flow (JWT Authentication)
The gateway abstracts user authentication away from upstream microservices using JSON Web Tokens (JWT).

```text
[Client] ──(Authorization: Bearer <Token>)──► [Gateway]
                                                 │
                                           [Auth Plugin]
                                                 │
                                     ┌───────────▼───────────┐
                                     │ 1. Extract Token      │
                                     │ 2. Validate Signature │
                                     │ 3. Check Expiration   │
                                     └───────────┬───────────┘
                                                 │
                          ┌──────────────────────┴──────────────────────┐
                   [Invalid]                                          [Valid]
                       ▼                                                ▼
              Returns 401 Unauthorized                       Extract UserID & Claims
                                                                        │
                                                         Inject into GatewayContext
                                                                        │
                                                         Forward to Route/Service
```
1. **Extraction:** Retrieves the token from the `Authorization` header.
2. **Validation:** Verifies the cryptographic signature (HMAC/RSA) using the Gateway's centralized secret keys.
3. **Context Injection:** Strips token validation logic away from the microservice. The proxy instead forwards the `X-User-ID` natively appended as a safe header constraint utilizing data extracted internally to the `GatewayContext`.

### B. Rate Limiting Flow
Uses a distributed sliding window natively stored within Redis.

```text
[Gateway Context (Contains User-ID/IP)]
                   │
           [RateLimiter Plugin]
                   │
           ┌───────▼───────┐
           │ Redis Lua     │ ──► [Limit Exceeded?] ──(Yes)──► Returns 429 Too Many Requests
           │ Sliding Window│
           └───────┬───────┘
                   │
                 (No)
                   ▼
             Pass to Upstream
```

### C. Smart Routing & AI Decision Flow
The active AI adaptive load balancer allocates instances based on real-time feedback.

```text
[Load Balancer]
       │
       ▼
 1. Check Epsilon-Greedy (10% Traffic) ──(Triggered)──► Returns Random Upstream (Exploration)
       │
  (90% Traffic)
       ▼
 2. Load Telemetry (Latency, Errors, Active Requests)
       │
 3. Normalize arrays to [0..1] range
       │
 4. Compute Linear Weights: (W1*Lat) + (W2*Err) + (W3*Load)
       │
 5. Roulette Wheel Random Selection ──► Returns Selected Instance (Exploitation)
```

### D. Error Handling & Retry Flow
The proxy engine maintains an isolated boundary to safely retry idempotent requests gracefully navigating temporary HTTP errors.

```text
[Proxy Engine] ──(HTTP Streaming)──► [Upstream Microservice]
                                                 │
                                  ┌──────────────┴──────────────┐
                            [502, 503, 504]                  [200 OK]
                                  │                             │
                      [Is Method Idempotent?]         Stream response to Client
                                  │                             │
                   ┌──────────────┴──────────────┐         [done() Telemetry Hook]
                 (Yes)                          (No)
                   │                             │
         [Wait Exponential Backoff]     Return Error to Client
                   │
         [Retry Upstream max 2x]
```
- **Circuit Breakers:** If the proxy persistently traps errors hitting above 50% failure rates sequentially across instances, a tripped circuit breaker isolates the backend instantly forcing explicit localized failures avoiding global execution pauses.
