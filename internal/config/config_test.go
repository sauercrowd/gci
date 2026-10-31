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
	p := write(t, `
app = "demo"
[services.web]
command = "run"
source = "./app"
port = 8080
depends_on = ["db", "cache"]
routes = [{}]
[services.db]
command = "db"
volumes = { data = "/data" }
[services.cache]
command = "cache"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Nixpkgs != DefaultNixpkgs {
		t.Errorf("nixpkgs default not applied")
	}
	web := cfg.Services["web"]
	if web.User != "gci" || web.Reuse != "dependencies" || web.Routes[0] != (Route{Host: "*", Prefix: "/"}) {
		t.Errorf("defaults not applied: %+v", web)
	}
	if web.Source != filepath.Join(filepath.Dir(p), "app") {
		t.Errorf("source not resolved relative to config: %s", web.Source)
	}
	if !cfg.Services["db"].Stateful() || web.Stateful() {
		t.Errorf("stateful detection wrong")
	}
	order, _ := cfg.Order()
	if want := []string{"cache", "db", "web"}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestValidate(t *testing.T) {
	cases := map[string]string{
		"must match":                       `app = "Bad_Name"` + "\n[services.a]\ncommand = \"x\"",
		"command is required":              `app = "a"` + "\n[services.a]\nport = 1",
		"unknown service":                  `app = "a"` + "\n[services.a]\ncommand = \"x\"\ndepends_on = [\"b\"]",
		"cycle":                            `app = "a"` + "\n[services.a]\ncommand = \"x\"\ndepends_on = [\"b\"]\n[services.b]\ncommand = \"x\"\ndepends_on = [\"a\"]",
		"routes require port":              `app = "a"` + "\n[services.a]\ncommand = \"x\"\nroutes = [{}]",
		"used by both":                     `app = "a"` + "\n[services.a]\ncommand = \"x\"\nvolumes = { v = \"/v\" }\n[services.b]\ncommand = \"x\"\nvolumes = { v = \"/w\" }",
		"health requires port":             `app = "a"` + "\n[services.a]\ncommand = \"x\"\nhealth = \"/h\"",
		"environment variable":             `app = "a"` + "\n[services.a]\ncommand = \"x\"\nenv = { \"bad-env\" = \"value\" }",
		"secret environment":               `app = "a"` + "\n[services.a]\ncommand = \"x\"\nsecrets = { \"bad-env\" = \"valid\" }",
		"reuse must be":                    `app = "a"` + "\n[services.a]\ncommand = \"x\"\nreuse = \"lockfile\"",
		"install and build require source": `app = "a"` + "\n[services.a]\ncommand = \"x\"\ninstall = \"npm install\"",
		"secret name":                      `app = "a"` + "\n[services.a]\ncommand = \"x\"\nsecrets = { TOKEN = \"Invalid_Name\" }",
		"also defined in env":              `app = "a"` + "\n[services.a]\ncommand = \"x\"\nenv = { TOKEN = \"x\" }\nsecrets = { TOKEN = \"token\" }",
		"build require source":             `app = "a"` + "\n[services.a]\ncommand = \"x\"\nbuild = \"npm run build\"",
	}
	for want, content := range cases {
		_, err := Load(write(t, content))
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), want) {
			t.Errorf("%q: got %v", want, err)
		}
	}
}

func TestReuseDefaultAndInvalidValue(t *testing.T) {
	cfg, err := Load(write(t, `
app = "demo"
[services.web]
command = "node server.js"
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["web"].Reuse; got != "dependencies" {
		t.Fatalf("reuse = %q, want dependencies", got)
	}

	_, err = Load(write(t, `
app = "demo"
[services.web]
command = "node server.js"
reuse = "lockfile"
`))
	if err == nil || !strings.Contains(err.Error(), "reuse must be") {
		t.Fatalf("invalid reuse error = %v", err)
	}
}

func TestValidateSecretName(t *testing.T) {
	for _, name := range []string{"ok", "secret.1", "secret-name_2"} {
		if err := ValidateSecretName(name); err != nil {
			t.Errorf("%q: %v", name, err)
		}
	}
	for _, name := range []string{"", "Upper", "has space", strings.Repeat("a", 64), "-leading"} {
		if err := ValidateSecretName(name); err == nil {
			t.Errorf("%q unexpectedly valid", name)
		}
	}
}
