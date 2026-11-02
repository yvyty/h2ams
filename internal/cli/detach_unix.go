//go:build unix

package cli

import "syscall"

// detachAttr starts the daemon in its own session, free of the terminal.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
