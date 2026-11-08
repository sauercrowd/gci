package remote

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
)

func TestAgentPreservesExitCode(t *testing.T) {
	h := &Host{root: true}
	h.run = func(ctx context.Context, _ string, _ io.Reader, _, _ io.Writer) error {
		return exec.CommandContext(ctx, "sh", "-c", "exit 3").Run()
	}
	err := h.Agent(context.Background(), []string{"run-job"}, nil, io.Discard, io.Discard)
	var exit *AgentExitError
	if !errors.As(err, &exit) {
		t.Fatalf("Agent() error = %v, want AgentExitError", err)
	}
	if exit.Code != 3 {
		t.Fatalf("exit code = %d, want 3", exit.Code)
	}
	if !errors.Is(err, ErrAgent) {
		t.Fatalf("AgentExitError does not match ErrAgent: %v", err)
	}
}

func TestRunJobAgentInputClosesOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	inputClosed := make(chan struct{})
	h := &Host{}
	h.run = func(ctx context.Context, _ string, stdin io.Reader, _, _ io.Writer) error {
		reader := bufio.NewReader(stdin)
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if line != "{\"job\":\"test\"}\n" {
			t.Errorf("agent input = %q", line)
		}
		close(started)
		_, _ = io.Copy(io.Discard, reader)
		close(inputClosed)
		return ctx.Err()
	}

	done := make(chan error, 1)
	go func() {
		done <- h.Agent(ctx, []string{"run-job"}, strings.NewReader("{\"job\":\"test\"}"), io.Discard, io.Discard)
	}()
	<-started
	select {
	case <-inputClosed:
		t.Fatal("run-job input closed before cancellation")
	default:
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Agent() error = %v, want context.Canceled", err)
	}
	<-inputClosed
}
