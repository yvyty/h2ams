package process

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func newShellCommand(command string) *exec.Cmd {
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	cmd := exec.Command(shell)
	// /S with outer quotes: cmd strips exactly those quotes and runs the rest
	// as written, even when the command itself starts with a quoted path.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       `/S /C "` + command + `"`,
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	return cmd
}

// terminateGroup asks the process tree rooted at pid to close.
func terminateGroup(pid int) {
	_ = exec.Command("taskkill", "/T", "/PID", strconv.Itoa(pid)).Run()
}

// groupAlive is always false on Windows: taskkill /T already waits for the
// tree, and a cmd.exe leader does not exit before its child.
func groupAlive(int) bool { return false }

// killGroup forcefully kills the process tree rooted at pid.
func killGroup(pid int) {
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}
