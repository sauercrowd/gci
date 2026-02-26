package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	gcissh "github.com/sauercrowd/gci/ssh"
)

func fetchNodeLabels(ctx context.Context, target gcissh.Target) (map[string]string, error) {
	nodeID, err := swarmNodeID(ctx, target)
	if err != nil {
		return nil, err
	}

	command := fmt.Sprintf("docker node inspect %s --format '{{json .Spec.Labels}}'", shellQuote(nodeID))
	result, err := gcissh.RunCommand(ctx, target, command)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect docker node labels: %w", err)
	}

	raw := strings.TrimSpace(result.Stdout)
	if raw == "" || raw == "null" {
		return map[string]string{}, nil
	}

	var labels map[string]string
	if err := json.Unmarshal([]byte(raw), &labels); err != nil {
		return nil, fmt.Errorf("failed to parse docker node labels: %w", err)
	}

	if labels == nil {
		return map[string]string{}, nil
	}
	return labels, nil
}

func addNodeLabel(ctx context.Context, target gcissh.Target, key, value string) error {
	nodeID, err := swarmNodeID(ctx, target)
	if err != nil {
		return err
	}

	command := fmt.Sprintf(
		"docker node update --label-add %s %s",
		shellQuote(fmt.Sprintf("%s=%s", key, value)),
		shellQuote(nodeID),
	)
	result, err := gcissh.RunCommand(ctx, target, command)
	if err != nil {
		return fmt.Errorf("failed to add docker node label %q: %w", key, errWithOutput(err, result))
	}
	return nil
}

func removeNodeLabel(ctx context.Context, target gcissh.Target, key string) error {
	nodeID, err := swarmNodeID(ctx, target)
	if err != nil {
		return err
	}

	command := fmt.Sprintf(
		"docker node update --label-rm %s %s",
		shellQuote(key),
		shellQuote(nodeID),
	)
	result, err := gcissh.RunCommand(ctx, target, command)
	if err != nil {
		return fmt.Errorf("failed to remove docker node label %q: %w", key, errWithOutput(err, result))
	}
	return nil
}

func swarmNodeID(ctx context.Context, target gcissh.Target) (string, error) {
	result, err := gcissh.RunCommand(ctx, target, "docker info --format '{{.Swarm.NodeID}}'")
	if err != nil {
		return "", fmt.Errorf("failed to read docker swarm node id: %w", errWithOutput(err, result))
	}

	nodeID := strings.TrimSpace(result.Stdout)
	if nodeID == "" {
		return "", fmt.Errorf("docker swarm is not active on this server")
	}
	return nodeID, nil
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "-"
	}

	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%s", key, labels[key]))
	}

	return strings.Join(pairs, ",")
}

func parseLabelPair(value string) (string, string, error) {
	trimmed := strings.TrimSpace(value)
	parts := strings.SplitN(trimmed, "=", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("label must be in key=value form")
	}

	key := strings.TrimSpace(parts[0])
	labelValue := strings.TrimSpace(parts[1])
	if key == "" {
		return "", "", fmt.Errorf("label key must not be empty")
	}
	if labelValue == "" {
		return "", "", fmt.Errorf("label value must not be empty")
	}
	return key, labelValue, nil
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func errWithOutput(err error, result gcissh.CommandResult) error {
	stderr := strings.TrimSpace(result.Stderr)
	stdout := strings.TrimSpace(result.Stdout)
	if stderr == "" && stdout == "" {
		return err
	}
	if stderr != "" && stdout != "" {
		return fmt.Errorf("%w\nstderr:\n%s\nstdout:\n%s", err, stderr, stdout)
	}
	if stderr != "" {
		return fmt.Errorf("%w\nstderr:\n%s", err, stderr)
	}
	return fmt.Errorf("%w\nstdout:\n%s", err, stdout)
}
