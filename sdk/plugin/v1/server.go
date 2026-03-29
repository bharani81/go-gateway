// Package v1 — PluginServer is the zero-boilerplate HTTP server for external plugin authors.
//
// Usage:
//
//	func main() {
//	    srv := v1.NewPluginServer(v1.Manifest{
//	        Name: "my-auth", Version: "1.0.0", SDKMajorVersion: 1,
//	    }, &MyPlugin{})
//	    log.Fatal(srv.ListenAndServe(":8082"))
//	}
//
//	type MyPlugin struct{}
//	func (p *MyPlugin) OnRequest(req v1.RequestPayload) v1.PluginResponse {
//	    if req.Headers["X-Api-Key"] != "secret" {
//	        return v1.PluginResponse{Action: "abort", StatusCode: 401, Message: "unauthorized"}
//	    }
//	    return v1.PluginResponse{Action: "continue"}
//	}
//	func (p *MyPlugin) OnResponse(req v1.RequestPayload) v1.PluginResponse {
//	    return v1.PluginResponse{Action: "continue"}
//	}
package v1

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

// Handler is the only interface external plugin authors implement.
type Handler interface {
	OnRequest(payload RequestPayload) PluginResponse
	OnResponse(payload RequestPayload) PluginResponse
}

// PluginServer wires up the HTTP server with all required SDK endpoints.
type PluginServer struct {
	manifest Manifest
	handler  Handler
}

// NewPluginServer creates a PluginServer.
func NewPluginServer(manifest Manifest, handler Handler) *PluginServer {
	return &PluginServer{manifest: manifest, handler: handler}
}

// ListenAndServe starts the plugin HTTP server.
// Supports TCP ("host:port") or Unix socket ("unix:///path/to/file") addresses.
func (s *PluginServer) ListenAndServe(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/manifest", s.handleManifest)
	mux.HandleFunc("/execute/request", s.handleExecuteRequest)
	mux.HandleFunc("/execute/response", s.handleExecuteResponse)
	mux.HandleFunc("/healthz", s.handleHealthz)

	srv := &http.Server{Handler: mux}

	// Unix domain socket support: "unix:///tmp/plugin.sock"
	if strings.HasPrefix(addr, "unix://") {
		socketPath := strings.TrimPrefix(addr, "unix://")
		ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socketPath)
		if err != nil {
			return err
		}
		return srv.Serve(ln)
	}

	srv.Addr = addr
	return srv.ListenAndServe()
}

func (s *PluginServer) handleManifest(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.manifest)
}

func (s *PluginServer) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *PluginServer) handleExecuteRequest(w http.ResponseWriter, r *http.Request) {
	s.handleExecute(w, r, s.handler.OnRequest)
}

func (s *PluginServer) handleExecuteResponse(w http.ResponseWriter, r *http.Request) {
	s.handleExecute(w, r, s.handler.OnResponse)
}

func (s *PluginServer) handleExecute(
	w http.ResponseWriter,
	r *http.Request,
	fn func(RequestPayload) PluginResponse,
) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload RequestPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid request payload", http.StatusBadRequest)
		return
	}
	resp := fn(payload)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
