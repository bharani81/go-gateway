// Package builtin provides the CORS plugin for the API Gateway.
package builtin

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/bharanidharansrinivasan/api-gateway/internal/plugin"
)

// CORSPlugin handles CORS preflight (OPTIONS) requests and injects
// CORS response headers on actual cross-origin requests.
type CORSPlugin struct {
	allowedOrigins   map[string]struct{}
	allowedMethods   string
	allowedHeaders   string
	allowCredentials bool
	maxAge           string
}

// NewCORSPlugin constructs a CORSPlugin from the route-level config map.
func NewCORSPlugin(cfg map[string]interface{}) (*CORSPlugin, error) {
	p := &CORSPlugin{
		allowedOrigins: make(map[string]struct{}),
		allowedMethods: "GET, POST, PUT, DELETE, OPTIONS",
		allowedHeaders: "Content-Type, Authorization",
		maxAge:         "86400",
	}

	if origins, ok := cfg["allowed_origins"].([]interface{}); ok {
		for _, o := range origins {
			if s, ok := o.(string); ok {
				p.allowedOrigins[s] = struct{}{}
			}
		}
	}
	if methods, ok := cfg["allowed_methods"].([]interface{}); ok {
		ms := make([]string, 0, len(methods))
		for _, m := range methods {
			if s, ok := m.(string); ok {
				ms = append(ms, s)
			}
		}
		p.allowedMethods = strings.Join(ms, ", ")
	}
	if headers, ok := cfg["allowed_headers"].([]interface{}); ok {
		hs := make([]string, 0, len(headers))
		for _, h := range headers {
			if s, ok := h.(string); ok {
				hs = append(hs, s)
			}
		}
		p.allowedHeaders = strings.Join(hs, ", ")
	}
	if creds, ok := cfg["allow_credentials"].(bool); ok {
		p.allowCredentials = creds
	}
	if age, ok := cfg["max_age"].(int); ok {
		p.maxAge = fmt.Sprintf("%d", age)
	}

	// Security: credentials + wildcard origin is forbidden by spec.
	if p.allowCredentials {
		if _, hasWild := p.allowedOrigins["*"]; hasWild {
			return nil, fmt.Errorf("cors: allow_credentials=true is incompatible with wildcard origin '*'")
		}
	}

	return p, nil
}

func (p *CORSPlugin) Name() string { return "cors" }

// ExecuteRequest handles OPTIONS preflight requests by responding directly
// without proxying to the upstream. For non-OPTIONS requests, it validates
// the Origin header.
func (p *CORSPlugin) ExecuteRequest(w http.ResponseWriter, r *http.Request) error {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil // Not a cross-origin request; skip.
	}

	// Check origin allowlist.
	if _, ok := p.allowedOrigins[origin]; !ok {
		if _, wild := p.allowedOrigins["*"]; !wild {
			return &plugin.AbortError{
				StatusCode: http.StatusForbidden,
				Message:    "origin not allowed",
			}
		}
	}

	if r.Method == http.MethodOptions {
		// Preflight: respond immediately, do not proxy to upstream.
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", p.allowedMethods)
		w.Header().Set("Access-Control-Allow-Headers", p.allowedHeaders)
		w.Header().Set("Access-Control-Max-Age", p.maxAge)
		w.Header().Set("Vary", "Origin")
		if p.allowCredentials {
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		w.WriteHeader(http.StatusNoContent)
		return &plugin.AbortError{StatusCode: http.StatusNoContent, Message: "preflight"}
	}
	return nil
}

// ExecuteResponse injects CORS headers on the actual (non-preflight) response.
func (p *CORSPlugin) ExecuteResponse(w http.ResponseWriter, r *http.Request) error {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Vary", "Origin")
	if p.allowCredentials {
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	return nil
}
