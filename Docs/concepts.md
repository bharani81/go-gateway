# Core Concepts

This document explains the foundational concepts behind the API Gateway to help engineers understand the architecture and design decisions.

## What is an API Gateway?
An API Gateway is a specialized reverse proxy that acts as the single entry point for all clients interacting with a backend system. Instead of clients calling dozens of microservices directly, they call the gateway. The gateway then routes requests to the appropriate service while handling cross-cutting concerns like security, observability, and traffic shaping.

## Why use an API Gateway in Microservices?
Microservice architectures introduce complexity in how clients discover and communicate with services. An API Gateway solves these problems by providing:
- **Centralized Security**: Authentication (JWT) and authorization are handled once, rather than duplicated across every service.
- **Traffic Control**: Rate limiting, load shedding, and circuit breaking prevent backend services from being overwhelmed.
- **Protocol Translation**: Clients use simple HTTP/JSON, while backends might use gRPC, Unix sockets, or other protocols internally.
- **Decoupling**: Backend services can be refactored, merged, or split without changing the external API surface.

## Plugin-Based Architecture
To gracefully handle diverse enterprise requirements, the gateway uses a **Pipeline/Plugin architecture**. 

Rather than hardcoding authentication or logging into the core routing logic, features are implemented as standalone plugins complying with a strict interface. These plugins are executed in a defined sequence (the "chain") for each route. 
- **Why?** It isolates complexity, makes testing easier, and allows operators to compose unique middleware chains per route.

## Reverse Proxy Concept
As a reverse proxy, the gateway retrieves resources on behalf of a client from one or more servers. To the client, the gateway *is* the server. Internally, the gateway streams the outbound HTTP connection and pipes the backend's response back to the client continuously. This ensures the gateway maintains a strict `O(1)` memory profile, never buffering massive file uploads or downloads in memory.

## Hot Config Reload (Atomic Runtime Swap)
In production, you cannot restart the gateway just to update a route or change a rate limit, as this drops in-flight requests.
- **How it works:** The gateway watches its configuration files for changes. When a change happens, a completely new routing tree, load balancer map, and plugin registry are built in the background forming a new `GatewayRuntime`. 
- **Atomic Swap:** Utilizing `sync/atomic`, the active pointer is swapped instantly. New requests use the new runtime seamlessly.
- **Drain Window:** The old runtime is retired gently, giving active requests a grace period (e.g., 30s) to finish draining before structurally garbage collecting memory connections.

## Distributed Rate Limiting
To prevent abuse, the gateway limits how many requests a client (IP or User) can make.
- **Sliding Window:** Rather than fixed intervals (which suffer from burst spikes at the reset window crossover), this gateway leverages a sliding window algorithm ensuring a consistently enforced limit over contiguous blocks of time.
- **Why Redis?** In a multi-instance gateway deployment, memory-based limiters only know about the traffic *they* receive. Redis guarantees deterministic global limits across the entire cluster. 
- **Fallback Safe:** Supported by strict Redis circuit-breakers; if Redis crashes, the gateway automatically degrades to an in-memory token bucket smoothly preventing cascading failure timeouts.

## External Plugins (Sidecar Model)
While Go-based plugins are highly performant, they require recompiling the entire proxy.
- **Sidecar Model:** The gateway allows registering external HTTP plugins. The gateway pauses the request, forwards the request context to an external sidecar via HTTP (or unix sockets), and evaluates a `Pass/Reject` response.
- **Use case:** Allowing teams to write custom WAF or Authentication logic in Python, Node, or Rust without modifying the core Gateway's Go binary.
- **Safety:** It leverages strict active `/healthz` polling and fallback policies (`reject` vs `pass`) on connectivity errors.

## AI-Based Smart Routing
Traditional load balancers (like Round Robin) blindly rotate traffic through backends, ignoring the reality that some servers might be struggling, experiencing GC pauses, or overloaded.
- **The Problem It Solves:** Preventing traffic from hitting degraded instances.
- **The Solution:** A Multi-Armed Bandit algorithm intelligently scores backends based on real-time **EWMA (Exponential Weighted Moving Average) Latency**, **Error Rates**, and **Load (Inflight Requests)**. Utilizing probabilistic routing (Weighted Roulette Wheel selection), the highest-scoring nodes reliably receive more traffic smoothly routing around localized degradation in real time.
