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
[images.web]
source = "./app"
[images.db]
[images.cache]
[services.web]
image = "web"
command = "run"
port = 8080
depends_on = ["db", "cache"]
routes = [{}]
[services.db]
image = "db"
command = "db"
volumes = { data = "/data" }
[services.cache]
image = "cache"
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
	if cfg.Images["web"].User != "gci" || web.Routes[0] != (Route{Host: "*", Prefix: "/"}) {
		t.Errorf("defaults not applied: %+v", web)
	}
	if got := cfg.Images["web"].Source; got != filepath.Join(filepath.Dir(p), "app") {
		t.Errorf("image source not resolved relative to config: %s", got)
	}
	if cfg.Images["web"].CarryOver != "" {
		t.Errorf("omitted carry_over defaulted to %q", cfg.Images["web"].CarryOver)
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
		"must match":                       `app = "Bad_Name"` + "\n[images.a]\n[services.a]\nimage = \"a\"\ncommand = \"x\"",
		"command is required":              `app = "a"` + "\n[images.a]\n[services.a]\nimage = \"a\"\nport = 1",
		"unknown service":                  `app = "a"` + "\n[images.a]\n[services.a]\nimage = \"a\"\ncommand = \"x\"\ndepends_on = [\"b\"]",
		"cycle":                            `app = "a"` + "\n[images.app]\n[services.a]\nimage = \"app\"\ncommand = \"x\"\ndepends_on = [\"b\"]\n[services.b]\nimage = \"app\"\ncommand = \"x\"\ndepends_on = [\"a\"]",
		"routes require port":              `app = "a"` + "\n[images.a]\n[services.a]\nimage = \"a\"\ncommand = \"x\"\nroutes = [{}]",
		"used by both":                     `app = "a"` + "\n[images.a]\n[services.a]\nimage = \"a\"\ncommand = \"x\"\nvolumes = { v = \"/v\" }\n[services.b]\nimage = \"a\"\ncommand = \"x\"\nvolumes = { v = \"/w\" }",
		"health requires port":             `app = "a"` + "\n[images.a]\n[services.a]\nimage = \"a\"\ncommand = \"x\"\nhealth = \"/h\"",
		"environment variable":             `app = "a"` + "\n[images.a]\n[services.a]\nimage = \"a\"\ncommand = \"x\"\nenv = { \"bad-env\" = \"value\" }",
		"secret environment":               `app = "a"` + "\n[images.a]\n[services.a]\nimage = \"a\"\ncommand = \"x\"\nsecrets = { \"bad-env\" = \"valid\" }",
		"secret name":                      `app = "a"` + "\n[images.a]\n[services.a]\nimage = \"a\"\ncommand = \"x\"\nsecrets = { TOKEN = \"Invalid_Name\" }",
		"also defined in env":              `app = "a"` + "\n[images.a]\n[services.a]\nimage = \"a\"\ncommand = \"x\"\nenv = { TOKEN = \"x\" }\nsecrets = { TOKEN = \"token\" }",
		"image is required":                `app = "a"` + "\n[services.a]\ncommand = \"x\"",
		"unknown image":                    `app = "a"` + "\n[services.a]\nimage = \"missing\"\ncommand = \"x\"",
		"image \"Bad_Name\" must match":    `app = "a"` + "\n[images.Bad_Name]\n[services.a]\nimage = \"Bad_Name\"\ncommand = \"x\"",
		"step name":                        `app = "a"` + "\n[images.a]\nsteps = [{ name = \"Bad_Name\", run = \"true\" }]\n[services.a]\nimage = \"a\"\ncommand = \"x\"",
		"duplicate step name":              `app = "a"` + "\n[images.a]\nsteps = [{ name = \"deps\", run = \"true\" }, { name = \"deps\", run = \"true\" }]\n[services.a]\nimage = \"a\"\ncommand = \"x\"",
		"step run is required":             `app = "a"` + "\n[images.a]\nsteps = [{ name = \"deps\", run = \"  \" }]\n[services.a]\nimage = \"a\"\ncommand = \"x\"",
		"carry_over":                       `app = "a"` + "\n[images.a]\ncarry_over = \"missing\"\nsteps = [{ name = \"deps\", run = \"true\" }]\n[services.a]\nimage = \"a\"\ncommand = \"x\"",
		"image \"a\": environment":         `app = "a"` + "\n[images.a]\nenv = { \"bad-env\" = \"value\" }\n[services.a]\nimage = \"a\"\ncommand = \"x\"",
		"migrate must not be blank":        `app = "a"` + "\n[images.a]\n[services.a]\nimage = \"a\"\ncommand = \"x\"\nmigrate = \"   \"",
		"health_command must not be blank": `app = "a"` + "\n[images.a]\n[services.a]\nimage = \"a\"\ncommand = \"x\"\nhealth_command = \"   \"",
		"strict mode":                      `app = "a"` + "\n[images.a]\nunknown = \"value\"\n[services.a]\nimage = \"a\"\ncommand = \"x\"",
		"missing in the target struct":     `app = "a"` + "\n[images.a]\nsteps = [{ run = \"true\", unknown = \"value\" }]\n[services.a]\nimage = \"a\"\ncommand = \"x\"",
	}
	for want, content := range cases {
		_, err := Load(write(t, content))
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
			t.Errorf("%q: got %v", want, err)
		}
	}
}

func TestHealthCommandAllowsPortlessService(t *testing.T) {
	cfg, err := Load(write(t, `
app = "demo"
[images.app]
[services.check]
image = "app"
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

func TestRemovedServiceBuildFieldsAreRejected(t *testing.T) {
	for _, field := range []string{"packages", "user", "source", "install", "build", "reuse"} {
		content := `app = "a"` + "\n[images.a]\n[services.a]\nimage = \"a\"\ncommand = \"x\"\n" + field + " = \"value\""
		if _, err := Load(write(t, content)); err == nil || !strings.Contains(strings.ToLower(err.Error()), "strict mode") {
			t.Errorf("service field %q: got %v, want unknown-field error", field, err)
		}
	}
}

func TestReferencedImages(t *testing.T) {
	cfg, err := Load(write(t, `
app = "demo"
[images.web]
carry_over = "deps"
steps = [{ name = "deps", run = "true" }]
[images.unused]
[services.web]
image = "web"
command = "node server.js"
[services.worker]
image = "web"
command = "worker"
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Images["web"].User; got != "gci" {
		t.Fatalf("image user = %q, want gci", got)
	}
	if got := cfg.ReferencedImages(); !reflect.DeepEqual(got, []string{"web"}) {
		t.Fatalf("referenced images = %v, want [web]", got)
	}
}

func TestMigrateWithVolumesIsAllowed(t *testing.T) {
	cfg, err := Load(write(t, `
app = "demo"
[images.app]
[services.db]
image = "app"
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
[images.app]
[services.web]
image = "app"
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
