package host

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sauercrowd/gci/internal/config"
)

func TestJobCommandArgs(t *testing.T) {
	args := jobCommandArgs("gci-demo-test-j123abc", "gci-job-123abc", "gci", true, 600*time.Second, "go test ./...")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"gci-demo-test-j123abc",
		"gci-job-123abc",
		"RuntimeMaxSec=600",
		"User=gci",
		"EnvironmentFile=/etc/gci/service.env",
		"WorkingDirectory=/app",
		"/bin/sh -e -c go test ./...",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q do not include %q", joined, want)
		}
	}
}

func TestJobCommandWithoutSourceUsesHomeDirectory(t *testing.T) {
	args := jobCommandArgs("gci-demo-test-j123abc", "gci-job-123abc", "gci", false, time.Minute, "true")
	if joined := strings.Join(args, " "); !strings.Contains(joined, "WorkingDirectory=/home/gci") {
		t.Fatalf("args %q do not use the image user's home directory", joined)
	}
}

func TestJobEnvironmentLayersAndReservedValues(t *testing.T) {
	vars := jobEnvVars("demo", "check",
		&config.Job{Image: "app", Env: map[string]string{"SHARED": "job", "npm_config_cache": "/job/npm"}},
		&config.Image{User: "root", Env: map[string]string{"IMAGE_ONLY": "image", "SHARED": "image", "XDG_CACHE_HOME": "/image/cache"}},
		"/nix/store/env", map[string]string{"SHARED": "secret"})
	if vars["IMAGE_ONLY"] != "image" || vars["SHARED"] != "secret" || vars["HOME"] != "/root" {
		t.Fatalf("job environment = %#v", vars)
	}
	cacheVars := map[string]string{
		"XDG_CACHE_HOME":       "/image/cache",
		"npm_config_cache":     "/job/npm",
		"npm_config_store_dir": "/var/cache/gci/pnpm",
		"YARN_CACHE_FOLDER":    "/var/cache/gci/yarn",
		"UV_CACHE_DIR":         "/var/cache/gci/uv",
		"UV_LINK_MODE":         "copy",
		"PIP_CACHE_DIR":        "/var/cache/gci/pip",
	}
	for key, want := range cacheVars {
		if vars[key] != want {
			t.Errorf("%s = %q, want %q", key, vars[key], want)
		}
	}
	if vars["GCI_APP"] != "demo" || vars["GCI_JOB"] != "check" {
		t.Fatalf("job identifiers = %#v", vars)
	}
	if _, ok := vars["PORT"]; ok {
		t.Fatalf("job environment includes PORT: %#v", vars)
	}
	if _, ok := vars["GCI_SERVICE"]; ok {
		t.Fatalf("job environment includes GCI_SERVICE: %#v", vars)
	}
}

func TestMaskJobSecretsAcrossWritesAndFlush(t *testing.T) {
	var output bytes.Buffer
	writer := &secretMaskWriter{dst: &output, secrets: map[string]string{"TOKEN": "secret"}}
	if _, err := writer.Write([]byte("before sec")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("ret after\npartial sec")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("ret")); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "before [REDACTED] after\n" {
		t.Fatalf("streamed output = %q", got)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "before [REDACTED] after\npartial [REDACTED]"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestMaskJobSecretsDoesNotCapOutput(t *testing.T) {
	output := strings.Repeat("x", 4096)
	if got := maskJobSecrets(output, nil); len(got) != len(output) {
		t.Fatalf("masked output length = %d, want %d", len(got), len(output))
	}
}
