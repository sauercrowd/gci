package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gci.toml")
	if err := os.WriteFile(path, []byte("app = \"demo\"\n[services.web]\ncommand = \"run\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Nixpkgs != DefaultNixpkgs || cfg.Services["web"].User != "gci" {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
}

func TestValidateRequiresCommand(t *testing.T) {
	cfg := &Config{App: "demo", Services: map[string]*Service{"web": {}}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing command to fail")
	}
}
