package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sauercrowd/gci/internal/config"
	"github.com/spf13/cobra"
)

const starterConfig = `app = "%s"

[services.web]
packages = ["python3"]
command = "python3 -m http.server 8080 --directory /home/gci"
port = 8080
health = "/"
routes = [{ host = "*", prefix = "/" }]
# Reuse "dependencies", "build", or "nothing" between source builds.
reuse = "dependencies"
# migrate = "npm run db:migrate"
`

var nonNameChars = regexp.MustCompile(`[^a-z0-9-]+`)

func defaultAppName(dir string) string {
	name := nonNameChars.ReplaceAllString(strings.ToLower(filepath.Base(dir)), "-")
	name = strings.TrimLeft(name, "0123456789-")
	if len(name) > 24 {
		name = name[:24]
	}
	name = strings.TrimRight(name, "-")
	if config.ValidateAppName(name) != nil {
		return "app"
	}
	return name
}

func writeStarterConfig(path, app string) error {
	if err := config.ValidateAppName(app); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s already exists", path)
	}
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, starterConfig, app); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func init() {
	var app string
	cmd := &cobra.Command{
		Use:  "init",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("config")
			if app == "" {
				dir, err := filepath.Abs(filepath.Dir(path))
				if err != nil {
					return err
				}
				app = defaultAppName(dir)
			}
			if err := writeStarterConfig(path, app); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (app %q)\n", path, app)
			return nil
		},
	}
	cmd.Flags().StringVar(&app, "app", "", "app name")
	rootCmd.AddCommand(cmd)
}
