package host

import (
	"strings"
	"testing"

	"github.com/sauercrowd/gci/internal/config"
)

func TestCrossAppRouteClash(t *testing.T) {
	states := []*AppState{{
		App: "shop",
		Services: map[string]*ServiceState{
			"web": {Routes: []config.Route{{Host: "example.com", Prefix: "/", TLS: "acme"}}},
		},
	}}
	cfg := &config.Config{
		App: "api",
		Services: map[string]*config.Service{
			"web": {Routes: []config.Route{{Host: "example.com", Prefix: "/", TLS: "acme"}}},
		},
	}
	err := validateCrossAppRoutes(states, cfg)
	if err == nil || !strings.Contains(err.Error(), `route example.com/ is already used by app "shop" service "web"`) {
		t.Fatalf("got %v", err)
	}
}

func TestCrossAppTLSModeMismatch(t *testing.T) {
	states := []*AppState{{
		App: "shop",
		Services: map[string]*ServiceState{
			"web": {Routes: []config.Route{{Host: "example.com", Prefix: "/", TLS: "acme"}}},
		},
	}}
	cfg := &config.Config{
		App: "api",
		Services: map[string]*config.Service{
			"web": {Routes: []config.Route{{Host: "example.com", Prefix: "/api", TLS: "off"}}},
		},
	}
	err := validateCrossAppRoutes(states, cfg)
	if err == nil || !strings.Contains(err.Error(), `conflicting tls settings with app "shop" service "web"`) {
		t.Fatalf("got %v", err)
	}
}

func TestCrossAppValidationIgnoresCurrentAppState(t *testing.T) {
	states := []*AppState{{
		App: "demo",
		Services: map[string]*ServiceState{
			"web": {Routes: []config.Route{{Host: "example.com", Prefix: "/", TLS: "acme"}}},
		},
	}}
	cfg := &config.Config{
		App: "demo",
		Services: map[string]*config.Service{
			"web": {Routes: []config.Route{{Host: "example.com", Prefix: "/", TLS: "acme"}}},
		},
	}
	if err := validateCrossAppRoutes(states, cfg); err != nil {
		t.Fatal(err)
	}
}
