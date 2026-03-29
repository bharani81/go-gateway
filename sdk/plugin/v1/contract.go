// Package v1 defines the Gateway Plugin SDK contract version 1.
//
// External plugins implement this HTTP API and use the PluginServer helper
// to avoid boilerplate. The gateway validates SDK major version compatibility
// at startup — a plugin compiled against SDK v1 works with any gateway v1.x release.
package v1

// SDKMajorVersion is the compatibility boundary.
// Plugin major version must match gateway major version.
// Minor/patch changes are backward-compatible.
const SDKMajorVersion = 1

// SDKVersion is the full semver of this SDK release.
const SDKVersion = "1.0.0"

// RequestPayload is sent by the gateway to the plugin's /execute/request endpoint.
//
// Note: The request Body is intentionally excluded. Including it would require
// the gateway to buffer the entire body in memory and re-stream it to the plugin,
// adding latency and memory pressure on large payloads. Plugins that need body
// inspection should be implemented as builtin plugins instead.
type RequestPayload struct {
	RouteID    string            `json:"route_id"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Query      string            `json:"query"`
	Headers    map[string]string `json:"headers"`
	RemoteAddr string            `json:"remote_addr"`
	UserID     string            `json:"user_id,omitempty"`
	TraceID    string            `json:"trace_id,omitempty"`
	Config     map[string]any    `json:"config"`
}

// PluginResponse is the plugin's decision on the request.
type PluginResponse struct {
	// Action must be "continue" or "abort".
	Action string `json:"action"`

	// StatusCode is the HTTP status to return to the client.
	// Only used when Action == "abort".
	StatusCode int `json:"status_code,omitempty"`

	// Message is the error message body.
	// Only used when Action == "abort".
	Message string `json:"message,omitempty"`

	// AddHeaders are injected into the upstream request before proxying.
	// Only used when Action == "continue".
	AddHeaders map[string]string `json:"add_headers,omitempty"`
}

// Manifest describes the external plugin's identity and SDK compatibility.
// The gateway fetches this from GET /manifest at startup.
type Manifest struct {
	Name            string `json:"name"`
	Version         string `json:"version"`          // plugin's own semver, e.g. "2.1.0"
	SDKMajorVersion int    `json:"sdk_major_version"` // must match gateway SDKMajorVersion
	Description     string `json:"description"`
	Author          string `json:"author"`
}
