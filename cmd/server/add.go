package server

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"time"

	gcissh "github.com/sauercrowd/gci/ssh"
	"github.com/spf13/cobra"
)

const connectTimeout = 5 * time.Second
const defaultServiceDir = "./gci-deployments"

func newAddCommand() *cobra.Command {
	var addUser string
	var addHost string
	var addPrivateKeyPath string
	var addServiceDir string
	var addSkipCheck bool

	addCmd := &cobra.Command{
		Use:          "add <name>",
		Short:        "Add a server",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			username := addUser
			if username == "" {
				resolvedUser, err := currentUsername()
				if err != nil {
					return err
				}
				username = resolvedUser
			}

			privateKeyPath, err := expandUserPath(addPrivateKeyPath)
			if err != nil {
				return err
			}

			if !addSkipCheck {
				checkCtx, cancel := context.WithTimeout(cmd.Context(), connectTimeout)
				defer cancel()

				target := gcissh.Target{
					User:           username,
					Host:           addHost,
					PrivateKeyPath: privateKeyPath,
				}
				if err := gcissh.CheckReachable(checkCtx, target); err != nil {
					return fmt.Errorf("server %q is not reachable over SSH: %w", name, err)
				}
			}

			entries, err := load()
			if err != nil {
				return err
			}

			for i := range entries {
				if entryName(entries[i]) == name {
					entries[i].Name = name
					entries[i].Host = addHost
					entries[i].User = username
					entries[i].PrivateKey = privateKeyPath
					entries[i].ServiceDir = addServiceDir
					if err := save(entries); err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "updated server %q\n", name)
					return nil
				}
			}

			entries = append(entries, entry{
				Name:       name,
				User:       username,
				Host:       addHost,
				PrivateKey: privateKeyPath,
				ServiceDir: addServiceDir,
			})
			if err := save(entries); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "added server %q\n", name)
			return nil
		},
	}

	addCmd.Flags().StringVar(&addHost, "host", "", "SSH host")
	addCmd.Flags().StringVarP(&addUser, "user", "u", "", "SSH username (defaults to current OS user)")
	addCmd.Flags().StringVarP(&addPrivateKeyPath, "private-key", "i", "", "Path to SSH private key")
	addCmd.Flags().StringVar(&addServiceDir, "service-dir", defaultServiceDir, "Remote base directory for deployed services")
	addCmd.Flags().BoolVar(&addSkipCheck, "skip-check", false, "Skip SSH connectivity check before saving")
	_ = addCmd.MarkFlagRequired("host")
	_ = addCmd.MarkFlagRequired("private-key")

	return addCmd
}

func currentUsername() (string, error) {
	currentUser, err := user.Current()
	if err == nil && currentUser.Username != "" {
		return currentUser.Username, nil
	}

	fallback := os.Getenv("USER")
	if fallback != "" {
		return fallback, nil
	}

	return "", fmt.Errorf("failed to determine current user; pass --user explicitly")
}
