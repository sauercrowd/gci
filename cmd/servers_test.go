package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveServersAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "servers.toml")
	t.Setenv("GCI_SERVERS_FILE", path)

	for _, want := range [][]server{
		{{Name: "old", Local: true}},
		{{Name: "new", Host: "builder", User: "deploy", PrivateKey: "/id"}},
	} {
		if err := saveServers(want); err != nil {
			t.Fatal(err)
		}
		got, err := loadServers()
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) || got[0] != want[0] {
			t.Fatalf("loaded servers = %#v, want %#v", got, want)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("servers file mode = %o, want 600", got)
	}
}
