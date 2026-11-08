package cmd

import (
	"errors"
	"testing"

	"github.com/sauercrowd/gci/internal/host"
	"github.com/sauercrowd/gci/internal/remote"
)

func TestPreserveJobExitCode(t *testing.T) {
	err := preserveJobExitCode(&remote.AgentExitError{Code: 3})
	code, ok := commandExitCode(err)
	if !ok || code != 3 {
		t.Fatalf("commandExitCode() = %d, %t; want 3, true", code, ok)
	}
}

func TestOtherCommandErrorsKeepDefaultExitCode(t *testing.T) {
	code, ok := commandExitCode(errors.New("failure"))
	if ok || code != 1 {
		t.Fatalf("commandExitCode() = %d, %t; want 1, false", code, ok)
	}
	if err := preserveJobExitCode(errors.New("failure")); err.Error() != "failure" {
		t.Fatalf("preserveJobExitCode changed error: %v", err)
	}
	if _, ok := commandExitCode(&host.JobExitError{Code: 0}); !ok {
		t.Fatal("job exit status 0 was not preserved")
	}
}
