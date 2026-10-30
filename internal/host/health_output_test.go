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
	assertNo16ByteSecretSubstring(t, output, secret)
}

func TestSanitizeHealthOutputDoesNotExposeSecretAfterRedactionShrink(t *testing.T) {
	secretA := strings.Repeat("a", 20)
	secretB := strings.Repeat("b", maxSecretSize)
	raw := strings.Repeat(secretA, 200) + secretB
	w := &cappedHealthWriter{limit: maxHealthCommandOutput + maxSecretSize}
	if _, err := w.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	captured := w.String()
	if len(captured) != maxHealthCommandOutput+maxSecretSize {
		t.Fatalf("captured output length = %d, want cap %d", len(captured), maxHealthCommandOutput+maxSecretSize)
	}

	output := sanitizeHealthOutput(captured, map[string]string{"A": secretA, "B": secretB})
	if !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("sanitized output %q does not include [REDACTED]", output)
	}
	assertNo16ByteSecretSubstring(t, output, secretB)
}

func TestSanitizeHealthOutputRedactsSecretAcrossRawOutputLimit(t *testing.T) {
	secret := strings.Repeat("s", 32)
	raw := strings.Repeat("f", maxHealthCommandOutput-16) + secret + "tail"
	w := &cappedHealthWriter{limit: maxHealthCommandOutput + maxSecretSize}
	if _, err := w.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}

	output := sanitizeHealthOutput(w.String(), map[string]string{"TOKEN": secret})
	if !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("sanitized output %q does not include [REDACTED]", output)
	}
	assertNo16ByteSecretSubstring(t, output, secret)
}

func assertNo16ByteSecretSubstring(t *testing.T, output, secret string) {
	t.Helper()
	seen := make(map[string]struct{})
	for i := 0; i+16 <= len(secret); i++ {
		substring := secret[i : i+16]
		if _, ok := seen[substring]; ok {
			continue
		}
		seen[substring] = struct{}{}
		if strings.Contains(output, substring) {
			t.Fatalf("sanitized output contains a 16-byte substring of the secret: %q", output)
		}
	}
}
