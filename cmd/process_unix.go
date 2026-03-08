//go:build !windows

package cmd

import (
	"os"
	"os/exec"
	"syscall"
)

func configureBuildCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateBuildProcess(process *os.Process) error {
	return syscall.Kill(-process.Pid, syscall.SIGTERM)
}

func killBuildProcess(process *os.Process) error {
	return syscall.Kill(-process.Pid, syscall.SIGKILL)
}

func shutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
