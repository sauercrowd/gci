package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sauercrowd/gci/internal/config"
)

func TestDefaultAppName(t *testing.T) {
	if got := defaultAppName("/src/My_Project"); got != "my-project" {
		t.Fatalf("defaultAppName() = %q", got)
	}
}

func TestWriteStarterConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gci.toml")
	if err := writeStarterConfig(path, "shop"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil || cfg.App != "shop" {
		t.Fatalf("Load() = %#v, %v", cfg, err)
	}
	if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeStarterConfig(path, "shop"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already-exists error, got %v", err)
	}
}
