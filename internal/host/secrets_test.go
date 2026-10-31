package host

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func useTestSecretRoot(t *testing.T) {
	t.Helper()
	old := secretRoot
	secretRoot = filepath.Join(t.TempDir(), "secrets")
	t.Cleanup(func() { secretRoot = old })
}

func TestSecretDigestKeyReusesExistingKeyAndLeavesNoTempFiles(t *testing.T) {
	useTestSecretRoot(t)
	if err := os.MkdirAll(secretRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat([]byte{0x5a}, 32)
	path := filepath.Join(secretRoot, secretDigestKeyName)
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := secretDigestKey()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("secret digest key was not reused")
	}
	assertOnlyDigestKeyFile(t)
}

func TestSecretDigestKeyCreationLeavesNoTempFiles(t *testing.T) {
	useTestSecretRoot(t)
	key, err := secretDigestKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Fatalf("key length = %d, want 32", len(key))
	}
	assertOnlyDigestKeyFile(t)
}

func assertOnlyDigestKeyFile(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(secretRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != secretDigestKeyName {
		t.Fatalf("secret directory entries = %v, want only %s", entries, secretDigestKeyName)
	}
}
