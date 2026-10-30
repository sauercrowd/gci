package host

import (
	"strings"
	"testing"
)

func TestCappedHealthWriterAcceptsAndCapsOutput(t *testing.T) {
	w := &cappedHealthWriter{limit: 5}
	if n, err := w.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatalf("first Write() = %d, %v", n, err)
	}
	if n, err := w.Write([]byte("defghi")); n != 6 || err != nil {
		t.Fatalf("second Write() = %d, %v", n, err)
	}
	if got := w.String(); got != "abcde" {
		t.Fatalf("captured output = %q, want abcde", got)
	}
}

func TestCappedHealthWriterKeepsSecretsWholeForRedaction(t *testing.T) {
	secret := strings.Repeat("s", 4*1024)
	w := &cappedHealthWriter{limit: maxHealthCommandOutput + maxSecretSize}
	if _, err := w.Write([]byte(strings.Repeat("f", 2000))); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(secret)); err != nil {
		t.Fatal(err)
	}

	output := sanitizeHealthOutput(w.String(), map[string]string{"TOKEN": secret})
	if !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("sanitized output %q does not include [REDACTED]", output)
	}
	for i := 0; i+16 <= len(secret); i++ {
		if strings.Contains(output, secret[i:i+16]) {
			t.Fatalf("sanitized output contains a 16-byte substring of the secret: %q", output)
		}
	}
}
