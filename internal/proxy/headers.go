// Package proxy provides header manipulation utilities for the reverse proxy.
//
// This file enforces two rules:
//  1. Hop-by-hop headers (RFC 7230 §6.1) are stripped before forwarding.
//  2. X-Forwarded-* headers are NEVER trusted from clients; they are always
//     replaced by the gateway with authoritative values.
package proxy

import (
	"net"
	"net/http"
)

// hopByHopHeaders is the set of headers that must be removed before forwarding.
// These headers describe the connection between two adjacent nodes, not the
// full end-to-end message, so they must not be forwarded.
var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailers":            {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

// StripInboundHeaders removes headers that external clients should not be able
// to inject. This is called on every inbound request BEFORE any plugin runs.
//
// Stripped headers:
//   - All X-Forwarded-* (gateway will re-set them authoritatively)
//   - X-Real-IP (same reason)
//   - Any hop-by-hop headers
func StripInboundHeaders(r *http.Request) {
	r.Header.Del("X-Forwarded-For")
	r.Header.Del("X-Forwarded-Host")
	r.Header.Del("X-Forwarded-Proto")
	r.Header.Del("X-Real-IP")
	for h := range hopByHopHeaders {
		r.Header.Del(h)
	}
}

// SetForwardingHeaders adds X-Forwarded-* headers to an outbound request,
// replacing any existing values. clientIP is extracted from the original
// inbound request's RemoteAddr.
func SetForwardingHeaders(outbound *http.Request, inbound *http.Request) {
	clientIP, _, err := net.SplitHostPort(inbound.RemoteAddr)
	if err != nil {
		clientIP = inbound.RemoteAddr
	}
	outbound.Header.Set("X-Forwarded-For", clientIP)
	outbound.Header.Set("X-Forwarded-Host", inbound.Host)

	proto := "http"
	if inbound.TLS != nil {
		proto = "https"
	}
	outbound.Header.Set("X-Forwarded-Proto", proto)
}

// StripHopByHopFromResponse removes hop-by-hop headers from the upstream response
// before it is forwarded to the client.
func StripHopByHopFromResponse(resp *http.Response) {
	for h := range hopByHopHeaders {
		resp.Header.Del(h)
	}
}
