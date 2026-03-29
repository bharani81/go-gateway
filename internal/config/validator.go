// Package config provides validation for the gateway configuration.
package config

import (
	"fmt"
	"regexp"
	"strings"
)

const maxRegexRoutes = 20

// Validate performs two-phase validation on the parsed config:
//  1. Schema validation: required fields, valid enum values, type correctness.
//  2. Reference integrity: routes reference existing services and plugins.
//
// Validation errors are accumulated and returned together so the operator can
// fix all issues in one edit cycle rather than discovering them one at a time.
func Validate(cfg *Config) error {
	var errs []string

	// Phase 1: gateway config
	if cfg.Gateway.Port == 0 {
		errs = append(errs, "gateway.port is required")
	}
	if cfg.Gateway.MaxConcurrent == 0 {
		cfg.Gateway.MaxConcurrent = 1000 // safe default
	}

	// Phase 1: services
	serviceNames := make(map[string]struct{}, len(cfg.Services))
	for i, svc := range cfg.Services {
		if svc.Name == "" {
			errs = append(errs, fmt.Sprintf("services[%d].name is required", i))
			continue
		}
		if _, dup := serviceNames[svc.Name]; dup {
			errs = append(errs, fmt.Sprintf("duplicate service name: %q", svc.Name))
		}
		serviceNames[svc.Name] = struct{}{}

		validLB := map[string]bool{"round-robin": true, "random": true, "smart": true, "": true}
		if !validLB[svc.LBStrategy] {
			errs = append(errs, fmt.Sprintf("service %q: unsupported lb_strategy %q", svc.Name, svc.LBStrategy))
		}
		if len(svc.Instances) == 0 {
			errs = append(errs, fmt.Sprintf("service %q: must have at least one instance", svc.Name))
		}
	}

	// Phase 1: plugins registry
	pluginNames := make(map[string]struct{}, len(cfg.Plugins))
	for i, p := range cfg.Plugins {
		if p.Name == "" {
			errs = append(errs, fmt.Sprintf("plugins[%d].name is required", i))
			continue
		}
		pluginNames[p.Name] = struct{}{}
	}

	// Phase 1 + 2: routes
	routeIDs := make(map[string]struct{}, len(cfg.Routes))
	regexCount := 0

	for i, r := range cfg.Routes {
		prefix := fmt.Sprintf("routes[%d] (id=%q)", i, r.ID)

		if r.ID == "" {
			errs = append(errs, fmt.Sprintf("routes[%d].id is required", i))
		} else if _, dup := routeIDs[r.ID]; dup {
			errs = append(errs, fmt.Sprintf("duplicate route id: %q", r.ID))
		} else {
			routeIDs[r.ID] = struct{}{}
		}

		if r.Path == "" {
			errs = append(errs, prefix+": path is required")
		}

		validMatch := map[string]bool{"exact": true, "prefix": true, "regex": true}
		if r.MatchType == "" {
			cfg.Routes[i].MatchType = "prefix" // safe default
		} else if !validMatch[r.MatchType] {
			errs = append(errs, fmt.Sprintf("%s: invalid match_type %q", prefix, r.MatchType))
		}

		if r.MatchType == "regex" {
			regexCount++
			if _, err := regexp.Compile(r.Path); err != nil {
				errs = append(errs, fmt.Sprintf("%s: invalid regex pattern: %v", prefix, err))
			}
		}

		// Reference integrity: service must exist
		if _, ok := serviceNames[r.Service]; !ok {
			errs = append(errs, fmt.Sprintf("%s: references unknown service %q", prefix, r.Service))
		}

		// Plugin ordering: detect duplicate order values within a route
		ordersSeen := make(map[int]string)
		for _, pr := range r.Plugins {
			if pr.Name == "" {
				errs = append(errs, fmt.Sprintf("%s: plugin reference has empty name", prefix))
				continue
			}
			if _, ok := pluginNames[pr.Name]; !ok {
				errs = append(errs, fmt.Sprintf("%s: references unknown plugin %q", prefix, pr.Name))
			}
			if existingName, dup := ordersSeen[pr.Order]; dup {
				errs = append(errs, fmt.Sprintf("%s: plugins %q and %q have the same order %d", prefix, existingName, pr.Name, pr.Order))
			}
			ordersSeen[pr.Order] = pr.Name
		}
	}

	if regexCount > maxRegexRoutes {
		errs = append(errs, fmt.Sprintf("regex route count (%d) exceeds max_regex_routes (%d); convert some to prefix routes", regexCount, maxRegexRoutes))
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
