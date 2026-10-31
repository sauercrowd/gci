package host

import (
	"fmt"
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
