package host

import (
	"reflect"
	"testing"
	"time"

	"github.com/sauercrowd/gci/internal/config"
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
		{Key: "old", ChainKey: "chain", Instance: "old-release", HasStepSnapshot: true, Created: now},
		{Key: "new", ChainKey: "chain", Instance: "new-release", HasStepSnapshot: true, Created: now.Add(time.Hour)},
		{Key: "built", ChainKey: "chain", Instance: "built-release", Created: now.Add(2 * time.Hour)},
		{Key: "other", ChainKey: "other-chain", Instance: "other-release", HasStepSnapshot: true, Created: now.Add(3 * time.Hour)},
	}
	tests := []struct {
		name           string
		rels           []*Release
		snapshotStep   string
		snapshotIsLast bool
		clean          bool
		from           string
		fresh          bool
	}{
		{name: "clean", rels: rels, snapshotStep: "deps", clean: true, fresh: true},
		{name: "no snapshot step", rels: rels, from: "", fresh: true},
		{name: "no chain match", rels: rels[3:], snapshotStep: "deps", fresh: true},
		{name: "intermediate snapshot missing", rels: rels[2:3], snapshotStep: "deps", fresh: true},
		{name: "intermediate snapshot chooses newest", rels: rels, snapshotStep: "deps", from: "new-release/step"},
		{name: "last step snapshot starts from release", rels: rels, snapshotStep: "build", snapshotIsLast: true, from: "built-release"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from, fresh := buildStart(tt.rels, "chain", tt.snapshotStep, tt.snapshotIsLast, tt.clean)
			if from != tt.from || fresh != tt.fresh {
				t.Fatalf("buildStart() = (%q, %v), want (%q, %v)", from, fresh, tt.from, tt.fresh)
			}
		})
	}
}

func TestChainKeySnapshotBoundary(t *testing.T) {
	steps := []config.Step{
		{Name: "prepare", Run: "prepare"},
		{Name: "deps", Run: "npm install"},
		{Name: "build", Run: "npm run build"},
	}
	base := chainKey("base", "env", "gci", map[string]string{"B": "2", "A": "1"}, "deps", steps)
	afterSnapshot := []config.Step{
		{Name: "prepare", Run: "prepare"},
		{Name: "deps", Run: "npm install"},
		{Name: "build", Run: "npm run build changed"},
	}
	if got := chainKey("base", "env", "gci", map[string]string{"A": "1", "B": "2"}, "deps", afterSnapshot); got != base {
		t.Fatal("step after snapshot changed chain key")
	}
	beforeSnapshot := []config.Step{
		{Name: "prepare", Run: "prepare changed"},
		{Name: "deps", Run: "npm install"},
		{Name: "build", Run: "npm run build"},
	}
	if got := chainKey("base", "env", "gci", map[string]string{"A": "1", "B": "2"}, "deps", beforeSnapshot); got == base {
		t.Fatal("step before snapshot did not change chain key")
	}
	if got := chainKey("base", "env", "gci", map[string]string{"A": "1", "B": "2"}, "build", steps); got == base {
		t.Fatal("snapshot step change did not change chain key")
	}
	for _, changed := range []string{
		chainKey("new-base", "env", "gci", map[string]string{"A": "1", "B": "2"}, "deps", steps),
		chainKey("base", "new-env", "gci", map[string]string{"A": "1", "B": "2"}, "deps", steps),
		chainKey("base", "env", "other-user", map[string]string{"A": "1", "B": "2"}, "deps", steps),
		chainKey("base", "env", "gci", map[string]string{"A": "changed", "B": "2"}, "deps", steps),
	} {
		if changed == base {
			t.Fatal("base, Nix env, user, or image env change did not change chain key")
		}
	}
}

func TestReleaseKeyIncludesBuildInputs(t *testing.T) {
	steps := []config.Step{{Name: "deps", Run: "npm install"}, {Name: "build", Run: "npm run build"}}
	env := map[string]string{"B": "2", "A": "1"}
	first := releaseKey("base", "nix-env", "gci", env, "source", steps)
	if sorted := releaseKey("base", "nix-env", "gci", map[string]string{"A": "1", "B": "2"}, "source", steps); sorted != first {
		t.Fatal("release key changed when image env map order changed")
	}
	changedStep := []config.Step{{Name: "deps", Run: "npm install"}, {Name: "build", Run: "npm run build changed"}}
	changedName := []config.Step{{Name: "dependencies", Run: "npm install"}, {Name: "build", Run: "npm run build"}}
	for _, changed := range []string{
		releaseKey("other-base", "nix-env", "gci", env, "source", steps),
		releaseKey("base", "other-env", "gci", env, "source", steps),
		releaseKey("base", "nix-env", "other-user", env, "source", steps),
		releaseKey("base", "nix-env", "gci", map[string]string{"A": "changed", "B": "2"}, "source", steps),
		releaseKey("base", "nix-env", "gci", env, "other-source", steps),
		releaseKey("base", "nix-env", "gci", env, "source", changedStep),
		releaseKey("base", "nix-env", "gci", env, "source", changedName),
	} {
		if changed == first {
			t.Fatal("release key did not change when a build input changed")
		}
	}
}

func TestImageEnvAppliesToBuildSteps(t *testing.T) {
	vars := imageBuildEnv("/nix/store/env", "app", "web", &config.Image{
		User: "gci",
		Env:  map[string]string{"PATH": "/custom/bin", "BUILD_FLAG": "1"},
	})
	if vars["PATH"] != "/custom/bin" || vars["BUILD_FLAG"] != "1" || vars["GCI_IMAGE"] != "web" {
		t.Fatalf("build vars = %#v", vars)
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
