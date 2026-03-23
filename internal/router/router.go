// Package router provides path-based HTTP request routing for the API Gateway.
//
// Matching priority (highest to lowest):
//  1. Exact match   — "/api/v1/users" matches only that literal path.
//  2. Prefix match  — uses a radix trie; longest prefix wins.
//  3. Regex match   — linear scan of compiled patterns; use sparingly.
package router

import (
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/bharanidharansrinivasan/api-gateway/internal/config"
)

// MatchType describes how a route's path pattern is interpreted.
type MatchType int

const (
	MatchExact  MatchType = iota
	MatchPrefix MatchType = iota
	MatchRegex  MatchType = iota
)

// Route is the resolved, runtime representation of a config.RouteConfig.
// The router builds these at startup and they remain immutable thereafter.
type Route struct {
	ID           string
	Methods      map[string]struct{} // empty means all methods allowed
	MatchType    MatchType
	Path         string         // raw pattern
	Pattern      *regexp.Regexp // non-nil only for MatchRegex
	StripPrefix  bool
	ServiceName  string
	Timeout      int64 // nanoseconds; 0 = use global timeout
	MaxBodyBytes int64
	Plugins      []config.PluginRef // sorted by Order

	// pathLen is the length of the literal prefix, used for trie depth sorting.
	pathLen int
}

// Router dispatches incoming requests to a matched Route.
type Router struct {
	exact   map[string]*Route   // key: "METHOD:path" (or ":path" for any method)
	prefix  []*Route            // sorted by descending path length for longest-prefix-first
	regexes []*Route            // evaluated in declaration order
}

// New builds a Router from the provided route configs.
// Regex patterns are pre-compiled here so failures surface at startup.
func New(routes []config.RouteConfig) (*Router, error) {
	r := &Router{
		exact: make(map[string]*Route),
	}

	for _, rc := range routes {
		route, err := buildRoute(rc)
		if err != nil {
			return nil, err
		}

		switch route.MatchType {
		case MatchExact:
			for method := range route.Methods {
				r.exact[method+":"+route.Path] = route
			}
			if len(route.Methods) == 0 {
				r.exact[":"+route.Path] = route
			}
		case MatchPrefix:
			r.prefix = append(r.prefix, route)
		case MatchRegex:
			r.regexes = append(r.regexes, route)
		}
	}

	// Sort prefix routes: longest path first (most specific wins).
	sort.Slice(r.prefix, func(i, j int) bool {
		return r.prefix[i].pathLen > r.prefix[j].pathLen
	})

	return r, nil
}

// Match finds the best-matching Route for the given request.
// Returns (nil, false) if no route matches.
func (r *Router) Match(req *http.Request) (*Route, bool) {
	method := req.Method
	path := req.URL.Path

	// 1. Try exact match with method-specific key first, then wildcard.
	if route, ok := r.exact[method+":"+path]; ok {
		if methodAllowed(route, method) {
			return route, true
		}
	}
	if route, ok := r.exact[":"+path]; ok {
		if methodAllowed(route, method) {
			return route, true
		}
	}

	// 2. Try longest-prefix match.
	for _, route := range r.prefix {
		if strings.HasPrefix(path, route.Path) && methodAllowed(route, method) {
			return route, true
		}
	}

	// 3. Try regex matches in declaration order.
	for _, route := range r.regexes {
		if route.Pattern.MatchString(path) && methodAllowed(route, method) {
			return route, true
		}
	}

	return nil, false
}

// buildRoute converts a config.RouteConfig to a runtime Route.
func buildRoute(rc config.RouteConfig) (*Route, error) {
	route := &Route{
		ID:           rc.ID,
		StripPrefix:  rc.StripPrefix,
		ServiceName:  rc.Service,
		Timeout:      rc.Timeout.Nanoseconds(),
		MaxBodyBytes: rc.MaxBodyBytes,
		Path:         rc.Path,
		pathLen:      len(rc.Path),
	}

	// Build method set.
	route.Methods = make(map[string]struct{}, len(rc.Methods))
	for _, m := range rc.Methods {
		route.Methods[strings.ToUpper(m)] = struct{}{}
	}

	// Determine match type.
	switch rc.MatchType {
	case "exact":
		route.MatchType = MatchExact
	case "regex":
		route.MatchType = MatchRegex
		var err error
		route.Pattern, err = regexp.Compile(rc.Path)
		if err != nil {
			return nil, err
		}
	default: // "prefix" or empty
		route.MatchType = MatchPrefix
	}

	// Sort plugins by order so the chain executor can iterate without sorting.
	plugins := make([]config.PluginRef, len(rc.Plugins))
	copy(plugins, rc.Plugins)
	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].Order < plugins[j].Order
	})
	route.Plugins = plugins

	return route, nil
}

// methodAllowed returns true if the route permits the given HTTP method.
func methodAllowed(r *Route, method string) bool {
	if len(r.Methods) == 0 {
		return true // no restriction
	}
	_, ok := r.Methods[method]
	return ok
}
