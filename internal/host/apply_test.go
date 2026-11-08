package host

import (
	"testing"

	"github.com/sauercrowd/gci/internal/config"
)

func TestShouldHoldRestart(t *testing.T) {
	cur := &ServiceState{Rev: "old"}
	tests := []struct {
		name       string
		restart    string
		cur        *ServiceState
		rev        string
		restartSet map[string]bool
		want       bool
	}{
		{name: "manual change", restart: "manual", cur: cur, rev: "new", want: true},
		{name: "auto change", restart: "auto", cur: cur, rev: "new"},
		{name: "first deploy", restart: "manual", rev: "new"},
		{name: "unchanged revision", restart: "manual", cur: cur, rev: "old"},
		{name: "explicit trigger", restart: "manual", cur: cur, rev: "new", restartSet: map[string]bool{"db": true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldHoldRestart("db", &config.Service{Restart: tt.restart}, tt.cur, tt.rev, tt.restartSet)
			if got != tt.want {
				t.Fatalf("shouldHoldRestart() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateRestartNames(t *testing.T) {
	cfg := &config.Config{Services: map[string]*config.Service{"web": {}, "db": {}}}
	if err := ValidateRestartNames(cfg, []string{"db", "web"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRestartNames(cfg, []string{"nope"}); err == nil || err.Error() != `--restart: unknown service "nope"` {
		t.Fatalf("unknown service error = %v", err)
	}
}

func TestShouldDeleteOrphanScopesLegacyDepsToApp(t *testing.T) {
	keep := map[string]bool{"gci-demo-web-r1234567": true}
	tests := []struct {
		name string
		inst instance
		want bool
	}{
		{
			name: "other app deps role",
			inst: instance{Name: "gci-shop-web-i1234567", Config: map[string]string{
				"user.gci.app":  "shop",
				"user.gci.role": "deps",
			}},
			want: false,
		},
		{
			name: "current app deps role",
			inst: instance{Name: "gci-demo-web-deps", Config: map[string]string{
				"user.gci.app":  "demo",
				"user.gci.role": "deps",
			}},
			want: true,
		},
		{
			name: "current app non-kept instance",
			inst: instance{Name: "gci-demo-web-old", Config: map[string]string{
				"user.gci.app": "demo",
			}},
			want: true,
		},
		{
			name: "current app kept instance",
			inst: instance{Name: "gci-demo-web-r1234567", Config: map[string]string{
				"user.gci.app": "demo",
			}},
			want: false,
		},
		{
			name: "legacy name without app key",
			inst: instance{Name: "gci-demo-web-i1234567"},
			want: true,
		},
		{
			name: "legacy name explicitly owned by another app",
			inst: instance{Name: "gci-demo-web-i1234567", Config: map[string]string{
				"user.gci.app": "shop",
			}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldDeleteOrphan(tt.inst, "demo", keep); got != tt.want {
				t.Errorf("shouldDeleteOrphan() = %t, want %t", got, tt.want)
			}
		})
	}
}
