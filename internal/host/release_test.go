package host

import "testing"

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
