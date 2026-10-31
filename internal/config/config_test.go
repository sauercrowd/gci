package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "gci.toml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaultsAndOrder(t *testing.T) {
	cfg, err := Load(write(t, `
app = "demo"
[services.web]
command = "run"
port = 8080
depends_on = ["db", "cache"]
routes = [{}]
[services.db]
command = "db"
volumes = { data = "/data" }
[services.cache]
command = "cache"
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Nixpkgs != DefaultNixpkgs {
		t.Fatal("nixpkgs default not applied")
	}
	web := cfg.Services["web"]
	if web.User != "gci" || web.Routes[0] != (Route{Host: "*", Prefix: "/"}) {
		t.Fatalf("defaults not applied: %+v", web)
	}
	if !cfg.Services["db"].Stateful() || web.Stateful() {
		t.Fatal("stateful detection is wrong")
	}
	order, err := cfg.Order()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"cache", "db", "web"}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestValidate(t *testing.T) {
	cases := map[string]string{
		"must match":                         `app = "Bad_Name"` + "\n[services.a]\ncommand = \"x\"",
		"command is required":                `app = "a"` + "\n[services.a]\nport = 1",
		"unknown service":                    `app = "a"` + "\n[services.a]\ncommand = \"x\"\ndepends_on = [\"b\"]",
		"cycle":                              `app = "a"` + "\n[services.a]\ncommand = \"x\"\ndepends_on = [\"b\"]\n[services.b]\ncommand = \"x\"\ndepends_on = [\"a\"]",
		"routes require port":                `app = "a"` + "\n[services.a]\ncommand = \"x\"\nroutes = [{}]",
		"used by both":                       `app = "a"` + "\n[services.a]\ncommand = \"x\"\nvolumes = { v = \"/v\" }\n[services.b]\ncommand = \"x\"\nvolumes = { v = \"/w\" }",
		"health requires port":               `app = "a"` + "\n[services.a]\ncommand = \"x\"\nhealth = \"/h\"",
		"environment variable":               `app = "a"` + "\n[services.a]\ncommand = \"x\"\nenv = { \"bad-env\" = \"value\" }",
		"fields in the document are missing": `app = "a"` + "\n[services.a]\ncommand = \"x\"\nsource = \"app\"",
	}
	for want, content := range cases {
		_, err := Load(write(t, content))
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), want) {
			t.Errorf("%q: got %v", want, err)
		}
	}
}

func TestValidateEnvironmentName(t *testing.T) {
	cfg := &Config{
		App: "demo",
		Services: map[string]*Service{
			"web": {Command: "run", Env: map[string]string{"bad-name": "value"}},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid environment name to fail")
	}
}
