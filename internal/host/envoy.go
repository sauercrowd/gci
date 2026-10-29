package host

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const envoyConfigMount = "/etc/gci-envoy"

func envoyBootstrap(adminPort int) string {
	return fmt.Sprintf(`node:
  id: gci
  cluster: gci
dynamic_resources:
  lds_config:
    resource_api_version: V3
    path_config_source:
      path: %[1]s/lds.json
      watched_directory: { path: %[1]s }
  cds_config:
    resource_api_version: V3
    path_config_source:
      path: %[1]s/cds.json
      watched_directory: { path: %[1]s }
admin:
  address:
    socket_address: { address: 127.0.0.1, port_value: %[2]d }
`, envoyConfigMount, adminPort)
}

type any_ = map[string]any

func clusterName(app, svc string) string { return app + "_" + svc }

func socketAddress(ip string, port int) any_ {
	return any_{"socket_address": any_{"address": ip, "port_value": port}}
}

// envoyResources renders the LDS and CDS documents for every app on this host.
func envoyResources(states []*AppState, httpPort int) (lds, cds []byte, err error) {
	type route struct {
		prefix, cluster string
	}
	vhosts := map[string][]route{}
	var clusters []any
	for _, st := range states {
		svcs := make([]string, 0, len(st.Services))
		for name := range st.Services {
			svcs = append(svcs, name)
		}
		sort.Strings(svcs)
		for _, name := range svcs {
			s := st.Services[name]
			if s.Port == 0 || s.IP == "" {
				continue
			}
			cn := clusterName(st.App, name)
			clusters = append(clusters, any_{
				"@type":           "type.googleapis.com/envoy.config.cluster.v3.Cluster",
				"name":            cn,
				"type":            "STATIC",
				"connect_timeout": "2s",
				"load_assignment": any_{
					"cluster_name": cn,
					"endpoints": []any{any_{"lb_endpoints": []any{
						any_{"endpoint": any_{"address": socketAddress(s.IP, s.Port)}},
					}}},
				},
			})
			for _, r := range s.Routes {
				vhosts[r.Host] = append(vhosts[r.Host], route{r.Prefix, cn})
			}
		}
	}
	if _, ok := vhosts["*"]; !ok {
		vhosts["*"] = nil
	}
	hosts := make([]string, 0, len(vhosts))
	for h := range vhosts {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	var vhs []any
	for _, h := range hosts {
		rs := vhosts[h]
		sort.SliceStable(rs, func(i, j int) bool { return len(rs[i].prefix) > len(rs[j].prefix) })
		var routes []any
		for _, r := range rs {
			routes = append(routes, any_{
				"match": any_{"prefix": r.prefix},
				"route": any_{
					"cluster":         r.cluster,
					"timeout":         "60s",
					"upgrade_configs": []any{any_{"upgrade_type": "websocket"}},
				},
			})
		}
		routes = append(routes, any_{
			"match":           any_{"prefix": "/"},
			"direct_response": any_{"status": 404, "body": any_{"inline_string": "gci: no route\n"}},
		})
		vhs = append(vhs, any_{"name": vhostName(h), "domains": []string{h}, "routes": routes})
	}
	listener := any_{
		"@type":   "type.googleapis.com/envoy.config.listener.v3.Listener",
		"name":    "http",
		"address": socketAddress("0.0.0.0", httpPort),
		"filter_chains": []any{any_{"filters": []any{any_{
			"name": "envoy.filters.network.http_connection_manager",
			"typed_config": any_{
				"@type":       "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
				"stat_prefix": "http",
				"access_log": []any{any_{
					"name":         "envoy.access_loggers.stdout",
					"typed_config": any_{"@type": "type.googleapis.com/envoy.extensions.access_loggers.stream.v3.StdoutAccessLog"},
				}},
				"route_config": any_{"name": "gci", "virtual_hosts": vhs},
				"http_filters": []any{any_{
					"name":         "envoy.filters.http.router",
					"typed_config": any_{"@type": "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router"},
				}},
			},
		}}}},
	}
	if clusters == nil {
		clusters = []any{}
	}
	lds, err = json.MarshalIndent(any_{"resources": []any{listener}}, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	cds, err = json.MarshalIndent(any_{"resources": clusters}, "", "  ")
	return lds, cds, err
}

func vhostName(host string) string {
	if host == "*" {
		return "default"
	}
	return strings.NewReplacer("*", "wildcard", ".", "_").Replace(host)
}

// writeEnvoyConfig regenerates the front door config; Envoy hot-reloads it.
// CDS is written first so new clusters exist before routes reference them.
func writeEnvoyConfig(httpPort int) error {
	states, err := loadAllStates()
	if err != nil {
		return err
	}
	lds, cds, err := envoyResources(states, httpPort)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(envoyDir(), "config", "cds.json"), cds, 0o644); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(envoyDir(), "config", "lds.json"), lds, 0o644)
}
