package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
)

// Echo server for stress testing. Simulates an upstream backend service.
// It simply echoes back headers and the request URL to verify routing.
// Extremely lightweight to ensure the bottleneck is the Gateway, not this service.
func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		
		resp := map[string]interface{}{
			"status":  "ok",
			"path":    r.URL.Path,
			"method":  r.Method,
			"headers": r.Header,
		}
		json.NewEncoder(w).Encode(resp)
	})

	log.Printf("Echo server starting on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
