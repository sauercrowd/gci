package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/pelletier/go-toml/v2"
	"github.com/sauercrowd/gci/internal/remote"
	gcissh "github.com/sauercrowd/gci/ssh"
	"github.com/spf13/cobra"
)

type server struct {
	Name       string `toml:"name"`
	Host       string `toml:"host,omitempty"`
	User       string `toml:"user,omitempty"`
	PrivateKey string `toml:"private_key,omitempty"`
	Local      bool   `toml:"local,omitempty"`
}

type serverFile struct {
	Servers []server `toml:"servers"`
}

func serversPath() (string, error) {
	if p := os.Getenv("GCI_SERVERS_FILE"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gci", "servers.toml"), nil
}

func loadServers() ([]server, error) {
	p, err := serversPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f serverFile
	if err := toml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return f.Servers, nil
}

func saveServers(list []server) error {
	p, err := serversPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	raw, err := toml.Marshal(serverFile{Servers: list})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".servers-*.toml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, p)
}

func findServer(name string) (server, error) {
	list, err := loadServers()
	if err != nil {
		return server{}, err
	}
	if name == "" {
		if len(list) == 1 {
			return list[0], nil
		}
		return server{}, fmt.Errorf("no server selected; pass --server or set `server` in gci.toml (configured: %d)", len(list))
	}
	for _, s := range list {
		if s.Name == name {
			return s, nil
		}
	}
	return server{}, fmt.Errorf("server %q not found; add it with `gci server add`", name)
}

func connect(ctx context.Context, s server) (*remote.Host, error) {
	if s.Local {
		h := remote.Local()
		h.Name = s.Name
		return h, nil
	}
	return remote.SSH(ctx, s.Name, gcissh.Target{User: s.User, Host: s.Host, PrivateKeyPath: s.PrivateKey})
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func newServerCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "server", Short: "Manage target hosts"}

	var host, usr, key string
	var local bool
	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Register a host (SSH, or --local for this machine)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s := server{Name: args[0], Local: local}
			if !local {
				if host == "" || key == "" {
					return fmt.Errorf("--host and --private-key are required (or pass --local)")
				}
				if usr == "" {
					if u, err := user.Current(); err == nil {
						usr = u.Username
					}
				}
				s.Host, s.User, s.PrivateKey = host, usr, expandHome(key)
				h, err := connect(cmd.Context(), s)
				if err != nil {
					return fmt.Errorf("cannot reach %s: %w", args[0], err)
				}
				h.Close()
			}
			list, err := loadServers()
			if err != nil {
				return err
			}
			replaced := false
			for i := range list {
				if list[i].Name == s.Name {
					list[i], replaced = s, true
				}
			}
			if !replaced {
				list = append(list, s)
			}
			if err := saveServers(list); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "saved server %q\n", s.Name)
			return nil
		},
	}
	add.Flags().StringVar(&host, "host", "", "SSH host[:port]")
	add.Flags().StringVarP(&usr, "user", "u", "", "SSH user (default: current user)")
	add.Flags().StringVarP(&key, "private-key", "i", "", "SSH private key path")
	add.Flags().BoolVar(&local, "local", false, "target this machine directly, without SSH")

	ls := &cobra.Command{
		Use:   "ls",
		Short: "List hosts",
		RunE: func(cmd *cobra.Command, args []string) error {
			list, err := loadServers()
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tTARGET")
			for _, s := range list {
				target := s.User + "@" + s.Host
				if s.Local {
					target = "local"
				}
				fmt.Fprintf(tw, "%s\t%s\n", s.Name, target)
			}
			return tw.Flush()
		},
	}

	rm := &cobra.Command{
		Use:   "rm <name>",
		Short: "Forget a host",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			list, err := loadServers()
			if err != nil {
				return err
			}
			out := list[:0]
			for _, s := range list {
				if s.Name != args[0] {
					out = append(out, s)
				}
			}
			if len(out) == len(list) {
				return fmt.Errorf("server %q not found", args[0])
			}
			return saveServers(out)
		},
	}
	cmd.AddCommand(add, ls, rm)
	return cmd
}

func init() { rootCmd.AddCommand(newServerCmd()) }
