package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/sauercrowd/gci/internal/config"
	"github.com/sauercrowd/gci/internal/host"
	"github.com/sauercrowd/gci/internal/remote"
	"github.com/spf13/cobra"
)

// project loads gci.toml (if required) and connects to the selected server.
func project(cmd *cobra.Command, needConfig bool) (*config.Config, *remote.Host, error) {
	path, _ := cmd.Flags().GetString("config")
	name, _ := cmd.Flags().GetString("server")
	var cfg *config.Config
	if needConfig || fileExists(path) {
		var err error
		if cfg, err = config.Load(path); err != nil {
			return nil, nil, err
		}
		if name == "" {
			name = cfg.Server
		}
	}
	s, err := findServer(name)
	if err != nil {
		return nil, nil, err
	}
	h, err := connect(cmd.Context(), s)
	if err != nil {
		return nil, nil, err
	}
	if err := h.EnsureAgent(cmd.Context(), cmd.ErrOrStderr()); err != nil {
		h.Close()
		return nil, nil, err
	}
	return cfg, h, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func init() {
	var subnet, vipRange string
	var httpPort, httpsPort int
	var storageSize string
	var acmeEmail, acmeDirectory, acmeCABundle string
	bootstrap := &cobra.Command{
		Use:   "bootstrap",
		Short: "Prepare a host once: Incus, Nix, network, base image, Envoy front door",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, h, err := project(cmd, false)
			if err != nil {
				return err
			}
			defer h.Close()
			return h.Agent(cmd.Context(), []string{
				"bootstrap", "--subnet", subnet,
				"--http-port", strconv.Itoa(httpPort), "--https-port", strconv.Itoa(httpsPort),
				"--vip-range", vipRange, "--storage-size", storageSize,
				"--acme-email", acmeEmail, "--acme-directory", acmeDirectory,
				"--acme-ca-bundle", acmeCABundle,
			},
				nil, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	bootstrap.Flags().StringVar(&subnet, "subnet", "", "gateway address/CIDR of the gci bridge (defaults to the existing bridge or 10.77.0.1/24)")
	bootstrap.Flags().IntVar(&httpPort, "http-port", 80, "host port Envoy listens on")
	bootstrap.Flags().IntVar(&httpsPort, "https-port", 443, "host HTTPS port Envoy listens on")
	bootstrap.Flags().StringVar(&vipRange, "vip-range", "10.77.1.0/24", "service VIP range, routed to Envoy")
	bootstrap.Flags().StringVar(&storageSize, "storage-size", "50GiB", "size of the btrfs-backed gci storage pool")
	bootstrap.Flags().StringVar(&acmeEmail, "acme-email", "", "optional ACME account email")
	bootstrap.Flags().StringVar(&acmeDirectory, "acme-directory", host.DefaultACMEDirectory, "ACME directory URL")
	bootstrap.Flags().StringVar(&acmeCABundle, "acme-ca-bundle", "", "host PEM bundle trusted for the ACME directory")

	var clean bool
	var restart []string
	deploy := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy the services in gci.toml",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, h, err := project(cmd, true)
			if err != nil {
				return err
			}
			defer h.Close()
			if err := host.ValidateRestartNames(cfg, restart); err != nil {
				return err
			}
			ctx := cmd.Context()
			plan := host.Plan{Config: cfg, Sources: map[string]string{}, Clean: clean || cleanEnabled(os.Getenv("GCI_CLEAN")), Restart: restart}
			plan.Sources, err = uploadImageSources(ctx, cmd, h, cfg, cfg.DeployImages())
			if err != nil {
				return err
			}
			raw, err := json.Marshal(plan)
			if err != nil {
				return err
			}
			return h.Agent(ctx, []string{"apply"}, bytes.NewReader(raw), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	deploy.Flags().BoolVar(&clean, "clean", false, "rebuild images from a fresh base")
	deploy.Flags().StringSliceVar(&restart, "restart", nil, "apply pending changes to a manual service; unchanged services stay unchanged")

	var runClean bool
	run := &cobra.Command{
		Use:   "run <job>",
		Short: "Run one job without running its dependencies",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, h, err := project(cmd, true)
			if err != nil {
				return err
			}
			defer h.Close()
			job, ok := cfg.Jobs[args[0]]
			if !ok {
				return fmt.Errorf("unknown job %q", args[0])
			}
			if len(job.Volumes) > 0 {
				owner := jobVolumeOwner(cfg, job)
				return fmt.Errorf("job %q uses volumes of service %q; it only runs during %s's rollout", args[0], owner, owner)
			}
			sources, err := uploadImageSources(cmd.Context(), cmd, h, cfg, []string{job.Image})
			if err != nil {
				return err
			}
			raw, err := json.Marshal(host.RunPlan{
				Config: cfg, Sources: sources, Job: args[0],
				Clean: runClean || cleanEnabled(os.Getenv("GCI_CLEAN")),
			})
			if err != nil {
				return err
			}
			err = h.Agent(cmd.Context(), []string{"run-job"}, bytes.NewReader(raw), cmd.OutOrStdout(), cmd.ErrOrStderr())
			return preserveJobExitCode(err)
		},
	}
	run.Flags().BoolVar(&runClean, "clean", false, "rebuild the job image from a fresh base")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show the services of this app",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, h, err := project(cmd, true)
			if err != nil {
				return err
			}
			defer h.Close()
			return h.Agent(cmd.Context(), []string{"status", cfg.App}, nil, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}

	var lines int
	var follow bool
	logs := &cobra.Command{
		Use:   "logs <service>",
		Short: "Show a service's logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, h, err := project(cmd, true)
			if err != nil {
				return err
			}
			defer h.Close()
			a := []string{"logs", cfg.App, args[0], "-n", strconv.Itoa(lines)}
			if follow {
				a = append(a, "-f")
			}
			return h.Agent(cmd.Context(), a, nil, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	logs.Flags().IntVarP(&lines, "lines", "n", 100, "number of lines")
	logs.Flags().BoolVarP(&follow, "follow", "f", false, "follow")

	var volumes bool
	rm := &cobra.Command{
		Use:   "rm",
		Short: "Remove all containers of this app (volumes are kept unless --volumes)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, h, err := project(cmd, true)
			if err != nil {
				return err
			}
			defer h.Close()
			a := []string{"rm", cfg.App}
			if volumes {
				a = append(a, "--volumes")
			}
			return h.Agent(cmd.Context(), a, nil, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	rm.Flags().BoolVar(&volumes, "volumes", false, "also delete the app's volumes (data loss)")

	secret := &cobra.Command{Use: "secret", Short: "Manage app secrets"}
	secretSet := &cobra.Command{
		Use:   "set <name>",
		Short: "Read a secret value from stdin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, h, err := project(cmd, true)
			if err != nil {
				return err
			}
			defer h.Close()
			if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "reading secret from stdin, end with Ctrl-D")
			}
			value, err := io.ReadAll(io.LimitReader(os.Stdin, 64*1024+1))
			if err != nil {
				return err
			}
			if len(value) > 64*1024 {
				return fmt.Errorf("secret exceeds 64 KiB")
			}
			if bytes.HasSuffix(value, []byte("\r\n")) {
				value = value[:len(value)-2]
			} else if bytes.HasSuffix(value, []byte("\n")) {
				value = value[:len(value)-1]
			}
			if err := h.Agent(cmd.Context(), []string{"secret-set", cfg.App, args[0]}, bytes.NewReader(value), cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			return nil
		},
	}
	secretList := &cobra.Command{
		Use:   "ls",
		Short: "List secret names",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, h, err := project(cmd, true)
			if err != nil {
				return err
			}
			defer h.Close()
			return h.Agent(cmd.Context(), []string{"secret-ls", cfg.App}, nil, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	secretRemove := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, h, err := project(cmd, true)
			if err != nil {
				return err
			}
			defer h.Close()
			return h.Agent(cmd.Context(), []string{"secret-rm", cfg.App, args[0]}, nil, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	secret.AddCommand(secretSet, secretList, secretRemove)
	rootCmd.AddCommand(bootstrap, deploy, run, status, logs, rm, secret)
}

func uploadImageSources(ctx context.Context, cmd *cobra.Command, h *remote.Host, cfg *config.Config, images []string) (map[string]string, error) {
	sources := make(map[string]string, len(images))
	for _, imageName := range images {
		image := cfg.Images[imageName]
		if image.Source == "" {
			continue
		}
		tarball, hash, err := remote.PackSource(image.Source)
		if err != nil {
			return nil, fmt.Errorf("%s: pack %s: %w", imageName, image.Source, err)
		}
		sources[imageName] = hash
		var out bytes.Buffer
		if err := h.Agent(ctx, []string{"has-source", cfg.App, imageName, hash}, nil, &out, io.Discard); err == nil {
			continue
		}
		fmt.Fprintf(cmd.OutOrStdout(), "==> %s: uploading source %s (%d KiB)\n", imageName, hash, len(tarball)/1024)
		if err := h.Agent(ctx, []string{"receive-source", cfg.App, imageName, hash}, bytes.NewReader(tarball), cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
			return nil, fmt.Errorf("%s: upload source: %w", imageName, err)
		}
	}
	return sources, nil
}

func preserveJobExitCode(err error) error {
	var agentExit *remote.AgentExitError
	if errors.As(err, &agentExit) {
		return &host.JobExitError{Code: agentExit.Code}
	}
	return err
}

func jobVolumeOwner(cfg *config.Config, job *config.Job) string {
	for name, svc := range cfg.Services {
		owns := true
		for volume := range job.Volumes {
			if _, ok := svc.Volumes[volume]; !ok {
				owns = false
				break
			}
		}
		if owns {
			return name
		}
	}
	return ""
}

func cleanEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func sortedNames(cfg *config.Config) []string {
	order, _ := cfg.Order()
	return order
}
