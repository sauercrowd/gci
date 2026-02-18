package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/sauercrowd/gci/service"
	"github.com/spf13/cobra"
)

type templateContext struct {
	GitSHA      string
	GitShortSHA string
	AppNetwork  string
	ServiceName string
}

func newTemplateCommand() *cobra.Command {
	templateCmd := &cobra.Command{
		Use:          "template",
		Short:        "Template-related commands",
		SilenceUsage: true,
	}
	templateCmd.AddCommand(newTemplateRenderCommand())
	return templateCmd
}

func newTemplateRenderCommand() *cobra.Command {
	var inplace bool

	renderCmd := &cobra.Command{
		Use:          "render <input_file> [output_file]",
		Short:        "Render a template file with git metadata",
		SilenceUsage: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 || len(args) > 2 {
				return fmt.Errorf("accepts 1 or 2 args")
			}
			if inplace && len(args) == 2 {
				return fmt.Errorf("cannot use -i/--inplace with output_file")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			inputPath := args[0]
			outputPath := ""
			if len(args) == 2 {
				outputPath = args[1]
			}
			if inplace {
				outputPath = inputPath
			}

			rendered, err := renderTemplateFile(inputPath)
			if err != nil {
				return err
			}

			if outputPath == "" {
				_, err := cmd.OutOrStdout().Write(rendered)
				return err
			}

			if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
				return fmt.Errorf("failed to create output directory for %q: %w", outputPath, err)
			}
			if err := os.WriteFile(outputPath, rendered, 0o644); err != nil {
				return fmt.Errorf("failed to write rendered template to %q: %w", outputPath, err)
			}
			return nil
		},
	}

	renderCmd.Flags().BoolVarP(&inplace, "inplace", "i", false, "Render in-place to input_file")
	return renderCmd
}

func renderTemplateFile(inputPath string) ([]byte, error) {
	content, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read input template file %q: %w", inputPath, err)
	}

	ctx, err := discoverTemplateContext(filepath.Dir(inputPath))
	if err != nil {
		return nil, err
	}

	rendered, err := renderTemplateString(filepath.Base(inputPath), string(content), filepath.Dir(inputPath), ctx)
	if err != nil {
		return nil, err
	}

	return []byte(rendered), nil
}

func renderTemplateString(templateName, content, gitDir string, ctx templateContext) (string, error) {
	gitSHA, gitShortSHA, err := gitSHAs(gitDir)
	if err != nil {
		return "", err
	}

	ctx.GitSHA = gitSHA
	ctx.GitShortSHA = gitShortSHA

	tmpl, err := template.New(templateName).
		Option("missingkey=error").
		Funcs(template.FuncMap{
			"git_sha": func() string {
				return ctx.GitSHA
			},
			"git_short_sha": func() string {
				return ctx.GitShortSHA
			},
			"app_network": func() string {
				return ctx.AppNetwork
			},
		}).
		Parse(content)
	if err != nil {
		return "", fmt.Errorf("failed to parse template %q: %w", templateName, err)
	}

	data := map[string]string{
		"GitSHA":      ctx.GitSHA,
		"GitShortSHA": ctx.GitShortSHA,
		"AppNetwork":  ctx.AppNetwork,
		"ServiceName": ctx.ServiceName,
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("failed to render template %q: %w", templateName, err)
	}

	return out.String(), nil
}

func discoverTemplateContext(startDir string) (templateContext, error) {
	ctx := templateContext{}
	configPath, found, err := findNearestConfigPath(startDir)
	if err != nil {
		return templateContext{}, err
	}
	if !found {
		return ctx, nil
	}

	cfg, err := service.ReadConfigFile(configPath)
	if err != nil {
		// Template rendering should still work with git-only values even if gci.toml is invalid.
		return ctx, nil
	}

	ctx.ServiceName = cfg.Name
	if cfg.DriverDockerSwarm != nil {
		ctx.AppNetwork = cfg.DriverDockerSwarm.ResolvedAppNetwork(cfg.Name)
	}

	return ctx, nil
}

func findNearestConfigPath(startDir string) (string, bool, error) {
	dir := startDir
	for {
		candidate := filepath.Join(dir, "gci.toml")
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, true, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return "", false, fmt.Errorf("failed to inspect %q: %w", candidate, err)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false, nil
}

func gitSHAs(dir string) (string, string, error) {
	shaResult, err := runGitCommand(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve git SHA (is %q inside a git repo?): %w", dir, err)
	}
	shortResult, err := runGitCommand(dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve short git SHA (is %q inside a git repo?): %w", dir, err)
	}
	return shaResult, shortResult, nil
}

func runGitCommand(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if trimmed == "" {
			return "", err
		}
		return "", fmt.Errorf("%w: %s", err, trimmed)
	}
	return strings.TrimSpace(string(out)), nil
}

func init() {
	rootCmd.AddCommand(newTemplateCommand())
}
