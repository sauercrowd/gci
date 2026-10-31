package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sauercrowd/gci/internal/config"
)

func TestDefaultAppName(t *testing.T) {
	for dir, want := range map[string]string{
		"/src/My_Project":                      "my-project",
		"/src/2fast":                           "fast",
		"/src/___":                             "app",
		"/src/a-very-long-project-name-indeed": "a-very-long-project-name",
	} {
		if got := defaultAppName(dir); got != want {
			t.Errorf("defaultAppName(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestWriteStarterConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gci.toml")
	if err := writeStarterConfig(path, "shop"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("starter config does not load: %v", err)
	}
	if cfg.App != "shop" || cfg.Services["web"] == nil {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = writeStarterConfig(path, "shop")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already-exists error, got %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "keep" {
		t.Fatalf("existing file was overwritten: %q", b)
	}
	if err := writeStarterConfig(filepath.Join(t.TempDir(), "gci.toml"), "Bad Name"); err == nil {
		t.Fatal("expected invalid app name error")
	}
}
