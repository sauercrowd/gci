package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/sauercrowd/gci/internal/remote"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "gci",
	Short: "Deploy services into Nix-provisioned Incus containers behind Envoy",
	Long: `gci deploys services onto your own hosts using reusable images.

A host is bootstrapped once (Incus, Nix, a bridge network, a base container
image and an Envoy front door). Each referenced [images.*] is prepared once
per deploy: its Nix packages are realised and its ordered steps run to prepare
a release, optionally carrying files over from the previous build via
carry_over. Every service referencing the image gets its own copy-on-write
container from that release.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		if !errors.Is(err, remote.ErrAgent) {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringP("server", "s", "", "server name (default: gci.toml server, or the only configured server)")
	rootCmd.PersistentFlags().StringP("config", "c", "gci.toml", "path to gci.toml")
}
