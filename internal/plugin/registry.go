// Package plugin provides the PluginRegistry that maps plugin names to factory functions.
package plugin

import (
	"fmt"
	"sort"

	"github.com/bharanidharansrinivasan/api-gateway/internal/config"
)

// Factory is a constructor function that creates a Plugin from a raw config map.
type Factory func(cfg map[string]interface{}) (Plugin, error)

// Registry maps a plugin type name (e.g., "builtin.logger") to its Factory.
// Plugins register themselves at init time or during gateway startup.
type Registry struct {
	factories map[string]Factory
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

// Register adds a factory for the given plugin type name.
// It panics on duplicate registration — treat this like init() code.
func (r *Registry) Register(typeName string, f Factory) {
	if _, exists := r.factories[typeName]; exists {
		panic(fmt.Sprintf("plugin: duplicate registration for type %q", typeName))
	}
	r.factories[typeName] = f
}

// Build constructs the ordered plugin list for a route by:
//  1. Looking up each plugin name in the provided PluginDef registry to find its type.
//  2. Calling the type's factory with the route-level config map.
//  3. Returning the plugins sorted by Order (ascending).
//
// Returns an error if any plugin type is unregistered or factory construction fails.
func (r *Registry) Build(defs []config.PluginDef, refs []config.PluginRef) ([]Plugin, error) {
	// Build name → type lookup from top-level plugin definitions.
	nameToType := make(map[string]string, len(defs))
	for _, d := range defs {
		nameToType[d.Name] = d.Type
	}

	// Sort refs by Order to guarantee the chain is ordered correctly at build time.
	sorted := make([]config.PluginRef, len(refs))
	copy(sorted, refs)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Order < sorted[j].Order
	})

	plugins := make([]Plugin, 0, len(sorted))
	for _, ref := range sorted {
		typeName, ok := nameToType[ref.Name]
		if !ok {
			return nil, fmt.Errorf("plugin %q: no type mapping found in plugins section", ref.Name)
		}
		factory, ok := r.factories[typeName]
		if !ok {
			return nil, fmt.Errorf("plugin type %q is not registered", typeName)
		}
		p, err := factory(ref.Config)
		if err != nil {
			return nil, fmt.Errorf("plugin %q (type %q): factory error: %w", ref.Name, typeName, err)
		}
		plugins = append(plugins, p)
	}
	return plugins, nil
}
