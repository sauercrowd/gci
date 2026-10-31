package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"time"
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
      path: %s/lds/lds.json
      watched_directory: { path: %s/lds }
  cds_config:
    resource_api_version: V3
    path_config_source:
      path: %s/cds/cds.json
      watched_directory: { path: %s/cds }
admin:
  address:
    socket_address: { address: 127.0.0.1, port_value: %d }
`, envoyConfigMount, envoyConfigMount, envoyConfigMount, envoyConfigMount, adminPort)
}

func writeEnvoyConfig(port int) error {
	hcm := map[string]any{
		"@type":       "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
		"stat_prefix": "http",
		"route_config": map[string]any{"name": "gci", "virtual_hosts": []any{
			map[string]any{"name": "default", "domains": []string{"*"}, "routes": []any{
				map[string]any{"match": map[string]any{"prefix": "/"}, "direct_response": map[string]any{"status": 404}},
			}},
		}},
		"http_filters": []any{map[string]any{
			"name":         "envoy.filters.http.router",
			"typed_config": map[string]any{"@type": "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router"},
		}},
	}
	listener := map[string]any{
		"@type": "type.googleapis.com/envoy.config.listener.v3.Listener",
		"name":  "http",
		"address": map[string]any{"socket_address": map[string]any{
			"address": "0.0.0.0", "port_value": port,
		}},
		"filter_chains": []any{map[string]any{"filters": []any{map[string]any{
			"name": "envoy.filters.network.http_connection_manager", "typed_config": hcm,
		}}}},
	}
	lds, err := json.MarshalIndent(map[string]any{"resources": []any{listener}}, "", "  ")
	if err != nil {
		return err
	}
	cds, err := json.MarshalIndent(map[string]any{"resources": []any{}}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Join(envoyDir(), "config")
	if err := writeFileAtomic(filepath.Join(dir, "lds", "lds.json"), lds, 0o644); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, "cds", "cds.json"), cds, 0o644)
}

func waitTCP(ctx context.Context, address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("%s: TCP connection timed out", address)
}
