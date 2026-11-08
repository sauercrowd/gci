package host

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

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
	// longest prefix first within the default vhost
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
		App:    "a",
		Images: map[string]*config.Image{"app": {User: "gci"}},
		Services: map[string]*config.Service{
			"web": {Image: "app", Command: "x", Port: 1},
		},
	}
	r1 := revision(cfg, "web", "/nix/store/env", "src1")
	cfg.Services["web"].Routes = []config.Route{{Host: "*", Prefix: "/"}}
	if r2 := revision(cfg, "web", "/nix/store/env", "src1"); r1 != r2 {
		t.Errorf("routes changed revision")
	}
	if r3 := revision(cfg, "web", "/nix/store/env", "src2"); r1 == r3 {
		t.Errorf("source change did not change revision")
	}
	if r4 := revision(cfg, "web", "/nix/store/other", "src1"); r1 == r4 {
		t.Errorf("env change did not change revision")
	}
	cfg.Images["app"].Env = map[string]string{"MODE": "production"}
	if r5 := revision(cfg, "web", "/nix/store/env", "src1"); r1 == r5 {
		t.Errorf("image env change did not change revision")
	}
}

func TestRevisionIgnoresImageSourcePath(t *testing.T) {
	newConfig := func(source string) *config.Config {
		return &config.Config{
			App:    "a",
			Images: map[string]*config.Image{"app": {User: "gci", Source: source}},
			Services: map[string]*config.Service{
				"web": {Image: "app", Command: "x"},
			},
		}
	}
	r1 := revision(newConfig("/checkout/one"), "web", "/nix/store/env", "source-hash")
	r2 := revision(newConfig("/checkout/two"), "web", "/nix/store/env", "source-hash")
	if r1 != r2 {
		t.Fatalf("image source path changed revision: %q != %q", r1, r2)
	}
}

func TestPruneReleases(t *testing.T) {
	now := time.Now()
	rels := []*Release{
		{Key: "one", Created: now},
		{Key: "two", Created: now.Add(time.Minute)},
		{Key: "three", Created: now.Add(2 * time.Minute)},
		{Key: "four", Created: now.Add(3 * time.Minute)},
		{Key: "five", Created: now.Add(4 * time.Minute)},
	}
	referenced := releaseReferences(&AppState{Services: map[string]*ServiceState{
		"web": {Image: "app", Release: "one"},
	}})
	if !referenced["app"]["one"] || referenced["other"] != nil {
		t.Fatalf("release references = %#v", referenced)
	}
	kept, dropped := pruneReleases(rels, referenced["app"], 3)
	if len(kept) != 4 || kept[0].Key != "one" || kept[1].Key != "three" || kept[2].Key != "four" || kept[3].Key != "five" {
		t.Fatalf("kept = %#v", kept)
	}
	if len(dropped) != 1 || dropped[0].Key != "two" {
		t.Fatalf("dropped = %#v", dropped)
	}
}

func TestServiceEnvInheritsImageEnv(t *testing.T) {
	vars := serviceEnvVars("app", "web",
		&config.Service{Image: "app", Command: "run", Env: map[string]string{"SHARED": "service"}},
		&config.Image{User: "root", Env: map[string]string{"IMAGE_ONLY": "image", "SHARED": "image"}},
		"/nix/store/env")
	if vars["IMAGE_ONLY"] != "image" || vars["SHARED"] != "service" || vars["HOME"] != "/root" {
		t.Fatalf("runtime vars = %#v", vars)
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
