//go:build windows

package cmd

import (
	"os"
	"os/exec"
)

func configureBuildCommand(cmd *exec.Cmd) {}

func terminateBuildProcess(process *os.Process) error {
	return process.Kill()
}

func killBuildProcess(process *os.Process) error {
	return process.Kill()
}

func shutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
