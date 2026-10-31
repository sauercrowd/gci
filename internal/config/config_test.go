package config

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
	if web.User != "gci" || web.Reuse != "dependencies" || web.Routes[0] != (Route{Host: "*", Prefix: "/", TLS: "self-signed"}) {
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
		"require source":                   `app = "a"` + "\n[services.a]\ncommand = \"x\"\ninstall = \"npm install\"",
		"reuse must be":                    `app = "a"` + "\n[services.a]\ncommand = \"x\"\nreuse = \"invalid\"",
		"secret environment":               `app = "a"` + "\n[services.a]\ncommand = \"x\"\nsecrets = { \"bad-env\" = \"valid\" }",
		"environment variable":             `app = "a"` + "\n[services.a]\ncommand = \"x\"\nenv = { \"bad-env\" = \"value\" }",
		"secret name":                      `app = "a"` + "\n[services.a]\ncommand = \"x\"\nsecrets = { TOKEN = \"Invalid_Name\" }",
		"also defined in env":              `app = "a"` + "\n[services.a]\ncommand = \"x\"\nenv = { TOKEN = \"x\" }\nsecrets = { TOKEN = \"token\" }",
		"build require source":             `app = "a"` + "\n[services.a]\ncommand = \"x\"\nbuild = \"npm run build\"",
		"unknown service":                  `app = "a"` + "\n[services.a]\ncommand = \"x\"\ndepends_on = [\"b\"]",
		"cycle":                            `app = "a"` + "\n[services.a]\ncommand = \"x\"\ndepends_on = [\"b\"]\n[services.b]\ncommand = \"x\"\ndepends_on = [\"a\"]",
		"routes require port":              `app = "a"` + "\n[services.a]\ncommand = \"x\"\nroutes = [{}]",
		"used by both":                     `app = "a"` + "\n[services.a]\ncommand = \"x\"\nvolumes = { v = \"/v\" }\n[services.b]\ncommand = \"x\"\nvolumes = { v = \"/w\" }",
		"health requires port":             `app = "a"` + "\n[services.a]\ncommand = \"x\"\nhealth = \"/h\"",
		"health_command must not be blank": `app = "a"` + "\n[services.a]\ncommand = \"x\"\nhealth_command = \"   \"",
		"missing in the target":            `app = "a"` + "\n[services.a]\ncommand = \"x\"\nimage = \"nginx\"",
	}
	for want, content := range cases {
		_, err := Load(write(t, content))
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), want) {
			t.Errorf("%q: got %v", want, err)
		}
	}
}

func TestHealthCommandAllowsPortlessService(t *testing.T) {
	cfg, err := Load(write(t, `
app = "demo"
[services.check]
command = "sleep infinity"
health_command = "test -n \"$GCI_APP\""
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["check"].HealthCommand; got != `test -n "$GCI_APP"` {
		t.Fatalf("health_command = %q", got)
	}
}

func TestRouteTLSDefaultsByHostKind(t *testing.T) {
	hosts := map[string]string{
		"*":               "self-signed",
		"*.example.com":   "self-signed",
		"localhost":       "self-signed",
		"api.localhost":   "self-signed",
		"127.0.0.1":       "self-signed",
		"[2001:db8::1]":   "self-signed",
		"example.com":     "acme",
		"api.example.com": "acme",
	}
	var content strings.Builder
	content.WriteString("app = \"demo\"\n")
	for i, host := range sortedHostNames(hosts) {
		content.WriteString("[services.s" + string(rune('a'+i)) + "]\n")
		content.WriteString("command = \"run\"\nport = 8080\nroutes = [{ host = \"" + host + "\" }]\n")
	}
	cfg, err := Load(write(t, content.String()))
	if err != nil {
		t.Fatal(err)
	}
	for i, host := range sortedHostNames(hosts) {
		if got, want := cfg.Services["s"+string(rune('a'+i))].Routes[0].TLS, hosts[host]; got != want {
			t.Errorf("%s TLS = %q, want %q", host, got, want)
		}
	}
}

func sortedHostNames(m map[string]string) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestRouteTLSValidation(t *testing.T) {
	cases := map[string]string{
		"unknown tls mode":            `app = "demo"` + "\n[services.web]\ncommand = \"run\"\nport = 8080\nroutes = [{ host = \"example.com\", tls = \"magic\" }]",
		"wildcard":                    `app = "demo"` + "\n[services.web]\ncommand = \"run\"\nport = 8080\nroutes = [{ host = \"*.example.com\", tls = \"acme\" }]",
		"localhost":                   `app = "demo"` + "\n[services.web]\ncommand = \"run\"\nport = 8080\nroutes = [{ host = \"localhost\", tls = \"acme\" }]",
		"IP":                          `app = "demo"` + "\n[services.web]\ncommand = \"run\"\nport = 8080\nroutes = [{ host = \"127.0.0.1\", tls = \"acme\" }]",
		"requires both cert and key":  `app = "demo"` + "\n[services.web]\ncommand = \"run\"\nport = 8080\nroutes = [{ host = \"example.com\", tls = \"custom\", cert = \"leaf\" }]",
		"cert and key are only valid": `app = "demo"` + "\n[services.web]\ncommand = \"run\"\nport = 8080\nroutes = [{ host = \"example.com\", cert = \"leaf\", key = \"private\" }]",
		"secret name":                 `app = "demo"` + "\n[services.web]\ncommand = \"run\"\nport = 8080\nroutes = [{ host = \"example.com\", tls = \"custom\", cert = \"Leaf\", key = \"private\" }]",
		"inconsistent tls":            `app = "demo"` + "\n[services.web]\ncommand = \"run\"\nport = 8080\nroutes = [{ host = \"example.com\", prefix = \"/\", tls = \"acme\" }, { host = \"example.com\", prefix = \"/api\", tls = \"off\" }]",
		"already used by app":         `app = "demo"` + "\n[services.web]\ncommand = \"run\"\nport = 8080\nroutes = [{ host = \"example.com\" }]\n[services.api]\ncommand = \"run\"\nport = 8081\nroutes = [{ host = \"example.com\" }]",
	}
	for want, content := range cases {
		_, err := Load(write(t, content))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v", want, err)
		}
	}
}

func TestCustomRouteRequiresBothSecretNames(t *testing.T) {
	for _, route := range []string{
		`{ host = "example.com", tls = "custom" }`,
		`{ host = "example.com", tls = "custom", cert = "leaf" }`,
		`{ host = "example.com", tls = "custom", key = "private" }`,
	} {
		content := "app = \"demo\"\n[services.web]\ncommand = \"run\"\nport = 8080\nroutes = [" + route + "]\n"
		_, err := Load(write(t, content))
		if err == nil || !strings.Contains(err.Error(), "requires both cert and key") {
			t.Errorf("%s: got %v", route, err)
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

func TestMigrateWithVolumesIsAllowed(t *testing.T) {
	cfg, err := Load(write(t, `
app = "demo"
[services.db]
command = "postgres"
migrate = "migrate"
volumes = { data = "/var/lib/data" }
`))
	if err != nil {
		t.Fatalf("migrate with volumes: %v", err)
	}
	if !cfg.Services["db"].Stateful() {
		t.Fatal("service with volumes is not stateful")
	}
}

func TestMigrateCommandMustNotBeBlank(t *testing.T) {
	_, err := Load(write(t, `
app = "demo"
[services.web]
command = "server"
migrate = "   "
`))
	if err == nil || !strings.Contains(err.Error(), "migrate must not be blank") {
		t.Fatalf("blank migrate error = %v", err)
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
