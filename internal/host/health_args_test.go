package host

import (
	"strings"
	"testing"
	"time"
)

func TestHealthCommandArgsSetUnitAndRuntimeLimit(t *testing.T) {
	args, unit := healthCommandArgs("web", "gci", true, 3, 1500*time.Millisecond)
	if unit != "gci-health-3" {
		t.Fatalf("unit = %q, want gci-health-3", unit)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--unit gci-health-3", "-p RuntimeMaxSec=2", "-p WorkingDirectory=/app"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q do not include %q", joined, want)
		}
	}
}

func TestHealthCommandArgsRuntimeLimitMinimumOneSecond(t *testing.T) {
	args, _ := healthCommandArgs("web", "gci", false, 1, 0)
	if !strings.Contains(strings.Join(args, " "), "-p RuntimeMaxSec=1") {
		t.Fatalf("args %q do not include one-second RuntimeMaxSec", args)
	}
}
