// Package server implements the main HTTP server with graceful shutdown and
// load shedding via a bounded-concurrency semaphore.
package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// Config holds HTTP server configuration.
type Config struct {
	Port          int
	ReadHeader    time.Duration
	Read          time.Duration
	Write         time.Duration
	Idle          time.Duration
	MaxConcurrent int
}

// Server wraps net/http.Server with graceful shutdown support.
type Server struct {
	httpServer *http.Server
	log        *zap.Logger
}

// New creates a Server with all timeout and TLS settings applied.
func New(cfg Config, handler http.Handler, log *zap.Logger) *Server {
	// Wrap handler with load-shedding semaphore.
	shedHandler := loadShed(handler, cfg.MaxConcurrent, log)

	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           shedHandler,
		ReadHeaderTimeout: cfg.ReadHeader,
		ReadTimeout:       cfg.Read,
		WriteTimeout:      cfg.Write,
		IdleTimeout:       cfg.Idle,
	}

	return &Server{httpServer: srv, log: log}
}

// ListenAndServe starts the server. Blocks until the server is stopped.
func (s *Server) ListenAndServe() error {
	s.log.Info("gateway listening", zap.String("addr", s.httpServer.Addr))
	return s.httpServer.ListenAndServe()
}

// Shutdown performs graceful shutdown, waiting up to 30 seconds for in-flight
// requests to complete before forcibly closing connections.
func (s *Server) Shutdown(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s.log.Info("gateway shutting down gracefully")
	return s.httpServer.Shutdown(shutdownCtx)
}

// loadShed wraps the handler in a semaphore-based load shedder.
// When all slots are occupied, new requests receive 503 immediately.
// This is a REJECT strategy (not queue) — see design doc Section 12 for rationale.
func loadShed(next http.Handler, maxConcurrent int, log *zap.Logger) http.Handler {
	if maxConcurrent <= 0 {
		maxConcurrent = 1000
	}
	// A buffered channel of empty structs acts as a counting semaphore.
	sem := make(chan struct{}, maxConcurrent)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case sem <- struct{}{}: // acquired a slot
			defer func() { <-sem }() // release on return
			next.ServeHTTP(w, r)
		default: // no slots available — reject immediately
			log.Warn("request load shed",
				zap.String("path", r.URL.Path),
				zap.Int("active", len(sem)),
			)
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"server overloaded","status":503}`, http.StatusServiceUnavailable)
		}
	})
}
