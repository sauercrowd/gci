package host

import (
	"strings"
	"testing"
)

func TestMigrationCommandArgs(t *testing.T) {
	args, unit := migrationCommandArgs("gci-demo-web-m1234567", "gci", "123456789abcd", "db:migrate")
	if unit != "gci-migrate-1234567" {
		t.Fatalf("unit = %q", unit)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"RuntimeMaxSec=600",
		"User=gci",
		"EnvironmentFile=/etc/gci/service.env",
		"WorkingDirectory=/app",
		"/bin/sh -e -c db:migrate",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q do not include %q", joined, want)
		}
	}
}
