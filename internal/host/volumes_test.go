package host

import (
	"reflect"
	"testing"
)

func TestVolumesToDeleteUsesOwnersAndExactLegacyNames(t *testing.T) {
	volumes := []volumeInfo{
		{Name: "gci-shop-api-pgdata", Type: "custom", Config: map[string]string{"user.gci.app": "shop-api"}},
		{Name: "gci-shop-api-unclaimed", Type: "custom"},
		{Name: "gci-shop-db", Type: "custom", Config: map[string]string{"user.gci.app": "shop"}},
		{Name: "gcicache-shop-web", Type: "custom", Config: map[string]string{"user.gci.app": "shop"}},
		{Name: "gci-shop-legacy", Type: "custom"},
		{Name: "gci-shop-other", Type: "custom", Config: map[string]string{"user.gci.app": "shop-api"}},
		{Name: "gci-shop-image", Type: "image", Config: map[string]string{"user.gci.app": "shop"}},
	}
	legacy := map[string]bool{"gci-shop-legacy": true}
	got := volumesToDelete("shop", volumes, legacy)
	want := []string{"gci-shop-db", "gci-shop-legacy", "gcicache-shop-web"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("volumesToDelete = %#v, want %#v", got, want)
	}
}

func TestVolumeOwnerClaim(t *testing.T) {
	tests := []struct {
		name      string
		app       string
		owner     string
		wantClaim bool
		wantError string
	}{
		{name: "untagged", app: "shop", wantClaim: true},
		{name: "same app", app: "shop", owner: "shop"},
		{name: "another app", app: "shop", owner: "shop-api", wantError: "volume gci-shop-api-pgdata belongs to app shop-api"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claim, err := volumeOwnerClaim("gci-shop-api-pgdata", tt.app, tt.owner)
			if err != nil {
				if err.Error() != tt.wantError {
					t.Fatalf("volumeOwnerClaim error = %q, want %q", err, tt.wantError)
				}
				return
			}
			if tt.wantError != "" {
				t.Fatalf("volumeOwnerClaim error = nil, want %q", tt.wantError)
			}
			if claim != tt.wantClaim {
				t.Fatalf("volumeOwnerClaim = %t, want %t", claim, tt.wantClaim)
			}
		})
	}
}
