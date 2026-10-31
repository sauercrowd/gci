package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"strconv"

	"github.com/sauercrowd/gci/internal/host"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:                "agent <command>",
		Hidden:             true,
		DisableFlagParsing: true,
		Args:               cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "apply":
				var plan host.Plan
				if err := json.NewDecoder(cmd.InOrStdin()).Decode(&plan); err != nil {
					return err
				}
				return host.Apply(cmd.Context(), cmd.OutOrStdout(), plan)
			case "bootstrap":
				return runBootstrapAgent(cmd, args[1:])
			case "firewall":
				return host.Firewall(cmd.Context())
			case "status":
				if len(args) != 2 {
					return fmt.Errorf("usage: gci agent status <app>")
				}
				return host.Status(cmd.Context(), cmd.OutOrStdout(), args[1])
			case "logs":
				return runLogsAgent(cmd, args[1:])
			case "rm":
				return runRemoveAgent(cmd, args[1:])
			default:
				return fmt.Errorf("unknown agent command %q", args[0])
			}
		},
	})
}

func runBootstrapAgent(cmd *cobra.Command, args []string) error {
	var subnet, vipRange, storageSize string
	var httpPort int
	fs := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	fs.SetOutput(cmd.ErrOrStderr())
	fs.StringVar(&subnet, "subnet", "10.77.0.1/24", "bridge subnet")
	fs.StringVar(&vipRange, "vip-range", "10.77.1.0/24", "VIP range")
	fs.StringVar(&storageSize, "storage-size", "50GiB", "storage pool size")
	fs.IntVar(&httpPort, "http-port", 80, "Envoy host port")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected bootstrap arguments: %v", fs.Args())
	}
	return host.Bootstrap(cmd.Context(), cmd.OutOrStdout(), host.BootstrapOptions{
		Subnet: subnet, VIPRange: vipRange, StorageSize: storageSize, HTTPPort: httpPort,
	})
}

func runLogsAgent(cmd *cobra.Command, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: gci agent logs <app> <service> [-n lines] [-f]")
	}
	lines, follow := 100, false
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "-n", "--lines":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a value", args[i])
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 0 {
				return fmt.Errorf("invalid line count %q", args[i+1])
			}
			lines = n
			i++
		case "-f", "--follow":
			follow = true
		default:
			return fmt.Errorf("unknown logs option %q", args[i])
		}
	}
	return host.Logs(cmd.Context(), cmd.OutOrStdout(), args[0], args[1], lines, follow)
}

func runRemoveAgent(cmd *cobra.Command, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: gci agent rm <app> [--volumes]")
	}
	volumes := false
	for _, arg := range args[1:] {
		if arg != "--volumes" {
			return fmt.Errorf("unknown rm option %q", arg)
		}
		volumes = true
	}
	return host.Remove(cmd.Context(), cmd.OutOrStdout(), args[0], volumes)
}
