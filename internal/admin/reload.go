// Package admin provides administrative HTTP endpoints for the gateway.
package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/bharanidharansrinivasan/api-gateway/internal/config"
	"go.uber.org/zap"
)

// ReloadHandler handles POST /admin/reload.
// It triggers an immediate config reload bypassing the fsnotify debounce,
// and returns the new runtime version in the response.
type ReloadHandler struct {
	loader *config.Loader
	log    *zap.Logger
}

// NewReloadHandler creates a ReloadHandler.
func NewReloadHandler(loader *config.Loader, log *zap.Logger) *ReloadHandler {
	return &ReloadHandler{loader: loader, log: log}
}

// ServeHTTP handles POST /admin/reload.
func (h *ReloadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	_, err := h.loader.ForceReload()
	if err != nil {
		h.log.Error("admin reload failed", zap.Error(err))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error":  err.Error(),
			"status": "failed",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "ok",
		"reloaded_at": time.Now().UTC().Format(time.RFC3339),
	})
}
