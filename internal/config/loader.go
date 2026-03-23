// Package config provides YAML loading and hot-reload for gateway configuration.
package config

import (
	"fmt"
	"os"
	"sync/atomic"
	"unsafe"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// Loader loads and hot-reloads the gateway configuration from a YAML file.
// All subsystems read from the atomically-swapped pointer; in-flight requests
// hold a reference to the old config and are never interrupted.
type Loader struct {
	path    string
	current unsafe.Pointer // *Config
	log     *zap.Logger
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

// watch uses fsnotify to detect file changes and hot-reload the config.
// On a valid new config, it atomically swaps the pointer.
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

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				l.log.Info("config file changed, attempting hot reload", zap.String("path", l.path))
				cfg, err := l.loadAndValidate()
				if err != nil {
					l.log.Error("config hot reload failed — keeping current config",
						zap.String("path", l.path),
						zap.Error(err),
					)
					continue
				}
				atomic.StorePointer(&l.current, unsafe.Pointer(cfg))
				l.log.Info("config hot reload successful")
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			l.log.Warn("config file watcher error", zap.Error(err))
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
