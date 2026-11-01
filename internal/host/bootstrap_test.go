package host

import (
	"net/netip"
	"testing"
)

func TestCheckSubnet(t *testing.T) {
	want := netip.MustParsePrefix("10.77.0.1/24")
	tests := []struct {
		name    string
		current string
		wantErr bool
	}{
		{name: "empty", current: ""},
		{name: "equal", current: "10.77.0.1/24"},
		{name: "different address", current: "10.77.0.2/24", wantErr: true},
		{name: "different prefix length", current: "10.77.0.1/25", wantErr: true},
		{name: "garbage", current: "not a subnet", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSubnet(tc.current, want)
			if (err != nil) != tc.wantErr {
				t.Fatalf("checkSubnet(%q, %s) error = %v, wantErr %t", tc.current, want, err, tc.wantErr)
			}
		})
	}
}
