package host

import (
	"errors"
	"testing"
	"time"
)

func TestKeepCurrentOnFailure(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	issuanceErr := errors.New("issuance failed")
	tests := []struct {
		name    string
		current *storedCertificate
		want    bool
	}{
		{name: "missing certificate"},
		{
			name: "placeholder certificate",
			current: &storedCertificate{Meta: CertificateMeta{
				Source:   "placeholder",
				NotAfter: now.Add(24 * time.Hour),
			}},
		},
		{
			name: "valid ACME certificate inside renewal window",
			current: &storedCertificate{Meta: CertificateMeta{
				Source:   "acme",
				NotAfter: now.Add(24 * time.Hour),
			}},
			want: true,
		},
		{
			name: "expired ACME certificate",
			current: &storedCertificate{Meta: CertificateMeta{
				Source:   "acme",
				NotAfter: now.Add(-time.Second),
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := keepCurrentOnFailure(tt.current, issuanceErr, now); got != tt.want {
				t.Fatalf("keepCurrentOnFailure() = %t, want %t", got, tt.want)
			}
		})
	}
}
