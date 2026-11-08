package host

import "testing"

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
