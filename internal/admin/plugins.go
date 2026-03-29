package admin

import (
	"encoding/json"
	"net/http"

	"github.com/bharanidharansrinivasan/api-gateway/internal/plugin/external"
	"go.uber.org/zap"
)

// PluginsHandler handles GET /admin/plugins.
// Returns a list of all registered builtin and external plugins with health status.
type PluginsHandler struct {
	builtinNames    []string
	externalPlugins []*external.ExternalPlugin
	log             *zap.Logger
}

// NewPluginsHandler creates a PluginsHandler.
func NewPluginsHandler(builtinNames []string, externalPlugins []*external.ExternalPlugin, log *zap.Logger) *PluginsHandler {
	return &PluginsHandler{
		builtinNames:    builtinNames,
		externalPlugins: externalPlugins,
		log:             log,
	}
}

// ExternalPluginInfo is the JSON response shape for an external plugin.
type ExternalPluginInfo struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	SDKMajor     int    `json:"sdk_major"`
	Address      string `json:"address"`
	OnError      string `json:"on_error"`
	TimeoutMs    int64  `json:"timeout_ms"`
	CircuitState string `json:"circuit_state"`
}

// ServeHTTP handles GET /admin/plugins.
func (h *PluginsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	extInfos := make([]ExternalPluginInfo, 0, len(h.externalPlugins))
	for _, p := range h.externalPlugins {
		info := p.Info()
		extInfos = append(extInfos, ExternalPluginInfo{
			Name:         info.Name,
			Version:      info.Version,
			SDKMajor:     info.SDKMajor,
			Address:      info.Address,
			OnError:      info.OnError,
			TimeoutMs:    info.TimeoutMs,
			CircuitState: info.CircuitState,
		})
	}

	response := map[string]interface{}{
		"builtin":  h.builtinNames,
		"external": extInfos,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
