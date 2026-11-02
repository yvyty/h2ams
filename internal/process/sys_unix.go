//go:build unix

package process

import (
	"os/exec"
	"syscall"
)

func newShellCommand(command string) *exec.Cmd {
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.SysProcAttr = sysProcAttr()
	return cmd
}

// terminateGroup sends SIGTERM to the whole process group led by pid.
func terminateGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGTERM) }

// killGroup sends SIGKILL to the whole process group led by pid.
func killGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) }
