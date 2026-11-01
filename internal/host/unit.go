package host

import (
	"fmt"
	"sort"
	"strings"
)

type unitSpec struct {
	Description string
	User        string
	EnvFile     string
	WorkingDir  string
	ExecPre     []string
	ExecStart   string
}

func renderUnit(u unitSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Unit]\nDescription=%s\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\n", u.Description)
	fmt.Fprintf(&b, "User=%s\n", u.User)
	if u.EnvFile != "" {
		fmt.Fprintf(&b, "EnvironmentFile=%s\n", u.EnvFile)
	}
	if u.WorkingDir != "" {
		fmt.Fprintf(&b, "WorkingDirectory=%s\n", u.WorkingDir)
	}
	for _, p := range u.ExecPre {
		fmt.Fprintf(&b, "ExecStartPre=%s\n", p)
	}
	fmt.Fprintf(&b, "ExecStart=%s\n", u.ExecStart)
	b.WriteString("Restart=always\nRestartSec=2\nLimitNOFILE=1048576\nTimeoutStopSec=30\n\n[Install]\nWantedBy=multi-user.target\n")
	return b.String()
}

// renderEnvFile renders a systemd EnvironmentFile with sorted, quoted values.
func renderEnvFile(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(env[k])
		fmt.Fprintf(&b, "%s=\"%s\"\n", k, v)
	}
	return b.String()
}

// shellScript turns a command into a script; single-line commands are exec'd
// so the service process receives signals directly.
func shellScript(command string) string {
	command = strings.TrimSpace(command)
	if !strings.ContainsAny(command, "\n;&|") {
		command = "exec " + command
	}
	return "#!/bin/sh\nset -e\n" + command + "\n"
}
