// Package builtin provides the JWT authentication plugin.
//
// Supported auth models (configured per-route):
//
//   - "validate"    — Gateway fully validates signature + claims; forwards decoded
//     claims to upstream via X-User-Id and X-Auth-Claims headers.
//   - "passthrough" — Gateway forwards the raw Authorization header unchanged.
//     The upstream service is responsible for validation.
//   - "hybrid"      — Gateway validates structurally (signature, exp, iss, aud)
//     then forwards decoded claims. Upstream enforces business-level authz.
//
// IMPORTANT: The gateway does NOT access any user database. It only verifies
// the cryptographic signature and standard claims. Identity is asserted by the
// token issuer; the gateway trusts the signature.
package builtin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/bharanidharansrinivasan/api-gateway/internal/plugin"
	"github.com/bharanidharansrinivasan/api-gateway/pkg/gwctx"
	gojwt "github.com/golang-jwt/jwt/v5"
)

// JWTPlugin validates JWT tokens and optionally forwards decoded claims.
type JWTPlugin struct {
	authModel      string   // validate | passthrough | hybrid
	algorithms     []string // allowed algorithms (e.g., ["HS256"])
	secret         []byte   // for HMAC algorithms
	issuer         string
	audience       string
	forwardClaims  []string // claim names to forward as headers
	clockSkew      int64    // seconds
}

// NewJWTPlugin constructs a JWTPlugin from a route-level config map.
func NewJWTPlugin(cfg map[string]interface{}) (*JWTPlugin, error) {
	p := &JWTPlugin{
		authModel:  "hybrid",
		algorithms: []string{"HS256"},
		clockSkew:  30,
	}

	if model, ok := cfg["auth_model"].(string); ok {
		p.authModel = model
	}
	if secret, ok := cfg["secret"].(string); ok {
		p.secret = []byte(secret)
	}
	if issuer, ok := cfg["issuer"].(string); ok {
		p.issuer = issuer
	}
	if aud, ok := cfg["audience"].(string); ok {
		p.audience = aud
	}
	if algos, ok := cfg["algorithms"].([]interface{}); ok {
		p.algorithms = p.algorithms[:0]
		for _, a := range algos {
			if s, ok := a.(string); ok {
				p.algorithms = append(p.algorithms, s)
			}
		}
	}
	if claims, ok := cfg["forward_claims"].([]interface{}); ok {
		for _, c := range claims {
			if s, ok := c.(string); ok {
				p.forwardClaims = append(p.forwardClaims, s)
			}
		}
	}

	if p.authModel != "passthrough" && len(p.secret) == 0 {
		return nil, fmt.Errorf("jwt-auth: 'secret' is required for auth_model=%q", p.authModel)
	}

	return p, nil
}

func (p *JWTPlugin) Name() string { return "jwt-auth" }

// ExecuteRequest validates the JWT and injects claims into GatewayContext.
// For "passthrough" mode, the Authorization header is forwarded unchanged.
func (p *JWTPlugin) ExecuteRequest(w http.ResponseWriter, r *http.Request) error {
	if p.authModel == "passthrough" {
		return nil // upstream handles validation
	}

	// 1. Extract token.
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return &plugin.AbortError{StatusCode: http.StatusUnauthorized, Message: "invalid_token"}
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return &plugin.AbortError{StatusCode: http.StatusUnauthorized, Message: "invalid_token"}
	}
	tokenString := parts[1]

	// 2. Parse and validate using golang-jwt.
	opts := []gojwt.ParserOption{
		gojwt.WithValidMethods(p.algorithms),
	}
	if p.issuer != "" {
		opts = append(opts, gojwt.WithIssuer(p.issuer))
	}
	if p.audience != "" {
		opts = append(opts, gojwt.WithAudience(p.audience))
	}

	token, err := gojwt.Parse(tokenString, func(t *gojwt.Token) (interface{}, error) {
		// Reject "none" algorithm (algorithm confusion attack prevention).
		if t.Method == gojwt.SigningMethodNone {
			return nil, fmt.Errorf("algorithm 'none' is not permitted")
		}
		return p.secret, nil
	}, opts...)

	if err != nil || !token.Valid {
		// Log the specific reason internally; return a generic error externally.
		return &plugin.AbortError{StatusCode: http.StatusUnauthorized, Message: "invalid_token"}
	}

	// 3. Store claims on GatewayContext for downstream plugins and the access log.
	mapClaims, ok := token.Claims.(gojwt.MapClaims)
	if !ok {
		return &plugin.AbortError{StatusCode: http.StatusUnauthorized, Message: "invalid_token"}
	}

	gwCtx := gwctx.From(r.Context())
	if gwCtx != nil {
		gwCtx.AuthClaims = mapClaims
		if sub, ok := mapClaims["sub"].(string); ok {
			gwCtx.UserID = sub
		}
	}

	// 4. Forward selected claims to upstream as headers (validate + hybrid modes).
	if len(p.forwardClaims) > 0 {
		if sub, ok := mapClaims["sub"].(string); ok {
			r.Header.Set("X-User-Id", sub)
		}
		claimsToForward := make(map[string]interface{})
		for _, key := range p.forwardClaims {
			if val, ok := mapClaims[key]; ok {
				claimsToForward[key] = val
			}
		}
		if encoded, err := json.Marshal(claimsToForward); err == nil {
			r.Header.Set("X-Auth-Claims", string(encoded))
		}
	}

	return nil
}

// ExecuteResponse is a no-op for JWT — all work is done in ExecuteRequest.
func (p *JWTPlugin) ExecuteResponse(_ http.ResponseWriter, _ *http.Request) error {
	return nil
}
