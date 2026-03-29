// Package config provides YAML loading and hot-reload for gateway configuration.
package config

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// reloadDebounce is how long to wait after a file event before reloading.
// Editors often write a temp file then rename it, producing 2+ rapid events.
// The debounce coalesces these into a single reload.
const reloadDebounce = 200 * time.Millisecond

// drainWindow is how long a retired GatewayRuntime is kept alive after a swap
// so in-flight requests (which hold a direct pointer) can complete cleanly.
// Must be >= the longest configured route timeout.
const DrainWindow = 30 * time.Second

// Loader loads and hot-reloads the gateway configuration from a YAML file.
// All subsystems read from the atomically-swapped pointer; in-flight requests
// hold a reference to the old config and are never interrupted.
type Loader struct {
	path        string
	current     unsafe.Pointer // *Config
	log         *zap.Logger
	subsMu      sync.Mutex
	subscribers []chan<- *Config
}

// NewLoader creates a Loader, loads the initial config, and starts the file watcher.
// Returns an error if the initial config is invalid.
func NewLoader(path string, log *zap.Logger) (*Loader, error) {
	l := &Loader{path: path, log: log}
	cfg, err := l.loadAndValidate()
	if err != nil {
		return nil, fmt.Errorf("initial config load failed: %w", err)
	}
	atomic.StorePointer(&l.current, unsafe.Pointer(cfg))

	go l.watch()
	return l, nil
}

// Current returns the active configuration. Safe for concurrent use.
func (l *Loader) Current() *Config {
	return (*Config)(atomic.LoadPointer(&l.current))
}

// Subscribe registers a channel to receive the new *Config after every successful
// hot reload. The send is non-blocking — if the channel is full (subscriber is busy),
// the event is dropped. This is intentional: the subscriber's next read will still
// process the latest config since the channel is refilled on the next reload.
//
// Callers should use a buffered channel of size 1.
func (l *Loader) Subscribe(ch chan<- *Config) {
	l.subsMu.Lock()
	l.subscribers = append(l.subscribers, ch)
	l.subsMu.Unlock()
}

// ForceReload re-reads and validates the config file immediately,
// bypassing the file watcher debounce. Used by POST /admin/reload.
func (l *Loader) ForceReload() (*Config, error) {
	cfg, err := l.loadAndValidate()
	if err != nil {
		return nil, err
	}
	atomic.StorePointer(&l.current, unsafe.Pointer(cfg))
	l.fanOut(cfg)
	l.log.Info("config force-reloaded via admin endpoint")
	return cfg, nil
}

// watch uses fsnotify to detect file changes and hot-reload the config.
// Events are debounced by reloadDebounce to handle editors that write temp files.
// On a valid new config, it atomically swaps the pointer and notifies subscribers.
// On an invalid config, it logs the error and keeps the current config.
func (l *Loader) watch() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		l.log.Error("failed to create config file watcher", zap.Error(err))
		return
	}
	defer watcher.Close()

	if err := watcher.Add(l.path); err != nil {
		l.log.Error("failed to watch config file", zap.String("path", l.path), zap.Error(err))
		return
	}

	var debounceTimer *time.Timer

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				// Debounce: reset timer on each event within the debounce window.
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.AfterFunc(reloadDebounce, func() {
					l.log.Info("config file changed, attempting hot reload", zap.String("path", l.path))
					cfg, err := l.loadAndValidate()
					if err != nil {
						l.log.Error("config hot reload failed — keeping current config",
							zap.String("path", l.path),
							zap.Error(err),
						)
						return
					}
					atomic.StorePointer(&l.current, unsafe.Pointer(cfg))
					l.fanOut(cfg)
					l.log.Info("config hot reload successful")
				})
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			l.log.Warn("config file watcher error", zap.Error(err))
		}
	}
}

// fanOut sends cfg to all registered subscribers (non-blocking).
func (l *Loader) fanOut(cfg *Config) {
	l.subsMu.Lock()
	subs := l.subscribers
	l.subsMu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- cfg:
		default:
			// Subscriber already has a pending reload signal; skip.
		}
	}
}

// loadAndValidate reads, parses, and validates the config file.
func (l *Loader) loadAndValidate() (*Config, error) {
	data, err := os.ReadFile(l.path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config YAML: %w", err)
	}

	if err := Validate(&cfg); err != nil {
		return nil, fmt.Errorf("config validation: %w", err)
	}

	return &cfg, nil
}
