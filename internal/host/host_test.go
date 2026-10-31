package host

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
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
	cfg := &config.Config{App: "a", Services: map[string]*config.Service{"web": {Command: "x", Port: 1}}}
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
}

func TestSecretRevisionScope(t *testing.T) {
	useTestSecretRoot(t)
	cfg := &config.Config{App: "a", Services: map[string]*config.Service{
		"web": {Command: "run", Secrets: map[string]string{"SESSION_SECRET": "session-secret"}},
		"db":  {Command: "db", Secrets: map[string]string{"PASSWORD": "db-password"}},
	}}
	digest := func(values map[string]string) string {
		t.Helper()
		got, err := secretDigest(values)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	one := revisionWithSecrets(cfg, "web", "/nix/store/env", "release", digest(map[string]string{"SESSION_SECRET": "one"}))
	two := revisionWithSecrets(cfg, "web", "/nix/store/env", "release", digest(map[string]string{"SESSION_SECRET": "two"}))
	if one == two {
		t.Fatal("secret value did not change revision")
	}
	other := revisionWithSecrets(cfg, "web", "/nix/store/env", "release", digest(map[string]string{"SESSION_SECRET": "one"}))
	cfg.Services["db"].Secrets["PASSWORD"] = "another-password"
	if got := revisionWithSecrets(cfg, "web", "/nix/store/env", "release", digest(map[string]string{"SESSION_SECRET": "one"})); got != other {
		t.Fatal("another service's secret changed web revision")
	}
}

func TestSecretDigestUsesPersistentHMACKey(t *testing.T) {
	useTestSecretRoot(t)
	values := map[string]string{"TOKEN": "low-entropy"}
	first, err := secretDigest(values)
	if err != nil {
		t.Fatal(err)
	}
	second, err := secretDigest(values)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("same secret produced different digests")
	}
	changed, err := secretDigest(map[string]string{"TOKEN": "different"})
	if err != nil {
		t.Fatal(err)
	}
	if first == changed {
		t.Fatal("different secret produced the same digest")
	}
	keyPath := filepath.Join(secretRoot, secretDigestKeyName)
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != sha256.Size {
		t.Fatalf("key length = %d, want %d", len(key), sha256.Size)
	}
	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %04o, want 0600", keyInfo.Mode().Perm())
	}
	dirInfo, err := os.Stat(secretRoot)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("secrets dir mode = %04o, want 0700", dirInfo.Mode().Perm())
	}
	h := hmac.New(sha256.New, key)
	hashString(h, "TOKEN")
	hashString(h, "low-entropy")
	if first != hex.EncodeToString(h.Sum(nil)) {
		t.Fatal("digest is not the expected HMAC-SHA256")
	}
}

func TestSecretDigestWithoutSecretsPreservesLegacyHash(t *testing.T) {
	useTestSecretRoot(t)
	got, err := secretDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("empty digest = %s, want legacy empty SHA-256", got)
	}
	if _, err := os.Stat(secretRoot); !os.IsNotExist(err) {
		t.Fatalf("secret digest created key storage with no secrets: %v", err)
	}
}

func TestListSecretsOmitsDigestKey(t *testing.T) {
	useTestSecretRoot(t)
	appDir := filepath.Join(secretRoot, "demo")
	if err := os.MkdirAll(appDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]string{
		filepath.Join(secretRoot, secretDigestKeyName): "host key",
		filepath.Join(appDir, secretDigestKeyName):     "app key",
		filepath.Join(appDir, "token"):                 "secret",
	} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	names, err := ListSecrets("demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "token" {
		t.Fatalf("ListSecrets() = %v, want [token]", names)
	}
}

func useTestSecretRoot(t *testing.T) {
	t.Helper()
	previous := secretRoot
	secretRoot = filepath.Join(t.TempDir(), "secrets")
	t.Cleanup(func() { secretRoot = previous })
}

func TestPruneReleases(t *testing.T) {
	rels := []*Release{{Key: "one"}, {Key: "two"}, {Key: "three"}, {Key: "four"}}
	kept, dropped := pruneReleases(rels, "one", 3)
	if len(kept) != 3 || kept[0].Key != "one" || kept[1].Key != "three" || kept[2].Key != "four" {
		t.Fatalf("kept = %#v", kept)
	}
	if len(dropped) != 1 || dropped[0].Key != "two" {
		t.Fatalf("dropped = %#v", dropped)
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

func TestHealthScript(t *testing.T) {
	if got, want := healthScript("check --ready"), "#!/bin/sh\nset -e\ncheck --ready\n"; got != want {
		t.Fatalf("healthScript() = %q, want %q", got, want)
	}
}

func TestSanitizeHealthOutputRedactsSecret(t *testing.T) {
	if got, want := sanitizeHealthOutput("check failed: private-value", map[string]string{"TOKEN": "private-value"}), "check failed: [REDACTED]"; got != want {
		t.Fatalf("sanitizeHealthOutput() = %q, want %q", got, want)
	}
}
