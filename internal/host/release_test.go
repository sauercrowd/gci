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
	removed, replaced, err := sourceManifestDiff(
		[]sourceManifestEntry{
			manifestEntry("keep.txt", false),
			manifestEntry("nested", true),
			manifestEntry("nested/old.txt", false),
			manifestEntry("nested/deep", true),
			manifestEntry("nested/deep/old.txt", false),
		},
		[]sourceManifestEntry{
			manifestEntry("keep.txt", false),
			manifestEntry("nested", true),
			manifestEntry("nested/new.txt", false),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"nested/deep", "nested/deep/old.txt", "nested/old.txt"}
	if !reflect.DeepEqual(removed, want) {
		t.Fatalf("removed = %v, want %v", removed, want)
	}
	if len(replaced) != 0 {
		t.Fatalf("replaced = %v, want none", replaced)
	}
}

func TestSourceManifestDiffRejectsUnsafePaths(t *testing.T) {
	for _, path := range []string{"/absolute", "../parent", "nested/../../escape"} {
		if _, _, err := sourceManifestDiff([]sourceManifestEntry{{Path: path}}, nil); err == nil {
			t.Errorf("unsafe path %q was accepted", path)
		}
		if _, _, err := sourceManifestDiff(nil, []sourceManifestEntry{manifestEntry(path, false)}); err == nil {
			t.Errorf("unsafe new path %q was accepted", path)
		}
	}
}

func TestSourceManifestDiffTypeChanges(t *testing.T) {
	tests := []struct {
		name    string
		old     []sourceManifestEntry
		current []sourceManifestEntry
		want    []string
	}{
		{
			name:    "file to directory",
			old:     []sourceManifestEntry{manifestEntry("asset", false)},
			current: []sourceManifestEntry{manifestEntry("asset", true)},
			want:    []string{"asset"},
		},
		{
			name:    "directory to file",
			old:     []sourceManifestEntry{manifestEntry("asset", true)},
			current: []sourceManifestEntry{manifestEntry("asset", false)},
			want:    []string{"asset"},
		},
		{
			name:    "unchanged type",
			old:     []sourceManifestEntry{manifestEntry("asset", false)},
			current: []sourceManifestEntry{manifestEntry("asset", false)},
			want:    []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			removed, replaced, err := sourceManifestDiff(tt.old, tt.current)
			if err != nil {
				t.Fatal(err)
			}
			if len(removed) != 0 {
				t.Fatalf("removed = %v, want none", removed)
			}
			if !reflect.DeepEqual(replaced, tt.want) {
				t.Fatalf("replaced = %v, want %v", replaced, tt.want)
			}
		})
	}
}

func TestSourceManifestDiffLegacyArrayHasUnknownTypes(t *testing.T) {
	old, err := decodeSourceManifest([]byte(`["asset"]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(old) != 1 || old[0].Dir != nil {
		t.Fatalf("legacy entries = %+v, want unknown type", old)
	}
	removed, replaced, err := sourceManifestDiff(old, []sourceManifestEntry{manifestEntry("asset", true)})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 || len(replaced) != 0 {
		t.Fatalf("legacy transition removed=%v replaced=%v, want neither", removed, replaced)
	}
}

func manifestEntry(path string, dir bool) sourceManifestEntry {
	return sourceManifestEntry{Path: path, Dir: &dir}
}
