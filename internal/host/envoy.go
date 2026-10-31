package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const envoyConfigMount = "/etc/gci-envoy"
const envoyHTTPSListenPort = 10443

func envoyBootstrap(adminPort int) string {
	return fmt.Sprintf(`node:
  id: gci
  cluster: gci
dynamic_resources:
  lds_config:
    resource_api_version: V3
    path_config_source:
      path: %[1]s/lds/lds.json
      watched_directory: { path: %[1]s/lds }
  cds_config:
    resource_api_version: V3
    path_config_source:
      path: %[1]s/cds/cds.json
      watched_directory: { path: %[1]s/cds }
admin:
  address:
    socket_address: { address: 127.0.0.1, port_value: %[2]d }
`, envoyConfigMount, adminPort)
}

type any_ = map[string]any
type envoyRoute struct {
	prefix, cluster string
}

func clusterName(app, svc string) string { return app + "_" + svc }

func socketAddress(ip string, port int) any_ {
	return any_{"socket_address": any_{"address": ip, "port_value": port}}
}

// envoyResources renders the LDS and CDS documents for every app on this host.
func envoyResources(states []*AppState, httpPort, httpsPort int, gateway, envoyIP string) (lds, cds []byte, err error) {
	type hostVHost struct {
		routes []envoyRoute
		tls    RouteTLSSettings
	}
	vhosts := map[string]*hostVHost{}
	wildcardConfigured := false
	var clusters, listeners, dnsNames []any
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
			if s.VIP != "" {
				fqdn := internalName(st.App, name)
				dnsNames = append(dnsNames, any_{"name": fqdn, "endpoint": any_{"address_list": any_{"address": []string{s.VIP}}}})
				listeners = append(listeners, serviceListener(st.App, name, s, cn))
			}
			for _, r := range s.Routes {
				if r.Host == "*" {
					wildcardConfigured = true
				}
				vh := vhosts[r.Host]
				if vh == nil {
					vh = &hostVHost{tls: tlsSettings(r)}
					vhosts[r.Host] = vh
				}
				vh.routes = append(vh.routes, envoyRoute{r.Prefix, cn})
			}
		}
	}
	if _, ok := vhosts["*"]; !ok {
		vhosts["*"] = &hostVHost{tls: RouteTLSSettings{Mode: "self-signed"}}
	}
	hosts := make([]string, 0, len(vhosts))
	for h := range vhosts {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	var httpVHosts []any
	for _, h := range hosts {
		vh := vhosts[h]
		routes := make([]any, 0, len(vh.routes)+2)
		routes = append(routes, acmeChallengeRoute())
		if h == "*" && !wildcardConfigured {
			routes = append(routes, noRouteRoute())
		} else if vh.tls.Mode == "off" {
			routes = append(routes, renderServiceRoutes(vh.routes)...)
		} else {
			redirect := any_{"https_redirect": true}
			if httpsPort != 443 {
				redirect["port_redirect"] = httpsPort
			}
			routes = append(routes, any_{"match": any_{"prefix": "/"}, "redirect": redirect})
			routes = append(routes, noRouteRoute())
		}
		if h != "*" || wildcardConfigured {
			routes = append(routes, noRouteRoute())
		}
		httpVHosts = append(httpVHosts, any_{"name": vhostName(h), "domains": []string{h}, "routes": routes})
	}
	acmeCluster := any_{
		"@type":           "type.googleapis.com/envoy.config.cluster.v3.Cluster",
		"name":            "gci_acme",
		"type":            "STATIC",
		"connect_timeout": "2s",
		"load_assignment": any_{
			"cluster_name": "gci_acme",
			"endpoints": []any{any_{"lb_endpoints": []any{
				any_{"endpoint": any_{"address": socketAddress(gateway, 8402)}},
			}}},
		},
	}
	clusters = append(clusters, acmeCluster)
	httpListener := any_{
		"@type":   "type.googleapis.com/envoy.config.listener.v3.Listener",
		"name":    "http",
		"address": socketAddress("0.0.0.0", httpPort),
		"filter_chains": []any{any_{"filters": []any{any_{
			"name":         "envoy.filters.network.http_connection_manager",
			"typed_config": edgeHTTPManager("http", httpVHosts, false),
		}}}},
	}
	httpsFilterChains := make([]any, 0, len(hosts))
	for _, h := range hosts {
		if h == "*" || vhosts[h].tls.Mode == "off" {
			continue
		}
		vh := renderTLSVHost(h, vhosts[h].routes)
		httpsFilterChains = append(httpsFilterChains, any_{
			"filter_chain_match": any_{"server_names": []string{h}},
			"transport_socket":   downstreamTLSSocket(h),
			"filters": []any{any_{
				"name":         "envoy.filters.network.http_connection_manager",
				"typed_config": edgeHTTPManager("https_"+vhostName(h), []any{vh}, true),
			}},
		})
	}
	defaultVHosts := []any{}
	if wildcard := vhosts["*"]; wildcardConfigured && wildcard.tls.Mode != "off" {
		defaultVHosts = append(defaultVHosts, renderTLSVHost("*", wildcard.routes))
	} else {
		defaultVHosts = append(defaultVHosts, noRouteVHost())
	}
	httpsListener := any_{
		"@type":   "type.googleapis.com/envoy.config.listener.v3.Listener",
		"name":    "https",
		"address": socketAddress("0.0.0.0", envoyHTTPSListenPort),
		"listener_filters": []any{any_{
			"name":         "envoy.filters.listener.tls_inspector",
			"typed_config": any_{"@type": "type.googleapis.com/envoy.extensions.filters.listener.tls_inspector.v3.TlsInspector"},
		}},
		"filter_chains": httpsFilterChains,
		"default_filter_chain": any_{
			"transport_socket": downstreamTLSSocket("*"),
			"filters": []any{any_{
				"name":         "envoy.filters.network.http_connection_manager",
				"typed_config": edgeHTTPManager("https_default", defaultVHosts, true),
			}},
		},
	}
	if clusters == nil {
		clusters = []any{}
	}
	listeners = append([]any{httpListener, httpsListener}, listeners...)
	if envoyIP != "" {
		if dnsNames == nil {
			dnsNames = []any{}
		}
		listeners = append(listeners, any_{
			"@type":   "type.googleapis.com/envoy.config.listener.v3.Listener",
			"name":    "dns",
			"address": any_{"socket_address": any_{"protocol": "UDP", "address": envoyIP, "port_value": 53}},
			"listener_filters": []any{any_{
				"name": "envoy.filters.udp.dns_filter",
				"typed_config": any_{
					"@type":         "type.googleapis.com/envoy.extensions.filters.udp.dns_filter.v3.DnsFilterConfig",
					"stat_prefix":   "dns",
					"server_config": any_{"inline_dns_table": any_{"virtual_domains": dnsNames}},
				},
			}},
		})
	}
	lds, err = json.MarshalIndent(any_{"resources": listeners}, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	cds, err = json.MarshalIndent(any_{"resources": clusters}, "", "  ")
	return lds, cds, err
}

func acmeChallengeRoute() any {
	return any_{
		"match": any_{"prefix": "/.well-known/acme-challenge/"},
		"route": any_{"cluster": "gci_acme", "timeout": "10s"},
	}
}

func renderServiceRoutes(routes []envoyRoute) []any {
	sort.SliceStable(routes, func(i, j int) bool { return len(routes[i].prefix) > len(routes[j].prefix) })
	var rendered []any
	for _, r := range routes {
		rendered = append(rendered, any_{
			"match": any_{"prefix": r.prefix},
			"route": any_{
				"cluster":         r.cluster,
				"timeout":         "60s",
				"upgrade_configs": []any{any_{"upgrade_type": "websocket"}},
			},
		})
	}
	return rendered
}

func noRouteRoute() any {
	return any_{"match": any_{"prefix": "/"}, "direct_response": any_{"status": 404, "body": any_{"inline_string": "no route\n"}}}
}

func noRouteVHost() any {
	return any_{"name": "no_route", "domains": []string{"*"}, "routes": []any{noRouteRoute()}}
}

func renderTLSVHost(host string, routes []envoyRoute) any {
	rendered := renderServiceRoutes(routes)
	rendered = append(rendered, noRouteRoute())
	return any_{"name": vhostName(host), "domains": []string{host}, "routes": rendered}
}

func edgeHTTPManager(name string, virtualHosts []any, https bool) any_ {
	manager := any_{
		"@type":               "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
		"stat_prefix":         name,
		"access_log":          accessLog(),
		"strip_any_host_port": true,
		"route_config":        any_{"name": name, "virtual_hosts": virtualHosts},
		"http_filters": []any{any_{
			"name":         "envoy.filters.http.router",
			"typed_config": any_{"@type": "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router"},
		}},
	}
	if https {
		manager["request_headers_to_add"] = []any{any_{
			"header":        any_{"key": "x-forwarded-proto", "value": "https"},
			"append_action": "OVERWRITE_IF_EXISTS_OR_ADD",
		}}
	}
	return manager
}

func downstreamTLSSocket(host string) any_ {
	name := certificateName(host)
	return any_{
		"name": "envoy.transport_sockets.tls",
		"typed_config": any_{
			"@type": "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.DownstreamTlsContext",
			"common_tls_context": any_{
				"alpn_protocols": []string{"h2", "http/1.1"},
				"tls_certificate_sds_secret_configs": []any{any_{
					"name": name,
					"sds_config": any_{
						"resource_api_version": "V3",
						"path_config_source": any_{
							"path":              "/etc/gci/certs/" + name + ".json",
							"watched_directory": any_{"path": "/etc/gci/certs"},
						},
					},
				}},
			},
		},
	}
}

// internalName is the DNS name other services use, e.g. postgres.platform.internal.
func internalName(app, svc string) string { return svc + "." + app + ".internal" }

func accessLog() []any {
	return []any{any_{
		"name":         "envoy.access_loggers.stdout",
		"typed_config": any_{"@type": "type.googleapis.com/envoy.extensions.access_loggers.stream.v3.StdoutAccessLog"},
	}}
}

// serviceListener binds the service's VIP:port. HTTP services (those with a
// health path) get an HTTP proxy so in-flight requests drain on rollout;
// everything else is proxied as plain TCP.
func serviceListener(app, name string, s *ServiceState, cluster string) any {
	var filter any
	if s.HTTP {
		filter = any_{
			"name": "envoy.filters.network.http_connection_manager",
			"typed_config": any_{
				"@type":       "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
				"stat_prefix": "svc_" + cluster,
				"access_log":  accessLog(),
				"route_config": any_{"name": cluster, "virtual_hosts": []any{any_{
					"name": cluster, "domains": []string{"*"},
					"routes": []any{any_{
						"match": any_{"prefix": "/"},
						"route": any_{"cluster": cluster, "timeout": "60s", "upgrade_configs": []any{any_{"upgrade_type": "websocket"}}},
					}},
				}}},
				"http_filters": []any{any_{
					"name":         "envoy.filters.http.router",
					"typed_config": any_{"@type": "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router"},
				}},
			},
		}
	} else {
		filter = any_{
			"name": "envoy.filters.network.tcp_proxy",
			"typed_config": any_{
				"@type":       "type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy",
				"stat_prefix": "svc_" + cluster,
				"cluster":     cluster,
				"access_log":  accessLog(),
			},
		}
	}
	return any_{
		"@type":         "type.googleapis.com/envoy.config.listener.v3.Listener",
		"name":          "svc_" + cluster,
		"address":       socketAddress(s.VIP, s.Port),
		"filter_chains": []any{any_{"filters": []any{filter}}},
	}
}

func vhostName(host string) string {
	if host == "*" {
		return "default"
	}
	return strings.NewReplacer("*", "wildcard", ".", "_").Replace(host)
}

// writeEnvoyConfig regenerates the front door config; Envoy hot-reloads it.
// CDS is written first so new clusters exist before routes reference them.
func writeEnvoyConfig(ctx context.Context) error {
	states, err := loadAllStates()
	if err != nil {
		return err
	}
	hc, err := loadHostConfig()
	if err != nil {
		return err
	}
	gateway, err := netip.ParsePrefix(hc.Subnet)
	if err != nil {
		return fmt.Errorf("host subnet: %w", err)
	}
	certificates, err := certificatesForStates(states)
	if err != nil {
		return err
	}
	if err := pushSDSCertificates(ctx, certificates); err != nil {
		return err
	}
	lds, cds, err := envoyResources(states, envoyListenPort, hc.HTTPSPort, gateway.Addr().String(), hc.EnvoyIP)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(envoyDir(), "config", "cds", "cds.json"), cds, 0o644); err != nil {
		return err
	}
	waitEnvoyClusters(cds)
	return writeFileAtomic(filepath.Join(envoyDir(), "config", "lds", "lds.json"), lds, 0o644)
}

// waitEnvoyClusters blocks until a running Envoy has loaded every cluster in
// cds; Envoy rejects an LDS update that routes to a cluster it doesn't know.
func waitEnvoyClusters(cds []byte) {
	var doc struct {
		Resources []struct {
			Name string `json:"name"`
		} `json:"resources"`
	}
	if json.Unmarshal(cds, &doc) != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		out, err := incus(ctx, "exec", envoyName, "--", "curl", "-sf", "127.0.0.1:9901/clusters")
		if err != nil {
			return // Envoy not running yet; it reads both files on start.
		}
		loaded := true
		for _, r := range doc.Resources {
			if !strings.Contains(out, r.Name+"::") {
				loaded = false
				break
			}
		}
		if loaded {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
