package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	cryptossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type Target struct {
	User           string
	Host           string
	PrivateKeyPath string
	Timeout        time.Duration
}

type CommandResult struct {
	Stdout string
	Stderr string
}

func CheckReachable(ctx context.Context, target Target) error {
	client, err := Dial(ctx, target)
	if err != nil {
		return err
	}
	return client.Close()
}

func RunCommand(ctx context.Context, target Target, command string) (CommandResult, error) {
	client, err := Dial(ctx, target)
	if err != nil {
		return CommandResult{}, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return CommandResult{}, fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	if err := session.Run(command); err != nil {
		return CommandResult{
			Stdout: stdout.String(),
			Stderr: stderr.String(),
		}, fmt.Errorf("failed to run remote command: %w", err)
	}

	return CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}, nil
}

func RunCommandStream(ctx context.Context, target Target, command string, stdout, stderr io.Writer) error {
	client, err := Dial(ctx, target)
	if err != nil {
		return err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	session.Stdout = stdout
	session.Stderr = stderr

	if err := session.Start(command); err != nil {
		return fmt.Errorf("failed to start remote command: %w", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- session.Wait()
	}()

	select {
	case err := <-waitCh:
		if err != nil {
			return fmt.Errorf("failed to run remote command: %w", err)
		}
		return nil
	case <-ctx.Done():
		_ = session.Close()
		_ = client.Close()
		<-waitCh
		return ctx.Err()
	}
}

func Dial(ctx context.Context, target Target) (*cryptossh.Client, error) {
	privateKeyPath, err := expandUserPath(target.PrivateKeyPath)
	if err != nil {
		return nil, err
	}

	privateKey, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key %q: %w", privateKeyPath, err)
	}

	signer, err := cryptossh.ParsePrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key %q: %w", privateKeyPath, err)
	}

	hostKeyCallback, knownHostsPath, err := newHostKeyCallback()
	if err != nil {
		return nil, err
	}

	clientConfig := &cryptossh.ClientConfig{
		User:            target.User,
		Auth:            []cryptossh.AuthMethod{cryptossh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCallback,
	}

	if target.Timeout > 0 {
		clientConfig.Timeout = target.Timeout
	}

	address := withDefaultPort(target.Host)
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %q: %w", address, err)
	}

	cconn, chans, reqs, err := cryptossh.NewClientConn(conn, address, clientConfig)
	if err != nil {
		_ = conn.Close()
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) {
			if len(keyErr.Want) == 0 {
				return nil, fmt.Errorf("host key for %q is unknown (known_hosts=%s); verify and add it first (for example: ssh-keyscan -H %q >> ~/.ssh/known_hosts)", address, knownHostsPath, stripPort(address))
			}
			return nil, fmt.Errorf("host key mismatch for %q (known_hosts=%s); possible MITM or host key rotation", address, knownHostsPath)
		}
		return nil, fmt.Errorf("failed SSH handshake with %q: %w", address, err)
	}

	return cryptossh.NewClient(cconn, chans, reqs), nil
}

func newHostKeyCallback() (cryptossh.HostKeyCallback, string, error) {
	knownHostsFiles, err := existingKnownHostsFiles()
	if err != nil {
		return nil, "", err
	}
	if len(knownHostsFiles) == 0 {
		return nil, "", fmt.Errorf("no known_hosts file found; expected one of: ~/.ssh/known_hosts, /etc/ssh/ssh_known_hosts")
	}

	callback, err := knownhosts.New(knownHostsFiles...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read known_hosts (%s): %w", strings.Join(knownHostsFiles, ", "), err)
	}
	return callback, strings.Join(knownHostsFiles, ", "), nil
}

func existingKnownHostsFiles() ([]string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve home directory for known_hosts: %w", err)
	}

	candidates := []string{
		filepath.Join(homeDir, ".ssh", "known_hosts"),
		"/etc/ssh/ssh_known_hosts",
		"/etc/ssh/ssh_known_hosts2",
	}

	files := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			files = append(files, candidate)
		}
	}

	return files, nil
}

func stripPort(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	return host
}

func withDefaultPort(host string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, "22")
}

func expandUserPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "~" {
		return os.UserHomeDir()
	}
	if !strings.HasPrefix(path, "~/") {
		return path, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}

	return filepath.Join(homeDir, path[2:]), nil
}
