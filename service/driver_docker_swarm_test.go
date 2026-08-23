package service

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"testing"
)

type logOrderRunner struct {
	results map[string]CommandResult
}

func (r logOrderRunner) Run(_ context.Context, command string) (CommandResult, error) {
	result, ok := r.results[command]
	if !ok {
		return CommandResult{}, fmt.Errorf("unexpected command: %s", command)
	}
	return result, nil
}

func (logOrderRunner) Stream(context.Context, string, io.Writer, io.Writer) error {
	return nil
}

func TestSortLogServicesByContainerStartOldestFirst(t *testing.T) {
	services := []DockerSwarmLogService{
		{Stack: "app", Name: "newest"},
		{Stack: "app", Name: "oldest"},
		{Stack: "app", Name: "middle"},
	}
	runner := logOrderRunner{results: map[string]CommandResult{
		"docker service ps --filter desired-state=running --format '{{.ID}}' 'app_newest'": {Stdout: "new-task\n"},
		"docker inspect --format '{{.Status.Timestamp}}' 'new-task'":                       {Stdout: "2026-08-23T12:00:00.000000000Z\n"},
		"docker service ps --filter desired-state=running --format '{{.ID}}' 'app_oldest'": {Stdout: "old-task\n"},
		"docker inspect --format '{{.Status.Timestamp}}' 'old-task'":                       {Stdout: "2026-08-23T10:00:00Z\n"},
		"docker service ps --filter desired-state=running --format '{{.ID}}' 'app_middle'": {Stdout: "middle-task-1\nmiddle-task-2\n"},
		"docker inspect --format '{{.Status.Timestamp}}' 'middle-task-1' 'middle-task-2'":  {Stdout: "2026-08-23T10:30:00Z\n2026-08-23T11:00:00Z\n"},
	}}

	if err := sortLogServicesByContainerStart(context.Background(), runner, services); err != nil {
		t.Fatal(err)
	}

	want := []DockerSwarmLogService{
		{Stack: "app", Name: "oldest"},
		{Stack: "app", Name: "middle"},
		{Stack: "app", Name: "newest"},
	}
	if !reflect.DeepEqual(services, want) {
		t.Fatalf("services = %#v, want %#v", services, want)
	}
}

func TestSortLogServicesByContainerStartPreservesEqualOrder(t *testing.T) {
	services := []DockerSwarmLogService{
		{Stack: "app", Name: "first"},
		{Stack: "app", Name: "second"},
	}
	runner := logOrderRunner{results: map[string]CommandResult{
		"docker service ps --filter desired-state=running --format '{{.ID}}' 'app_first'":  {},
		"docker service ps --filter desired-state=running --format '{{.ID}}' 'app_second'": {},
	}}

	if err := sortLogServicesByContainerStart(context.Background(), runner, services); err != nil {
		t.Fatal(err)
	}

	want := []DockerSwarmLogService{
		{Stack: "app", Name: "first"},
		{Stack: "app", Name: "second"},
	}
	if !reflect.DeepEqual(services, want) {
		t.Fatalf("services = %#v, want %#v", services, want)
	}
}
