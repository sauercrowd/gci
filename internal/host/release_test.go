package host

import (
	"reflect"
	"testing"
	"time"
)

func TestBuildCommandArgsUseFailFastShell(t *testing.T) {
	args := buildCommandArgs("builder", "1000", "1000", map[string]string{"B": "2", "A": "1"}, "false; echo unreachable")
	got := args[len(args)-5:]
	want := []string{"--", "sh", "-e", "-c", "false; echo unreachable"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("shell args = %v, want suffix %v", got, want)
		}
	}
}

func TestBuildStart(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	rels := []*Release{
		{Key: "old", ChainKey: "chain", Instance: "old-release", Installed: true, Created: now},
		{Key: "new", ChainKey: "chain", Instance: "new-release", Installed: true, Created: now.Add(time.Hour)},
		{Key: "built", ChainKey: "chain", Instance: "built-release", Created: now.Add(2 * time.Hour)},
		{Key: "other", ChainKey: "other-chain", Instance: "other-release", Installed: true, Created: now.Add(3 * time.Hour)},
	}
	tests := []struct {
		name  string
		rels  []*Release
		reuse string
		clean bool
		from  string
		fresh bool
	}{
		{name: "clean", rels: rels, reuse: "build", clean: true, fresh: true},
		{name: "nothing", rels: rels, reuse: "nothing", from: "", fresh: true},
		{name: "no chain match", rels: rels[3:], reuse: "dependencies", fresh: true},
		{name: "dependencies without installed snapshot", rels: rels[2:3], reuse: "dependencies", fresh: true},
		{name: "dependencies chooses newest installed snapshot", rels: rels, reuse: "dependencies", from: "new-release/installed"},
		{name: "build chooses newest full release", rels: rels, reuse: "build", from: "built-release"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from, fresh := buildStart(tt.rels, "chain", tt.reuse, tt.clean)
			if from != tt.from || fresh != tt.fresh {
				t.Fatalf("buildStart() = (%q, %v), want (%q, %v)", from, fresh, tt.from, tt.fresh)
			}
		})
	}
}

func TestReleaseKeyIncludesChainKey(t *testing.T) {
	first := releaseKey("chain-one", "source", "build", map[string]string{"A": "1"})
	second := releaseKey("chain-two", "source", "build", map[string]string{"A": "1"})
	if first == second {
		t.Fatal("release key did not change when chain key changed")
	}
}

func TestSourceManifestDiff(t *testing.T) {
	removed, err := sourceManifestDiff(
		[]string{"keep.txt", "nested", "nested/old.txt", "nested/deep", "nested/deep/old.txt"},
		[]string{"keep.txt", "nested", "nested/new.txt"},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"nested/deep", "nested/deep/old.txt", "nested/old.txt"}
	if !reflect.DeepEqual(removed, want) {
		t.Fatalf("removed = %v, want %v", removed, want)
	}
}

func TestSourceManifestDiffRejectsUnsafePaths(t *testing.T) {
	for _, path := range []string{"/absolute", "../parent", "nested/../../escape"} {
		if _, err := sourceManifestDiff([]string{path}, nil); err == nil {
			t.Errorf("unsafe path %q was accepted", path)
		}
		if _, err := sourceManifestDiff(nil, []string{path}); err == nil {
			t.Errorf("unsafe new path %q was accepted", path)
		}
	}
}
