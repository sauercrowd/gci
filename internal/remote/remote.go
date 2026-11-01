// Package remote runs the gci agent on a target host, over SSH or locally.
package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	gcissh "github.com/sauercrowd/gci/ssh"
	cryptossh "golang.org/x/crypto/ssh"
)

const AgentPath = "/usr/local/bin/gci"

// ErrAgent marks failures the agent already reported on stderr.
var ErrAgent = errors.New("agent failed")

type Host struct {
	run  func(ctx context.Context, command string, stdin io.Reader, stdout, stderr io.Writer) error
	root bool
	ssh  *cryptossh.Client
	Name string
}

// Local returns a host that runs commands on this machine.
func Local() *Host {
	return &Host{
		Name: "local",
		root: os.Geteuid() == 0,
		run: func(ctx context.Context, command string, stdin io.Reader, stdout, stderr io.Writer) error {
			cmd := exec.CommandContext(ctx, "sh", "-c", command)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
			return cmd.Run()
		},
	}
}

// SSH connects to a remote host. The connection is reused for all commands.
func SSH(ctx context.Context, name string, target gcissh.Target) (*Host, error) {
	if target.Timeout == 0 {
		target.Timeout = 15 * time.Second
	}
	client, err := gcissh.Dial(ctx, target)
	if err != nil {
		return nil, err
	}
	h := &Host{Name: name, ssh: client, root: target.User == "root"}
	h.run = func(ctx context.Context, command string, stdin io.Reader, stdout, stderr io.Writer) error {
		sess, err := client.NewSession()
		if err != nil {
			return err
		}
		defer sess.Close()
		sess.Stdin, sess.Stdout, sess.Stderr = stdin, stdout, stderr
		if err := sess.Start(command); err != nil {
			return err
		}
		done := make(chan error, 1)
		go func() { done <- sess.Wait() }()
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			_ = sess.Signal(cryptossh.SIGTERM)
			_ = sess.Close()
			return ctx.Err()
		}
	}
	return h, nil
}

func (h *Host) Close() error {
	if h.ssh != nil {
		return h.ssh.Close()
	}
	return nil
}

func (h *Host) sudo(command string) string {
	if h.root {
		return command
	}
	return "sudo -n " + command
}

// Output runs a command and returns its stdout.
func (h *Host) Output(ctx context.Context, command string) (string, error) {
	var out, errb bytes.Buffer
	if err := h.run(ctx, command, nil, &out, &errb); err != nil {
		return "", fmt.Errorf("%s: %w: %s", command, err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// Agent runs `gci agent <args>` as root on the host.
func (h *Host) Agent(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	err := h.run(ctx, h.sudo(AgentPath+" agent "+strings.Join(quoted, " ")), stdin, stdout, stderr)
	var exitErr *exec.ExitError
	var sshExit *cryptossh.ExitError
	if errors.As(err, &exitErr) || errors.As(err, &sshExit) {
		return ErrAgent
	}
	return err
}

// EnsureAgent installs this gci binary on the host if it differs from the
// installed one. Set GCI_AGENT_BINARY to ship a different (e.g. linux) build.
func (h *Host) EnsureAgent(ctx context.Context, w io.Writer) error {
	bin := os.Getenv("GCI_AGENT_BINARY")
	if bin == "" {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		bin = self
	}
	content, err := os.ReadFile(bin)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(content)
	want := hex.EncodeToString(sum[:])

	out, err := h.Output(ctx, "uname -sm; sha256sum "+AgentPath+" 2>/dev/null || true")
	if err != nil {
		return err
	}
	lines := strings.SplitN(out, "\n", 2)
	if len(lines) == 2 && strings.HasPrefix(lines[1], want) {
		return nil
	}
	if os.Getenv("GCI_AGENT_BINARY") == "" {
		if want := goPlatform(lines[0]); want != runtime.GOOS+"/"+runtime.GOARCH {
			return fmt.Errorf("host is %s but this gci binary is %s/%s; set GCI_AGENT_BINARY to a %s build", want, runtime.GOOS, runtime.GOARCH, want)
		}
	}
	fmt.Fprintf(w, "installing gci agent on %s\n", h.Name)
	cmd := h.sudo(fmt.Sprintf("sh -c 'tmp=$(mktemp %s.XXXXXX) || exit 1; if ! cat > \"$tmp\" || ! chmod 755 \"$tmp\" || ! mv \"$tmp\" %s; then rm -f \"$tmp\"; exit 1; fi'", AgentPath, AgentPath))
	var errb bytes.Buffer
	if err := h.run(ctx, cmd, bytes.NewReader(content), io.Discard, &errb); err != nil {
		return fmt.Errorf("upload agent: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	return nil
}

func goPlatform(unameSM string) string {
	f := strings.Fields(strings.ToLower(unameSM))
	if len(f) != 2 {
		return unameSM
	}
	arch := map[string]string{"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[f[1]]
	if arch == "" {
		arch = f[1]
	}
	return f[0] + "/" + arch
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:,@", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
