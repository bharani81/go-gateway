// Package proxy implements the core reverse proxy with retry logic, circuit breaker
// integration, and deadline-bounded retry budget.
package proxy

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"

	"github.com/bharanidharansrinivasan/api-gateway/internal/loadbalancer"
	"github.com/bharanidharansrinivasan/api-gateway/internal/registry"
	"github.com/bharanidharansrinivasan/api-gateway/pkg/circuitbreaker"
	"github.com/bharanidharansrinivasan/api-gateway/pkg/gwctx"
	"go.uber.org/zap"
)

const (
	minRemainingForRetry = 200 * time.Millisecond
	maxRetries           = 2
)

// retryableStatusCodes is the set of upstream status codes that warrant a retry.
// 500 is excluded: it may indicate a permanent bug and retrying can cause duplicates.
var retryableStatusCodes = map[int]bool{
	http.StatusBadGateway:         true, // 502
	http.StatusServiceUnavailable: true, // 503
	http.StatusGatewayTimeout:     true, // 504
}

// ReverseProxy forwards requests to an upstream instance and handles retries.
type ReverseProxy struct {
	transports *TransportRegistry
	log        *zap.Logger
}

// New creates a ReverseProxy.
func New(transports *TransportRegistry, log *zap.Logger) *ReverseProxy {
	return &ReverseProxy{transports: transports, log: log}
}

// ProxyRequest attempts to forward the request to a healthy upstream instance.
// It respects the circuit breaker state and retries on retryable failures.
//
//   - The retry budget is bounded by the request's context deadline.
//   - Only idempotent methods (GET, HEAD, OPTIONS, PUT, DELETE) or requests
//     with X-Idempotency-Key are retried.
//   - Once the response body has started streaming to the client, retries
//     are impossible. This is enforced by the streaming check below.
func (p *ReverseProxy) ProxyRequest(
	w http.ResponseWriter,
	r *http.Request,
	svc *registry.ServiceEntry,
	lb loadbalancer.LoadBalancer,
	serviceCB *circuitbreaker.CircuitBreaker,
	instanceCBs map[string]*circuitbreaker.CircuitBreaker,
) error {
	gwCtx := gwctx.MustFrom(r.Context())

	// Service-level circuit breaker check: if Open, fail immediately.
	if serviceCB != nil && !serviceCB.Allow() {
		gwCtx.CircuitState = gwctx.CircuitOpen
		gwCtx.FailureReason = "circuit_open"
		return &ProxyError{StatusCode: http.StatusServiceUnavailable, Message: "service circuit open"}
	}
	gwCtx.CircuitState = gwctx.CircuitClosed

	isIdempotent := isIdempotentRequest(r)

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Check remaining deadline before each attempt.
		if deadline, ok := r.Context().Deadline(); ok {
			if time.Until(deadline) < minRemainingForRetry {
				gwCtx.FailureReason = "deadline_exceeded"
				return &ProxyError{StatusCode: http.StatusGatewayTimeout, Message: "upstream deadline exceeded"}
			}
		}

		// Pick the next instance, skipping any with an open per-instance circuit.
		isSkipped := func(inst *registry.Instance) bool {
			cb, ok := instanceCBs[inst.ID]
			return ok && !cb.Allow()
		}

		healthy := svc.HealthyInstances()
		startRoute := time.Now()
		inst, done, err := lb.Next(healthy, isSkipped)
		if err != nil {
			gwCtx.FailureReason = "no_healthy_instances"
			if serviceCB != nil {
				serviceCB.RecordFailure()
			}
			return &ProxyError{StatusCode: http.StatusServiceUnavailable, Message: "no healthy upstream instances"}
		}

		gwCtx.SelectedInstance = inst.Address
		gwCtx.ServiceName = svc.Name

		// Attempt the upstream call.
		upstream := fmt.Sprintf("http://%s", inst.Address)
		status, err := p.doRequest(w, r, upstream, svc.Name)

		if err == nil && !retryableStatusCodes[status] {
			// Success path.
			if done != nil {
				done(time.Since(startRoute), false)
			}
			if cb, ok := instanceCBs[inst.ID]; ok {
				cb.RecordSuccess()
			}
			if serviceCB != nil {
				serviceCB.RecordSuccess()
			}
			return nil
		}

		if done != nil {
			done(time.Since(startRoute), true)
		}

		// Failure path: record and decide whether to retry.
		reason := "upstream_error"
		if err != nil {
			reason = "connection_error"
		} else {
			reason = fmt.Sprintf("upstream_%d", status)
		}

		if cb, ok := instanceCBs[inst.ID]; ok {
			cb.RecordFailure()
		}

		if !isIdempotent || attempt >= maxRetries {
			gwCtx.FailureReason = reason
			if serviceCB != nil {
				serviceCB.RecordFailure()
			}
			code := http.StatusBadGateway
			if status != 0 {
				code = status
			}
			return &ProxyError{StatusCode: code, Message: reason}
		}

		gwCtx.RetryCount++
		// Exponential backoff with jitter.
		backoff := time.Duration(attempt) * 100 * time.Millisecond
		jitter := time.Duration(rand.Intn(50)) * time.Millisecond
		p.log.Warn("retrying upstream request",
			zap.String("service", svc.Name),
			zap.String("instance", inst.Address),
			zap.String("reason", reason),
			zap.Int("attempt", attempt+1),
			zap.Duration("backoff", backoff+jitter),
		)
		select {
		case <-r.Context().Done():
			return &ProxyError{StatusCode: http.StatusGatewayTimeout, Message: "context cancelled during retry"}
		case <-time.After(backoff + jitter):
		}
	}

	return &ProxyError{StatusCode: http.StatusBadGateway, Message: "all retries exhausted"}
}

