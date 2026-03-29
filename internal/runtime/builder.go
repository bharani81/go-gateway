package runtime

import (
	"fmt"
	"sort"

	"github.com/bharanidharansrinivasan/api-gateway/internal/config"
	"github.com/bharanidharansrinivasan/api-gateway/internal/plugin"
	"github.com/bharanidharansrinivasan/api-gateway/internal/router"
	"go.uber.org/zap"
)

// Build constructs a GatewayRuntime from a config snapshot.
//
// It rebuilds only route-derived subsystems (Router + PluginChains).
// It does NOT touch the service registry or health checker — those are
// long-lived singletons that receive config diffs separately.
//
// Returns an error if route patterns fail to compile or plugin factories fail.
func Build(
	cfg *config.Config,
	pluginReg *plugin.Registry,
	version uint64,
	log *zap.Logger,
) (*GatewayRuntime, error) {
	// Build router from route configs.
	r, err := router.New(cfg.Routes)
	if err != nil {
		return nil, fmt.Errorf("router build failed: %w", err)
	}

	// Build plugin chain per route.
	chains := make(map[string]*plugin.Chain, len(cfg.Routes))
	for _, route := range cfg.Routes {
		// Sort plugins by order before building, matching registry.Build contract.
		refs := make([]config.PluginRef, len(route.Plugins))
		copy(refs, route.Plugins)
		sort.Slice(refs, func(i, j int) bool { return refs[i].Order < refs[j].Order })

		plugins, err := pluginReg.Build(cfg.Plugins, refs)
		if err != nil {
			return nil, fmt.Errorf("plugin chain for route %q: %w", route.ID, err)
		}
		chains[route.ID] = plugin.NewChain(plugins, log)
	}

	return &GatewayRuntime{
		Version:      version,
		Router:       r,
		PluginChains: chains,
	}, nil
}
