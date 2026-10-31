package host

import (
	"fmt"
	"sort"

	"github.com/sauercrowd/gci/internal/config"
)

type routeOwner struct {
	app     string
	service string
}

func validateCrossAppRoutes(states []*AppState, cfg *config.Config) error {
	owners := map[string]routeOwner{}
	hostSettings := map[string]struct {
		route RouteTLSSettings
		owner routeOwner
	}{}
	for _, st := range states {
		if st.App == cfg.App {
			continue
		}
		for _, service := range sortedKeys(st.Services) {
			for _, r := range st.Services[service].Routes {
				owner := routeOwner{app: st.App, service: service}
				owners[r.Host+"\x00"+r.Prefix] = owner
				if _, ok := hostSettings[r.Host]; !ok {
					hostSettings[r.Host] = struct {
						route RouteTLSSettings
						owner routeOwner
					}{tlsSettings(r), owner}
				}
			}
		}
	}
	services := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		services = append(services, name)
	}
	sort.Strings(services)
	for _, service := range services {
		for _, r := range cfg.Services[service].Routes {
			if owner, ok := owners[r.Host+"\x00"+r.Prefix]; ok {
				return fmt.Errorf("route %s%s is already used by app %q service %q", r.Host, r.Prefix, owner.app, owner.service)
			}
			if existing, ok := hostSettings[r.Host]; ok && existing.route != tlsSettings(r) {
				return fmt.Errorf("route host %q has conflicting tls settings with app %q service %q", r.Host, existing.owner.app, existing.owner.service)
			}
		}
	}
	return nil
}

type RouteTLSSettings struct {
	Mode string
	Cert string
	Key  string
}

func tlsSettings(route config.Route) RouteTLSSettings {
	mode := route.TLS
	if mode == "" {
		mode = config.DefaultTLSMode(route.Host)
	}
	return RouteTLSSettings{Mode: mode, Cert: route.Cert, Key: route.Key}
}
