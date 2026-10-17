package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

type logOrderRunner struct {
	results       map[string]CommandResult
	failures      map[string]error
	streamCommand string
}

func (r *logOrderRunner) Run(_ context.Context, command string) (CommandResult, error) {
	result, ok := r.results[command]
	if !ok {
		return CommandResult{}, fmt.Errorf("unexpected command: %s", command)
	}
	return result, r.failures[command]
}
func (r *logOrderRunner) Stream(_ context.Context, command string, stdout, stderr io.Writer) error {
	r.streamCommand = command
	return nil
}

func logTestConfig(names ...string) Config {
	cfg := Config{DriverDockerSwarm: &DockerSwarmConfig{Stacks: []DockerSwarmStack{{Name: "app"}}}}
	for _, name := range names {
		cfg.DriverDockerSwarm.LogServices = append(cfg.DriverDockerSwarm.LogServices, DockerSwarmLogService{Name: name})
	}
	return cfg
}

func TestLogsChronologicalAcrossServicesAndTasks(t *testing.T) {
	runner := &logOrderRunner{results: map[string]CommandResult{
		"docker service logs --timestamps --tail 10 'app_web' 2>&1": {Stdout: "web.2.b@node | 2026-10-17T10:00:03Z third\nweb.1.a@node | 2026-10-17T10:00:01.000000001Z first\n"},
		"docker service logs --timestamps --tail 10 'app_db' 2>&1":  {Stdout: "db.1.c@node | 2026-10-17T11:00:02+01:00 second\n"},
	}}
	var stdout, stderr bytes.Buffer
	err := (dockerSwarmDriver{}).Logs(context.Background(), runner, logTestConfig("web", "db"), 10, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	want := "[app_web] web.1.a@node | 2026-10-17T10:00:01.000000001Z first\n[app_db] db.1.c@node | 2026-10-17T11:00:02+01:00 second\n[app_web] web.2.b@node | 2026-10-17T10:00:03Z third\n"
	if stdout.String() != want {
		t.Fatalf("output = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", &stderr)
	}
}

func TestLogsPreserveTiesMultilineAndUntimestampedOutput(t *testing.T) {
	entries := parseDockerSwarmLogs("app_web", "warning before logs\nweb.1.a@node | 2026-10-17T10:00:02Z later\nweb.1.a@node | 2026-10-17T10:00:01Z traceback\n  at handler\n\nweb.1.a@node | 2026-10-17T10:00:01Z same timestamp")
	entries = append(entries, parseDockerSwarmLogs("app_db", "2026-10-17T10:00:01Z another | service\n")...)
	var out bytes.Buffer
	if err := writeDockerSwarmLogs(&out, entries); err != nil {
		t.Fatal(err)
	}
	want := "[app_web] warning before logs\n[app_web] web.1.a@node | 2026-10-17T10:00:01Z traceback\n[app_web]   at handler\n[app_web] \n[app_web] web.1.a@node | 2026-10-17T10:00:01Z same timestamp\n[app_db] 2026-10-17T10:00:01Z another | service\n[app_web] web.1.a@node | 2026-10-17T10:00:02Z later\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
	if got := parseDockerSwarmLogs("app_web", ""); len(got) != 0 {
		t.Fatalf("empty logs = %#v", got)
	}
}

func TestLogsFetchFailurePreservesPartialLogs(t *testing.T) {
	command := "docker service logs --timestamps --tail 10 'app_web' 2>&1"
	runner := &logOrderRunner{
		results:  map[string]CommandResult{command: {Stdout: "web.1.a@node | 2026-10-17T10:00:01Z partial\n", Stderr: "SSH failure"}},
		failures: map[string]error{command: errors.New("connection lost")},
	}
	var out bytes.Buffer
	err := (dockerSwarmDriver{}).Logs(context.Background(), runner, logTestConfig("web"), 10, &out, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "connection lost") || !strings.Contains(err.Error(), "SSH failure") {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(out.String(), "partial") {
		t.Fatalf("partial logs lost: %q", out.String())
	}
}

type failedLogWriter struct{}

func (failedLogWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestLogsWriteFailure(t *testing.T) {
	err := writeDockerSwarmLogs(failedLogWriter{}, parseDockerSwarmLogs("app_web", "message\n"))
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("error = %v", err)
	}
}

func TestLogsStreamMergesRemoteStreams(t *testing.T) {
	runner := &logOrderRunner{}
	err := (dockerSwarmDriver{}).LogsStream(context.Background(), runner, logTestConfig("web"), 10, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	want := "docker service logs --timestamps --tail 10 --follow 'app_web' 2>&1"
	if runner.streamCommand != want {
		t.Fatalf("command = %q, want %q", runner.streamCommand, want)
	}
	err = (dockerSwarmDriver{}).LogsStream(context.Background(), runner, logTestConfig("web", "db"), 10, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected multiple-service streaming error")
	}
}

func TestDiagnosticLogsSortAndContinueAfterFetchFailure(t *testing.T) {
	command := "docker service logs --timestamps --tail 50 'app_web' 2>&1"
	runner := &logOrderRunner{
		results: map[string]CommandResult{
			"docker stack services 'app' --format '{{.Name}}'": {Stdout: "app_web\napp_db\n"},
			command: {Stdout: "web.1.a@node | 2026-10-17T10:00:02Z later\n"},
			"docker service logs --timestamps --tail 50 'app_db' 2>&1": {Stdout: "db.1.b@node | 2026-10-17T10:00:01Z earlier\n"},
		},
		failures: map[string]error{command: errors.New("task unavailable")},
	}
	var out bytes.Buffer
	dumpDockerSwarmStackLogs(context.Background(), runner, "app", 50, &out)
	got := out.String()
	if !strings.Contains(got, "task unavailable") {
		t.Fatalf("missing failure: %s", got)
	}
	earlier := strings.Index(got, "[app_db]")
	later := strings.Index(got, "[app_web]")
	if earlier < 0 || later < earlier {
		t.Fatalf("wrong log order: %s", got)
	}
}