// doRequest performs a single HTTP request to the upstream and streams the response.
// Returns the upstream status code (0 on connection error) and a non-nil error
// only on connection-level failures.
func (p *ReverseProxy) doRequest(w http.ResponseWriter, r *http.Request, upstream, svcName string) (int, error) {
	outURL := upstream + r.URL.RequestURI()
	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, outURL, r.Body)
	if err != nil {
		return 0, err
	}

	// Copy inbound headers (excluding hop-by-hop) to the outbound request.
	for key, vals := range r.Header {
		if _, skip := hopByHopHeaders[key]; !skip {
			outReq.Header[key] = vals
		}
	}
	SetForwardingHeaders(outReq, r)

	transport := p.transports.Get(svcName)
	client := &http.Client{Transport: transport}

	resp, err := client.Do(outReq)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	StripHopByHopFromResponse(resp)

	// Copy upstream response headers to the client.
	for key, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Stream the body directly — never buffer the full response.
	if _, err := io.Copy(w, resp.Body); err != nil {
		// Body has started streaming; we cannot retry or change the status code.
		p.log.Error("streaming truncated",
			zap.String("upstream", upstream),
			zap.Error(err),
		)
	}

	return resp.StatusCode, nil
}

// isIdempotentRequest returns true for HTTP methods where it is safe to retry.
// POST/PATCH without X-Idempotency-Key are never retried.
func isIdempotentRequest(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete:
		return true
	case http.MethodPost, http.MethodPatch:
		return r.Header.Get("X-Idempotency-Key") != ""
	default:
		return false
	}
}

// ProxyError is returned when the proxy cannot deliver a successful upstream response.
type ProxyError struct {
	StatusCode int
	Message    string
}

func (e *ProxyError) Error() string {
	return fmt.Sprintf("proxy error %d: %s", e.StatusCode, e.Message)
}

// IsProxyError returns true if err is a ProxyError.
func IsProxyError(err error) bool {
	var e *ProxyError
	return errors.As(err, &e)
}
