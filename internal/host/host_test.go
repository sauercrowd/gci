package host

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sauercrowd/gci/internal/config"
)

func TestEnvoyResources(t *testing.T) {
	states := []*AppState{{
		App: "demo",
		Services: map[string]*ServiceState{
			"web": {IP: "10.0.0.2", Port: 8080, Routes: []config.Route{{Host: "*", Prefix: "/"}}},
			"api": {IP: "10.0.0.3", Port: 9000, Routes: []config.Route{{Host: "*", Prefix: "/api"}, {Host: "api.example.com", Prefix: "/"}}},
			"db":  {IP: "10.0.0.4", Port: 5432},
		},
	}}
	states[0].Services["db"].VIP = "10.77.1.4"
	lds, cds, err := envoyResources(states, 10080, "10.77.0.2")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Resources []struct{ Name string } `json:"resources"`
	}
	if err := json.Unmarshal(cds, &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Resources) != 3 {
		t.Errorf("want 3 clusters, got %d", len(c.Resources))
	}
	s := string(lds)
	if strings.Index(s, `"prefix": "/api"`) > strings.Index(s, `"cluster": "demo_web"`) {
		t.Errorf("/api route must precede / route:\n%s", s)
	}
	for _, want := range []string{`"db.demo.internal"`, `"10.77.1.4"`, `"svc_demo_db"`, "tcp_proxy", "dns_filter"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in LDS", want)
		}
	}
	if !strings.Contains(s, `"api.example.com"`) || !strings.Contains(s, `"port_value": 10080`) {
		t.Errorf("missing host vhost or listener port:\n%s", s)
	}
}

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

func TestEnvFileQuoting(t *testing.T) {
	got := renderEnvFile(map[string]string{"B": `say "hi"`, "A": "1", "TOKEN": "secret-value"})
	if got != "A=\"1\"\nB=\"say \\\"hi\\\"\"\nTOKEN=\"secret-value\"\n" {
		t.Errorf("got %q", got)
	}
	if shellScript("run me") != "#!/bin/sh\nset -e\nexec run me\n" {
		t.Errorf("single-line command should exec")
	}
	if s := shellScript("cd /app && run"); strings.Contains(s, "exec") {
		t.Errorf("compound command must not be exec'd: %q", s)
	}
}
