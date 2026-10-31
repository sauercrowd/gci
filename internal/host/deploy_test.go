package host

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sauercrowd/gci/internal/config"
)

func TestRevisionIgnoresRoutes(t *testing.T) {
	cfg := &config.Config{
		App: "demo",
		Services: map[string]*config.Service{
			"web": {Command: "serve", Port: 8080, Routes: []config.Route{{Host: "one.test", Prefix: "/"}}},
		},
	}
	first := revision(cfg, "web", "/nix/store/env")
	cfg.Services["web"].Routes = []config.Route{{Host: "two.test", Prefix: "/api"}}
	if got := revision(cfg, "web", "/nix/store/env"); got != first {
		t.Fatalf("route change changed revision: %s != %s", got, first)
	}
	cfg.Services["web"].Command = "serve --debug"
	if got := revision(cfg, "web", "/nix/store/env"); got == first {
		t.Fatal("command change did not change revision")
	}
}

func TestEnvoyResourcesIncludeStableVIPAndRoutes(t *testing.T) {
	states := []*AppState{{
		App: "demo",
		Services: map[string]*ServiceState{
			"web": {
				IP: "10.77.0.10", VIP: "10.77.1.10", Port: 8080, HTTP: true,
				Routes: []config.Route{{Host: "demo.test", Prefix: "/api"}},
			},
		},
	}}
	lds, cds, err := envoyResources(states, 80, "10.77.0.2")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"demo.test", "/api", "10.77.1.10", "svc_demo_web"} {
		if !strings.Contains(string(lds), want) {
			t.Errorf("LDS missing %q", want)
		}
	}
	var clusters struct {
		Resources []struct {
			Name string `json:"name"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(cds, &clusters); err != nil {
		t.Fatal(err)
	}
	if len(clusters.Resources) != 1 || clusters.Resources[0].Name != "demo_web" {
		t.Fatalf("CDS resources = %+v", clusters.Resources)
	}
}

func TestRenderUnitIncludesExecStartPre(t *testing.T) {
	unit := renderUnit(unitSpec{
		User: "gci", ExecPre: []string{"+/bin/chown gci: /data"},
		ExecStart: "/bin/sh /etc/gci/run.sh",
	})
	if !strings.Contains(unit, "ExecStartPre=+/bin/chown gci: /data\n") {
		t.Fatalf("missing volume ownership step:\n%s", unit)
	}
}
