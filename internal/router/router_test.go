package router_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/bharanidharansrinivasan/api-gateway/internal/config"
	"github.com/bharanidharansrinivasan/api-gateway/internal/router"
)

func buildRouter(t *testing.T, routes []config.RouteConfig) *router.Router {
	r, err := router.New(routes)
	if err != nil {
		t.Fatalf("failed to build router: %v", err)
	}
	return r
}

func makeReq(method, path string) *http.Request {
	return &http.Request{
		Method: method,
		URL:    &url.URL{Path: path},
	}
}

func TestExactMatchPriority(t *testing.T) {
	routes := []config.RouteConfig{
		{ID: "prefix", MatchType: "prefix", Path: "/api/v1/users", Service: "svc-prefix"},
		{ID: "exact", MatchType: "exact", Path: "/api/v1/users", Service: "svc-exact"},
	}
	r := buildRouter(t, routes)

	route, ok := r.Match(makeReq("GET", "/api/v1/users"))
	if !ok {
		t.Fatal("expected match")
	}
	if route.ID != "exact" {
		t.Fatalf("expected exact match to take priority, got %q", route.ID)
	}
}

func TestPrefixLongestWins(t *testing.T) {
	routes := []config.RouteConfig{
		{ID: "short", MatchType: "prefix", Path: "/api/v1"},
		{ID: "long", MatchType: "prefix", Path: "/api/v1/users/profile"},
		{ID: "medium", MatchType: "prefix", Path: "/api/v1/users"},
	}
	r := buildRouter(t, routes)

	tests := []struct {
		path   string
		wantID string
	}{
		{"/api/v1/users/profile/edit", "long"},
		{"/api/v1/users/active", "medium"},
		{"/api/v1/health", "short"},
	}

	for _, tc := range tests {
		route, ok := r.Match(makeReq("GET", tc.path))
		if !ok {
			t.Errorf("expected match for %q", tc.path)
			continue
		}
		if route.ID != tc.wantID {
			t.Errorf("path %q: want %q, got %q", tc.path, tc.wantID, route.ID)
		}
	}
}

func TestRegexMatch(t *testing.T) {
	routes := []config.RouteConfig{
		{ID: "regex-items", MatchType: "regex", Path: "^/items/[0-9]+$"},
	}
	r := buildRouter(t, routes)

	if _, ok := r.Match(makeReq("GET", "/items/42")); !ok {
		t.Errorf("expected match for /items/42")
	}
	if _, ok := r.Match(makeReq("GET", "/items/foo")); ok {
		t.Errorf("expected no match for /items/foo")
	}
}

func TestMethodFiltering(t *testing.T) {
	routes := []config.RouteConfig{
		{ID: "exact-get", MatchType: "exact", Path: "/api/data", Methods: []string{"GET"}},
		{ID: "prefix-post", MatchType: "prefix", Path: "/api/submit", Methods: []string{"POST"}},
	}
	r := buildRouter(t, routes)

	// Exact match checks method
	if _, ok := r.Match(makeReq("GET", "/api/data")); !ok {
		t.Errorf("expected GET to match exact route")
	}
	if _, ok := r.Match(makeReq("POST", "/api/data")); ok {
		t.Errorf("expected POST to fail on GET-only exact route")
	}

	// Prefix match checks method
	if _, ok := r.Match(makeReq("POST", "/api/submit/123")); !ok {
		t.Errorf("expected POST to match prefix route")
	}
	if _, ok := r.Match(makeReq("GET", "/api/submit/123")); ok {
		t.Errorf("expected GET to fail on POST-only prefix route")
	}
}

func TestAllMethodsAllowed(t *testing.T) {
	routes := []config.RouteConfig{
		{ID: "any-method", MatchType: "exact", Path: "/health"}, // no methods defined
	}
	r := buildRouter(t, routes)

	methods := []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"}
	for _, m := range methods {
		if _, ok := r.Match(makeReq(m, "/health")); !ok {
			t.Errorf("expected method %s to match", m)
		}
	}
}

func TestNoRouteReturnsNotFound(t *testing.T) {
	r := buildRouter(t, []config.RouteConfig{})
	if _, ok := r.Match(makeReq("GET", "/404")); ok {
		t.Errorf("expected no match for empty router")
	}
}

func TestInvalidRegexErrors(t *testing.T) {
	routes := []config.RouteConfig{
		{ID: "bad", MatchType: "regex", Path: "[broken"},
	}
	_, err := router.New(routes)
	if err == nil {
		t.Fatal("expected error for invalid regex during New()")
	}
}

func BenchmarkRouterMatch(b *testing.B) {
	routes := []config.RouteConfig{
		{ID: "exact", MatchType: "exact", Path: "/api/v1/exact"},
		{ID: "prefix", MatchType: "prefix", Path: "/api/v1/prefix"},
		{ID: "regex", MatchType: "regex", Path: "^/api/v1/items/[0-9]+$"},
	}
	r, _ := router.New(routes)

	reqExact := makeReq("GET", "/api/v1/exact")
	reqPrefix := makeReq("GET", "/api/v1/prefix/foo/bar")
	reqRegex := makeReq("GET", "/api/v1/items/42")
	reqMiss := makeReq("GET", "/api/v2/missing")

	b.Run("Exact", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r.Match(reqExact)
		}
	})
	b.Run("Prefix", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r.Match(reqPrefix)
		}
	})
	b.Run("Regex", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r.Match(reqRegex)
		}
	})
	b.Run("Miss", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r.Match(reqMiss)
		}
	})
}
