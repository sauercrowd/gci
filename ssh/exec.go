package ssh

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	cryptossh "golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

func RunCommandTerminal(ctx context.Context, target Target, command string, stdin, stdout, stderr *os.File) error {
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

	session.Stdin = stdin
	session.Stdout = stdout
	session.Stderr = stderr

	interactive := term.IsTerminal(int(stdin.Fd())) && term.IsTerminal(int(stdout.Fd()))
	if interactive {
		termType := os.Getenv("TERM")
		if termType == "" {
			termType = "xterm-256color"
		}

		width, height, err := term.GetSize(int(stdout.Fd()))
		if err != nil || width <= 0 || height <= 0 {
			width = 80
			height = 24
		}

		modes := cryptossh.TerminalModes{
			cryptossh.ECHO:          1,
			cryptossh.TTY_OP_ISPEED: 14400,
			cryptossh.TTY_OP_OSPEED: 14400,
		}
		if err := session.RequestPty(termType, height, width, modes); err != nil {
			return fmt.Errorf("failed to request remote PTY: %w", err)
		}

		oldState, err := term.MakeRaw(int(stdin.Fd()))
		if err != nil {
			return fmt.Errorf("failed to switch terminal to raw mode: %w", err)
		}
		defer func() {
			_ = term.Restore(int(stdin.Fd()), oldState)
		}()

		done := watchWindowChanges(session, stdout)
		defer close(done)
	}

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

func watchWindowChanges(session *cryptossh.Session, stdout *os.File) chan struct{} {
	sigCh := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(sigCh, syscall.SIGWINCH)

	go func() {
		defer signal.Stop(sigCh)
		for {
			select {
			case <-done:
				return
			case <-sigCh:
				width, height, err := term.GetSize(int(stdout.Fd()))
				if err != nil || width <= 0 || height <= 0 {
					continue
				}
				_ = session.WindowChange(height, width)
			}
		}
	}()

	sigCh <- syscall.SIGWINCH
	return done
}
