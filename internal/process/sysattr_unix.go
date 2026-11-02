//go:build unix && !linux

package process

import "syscall"

// sysProcAttr puts the child in its own process group. These platforms have no
// parent-death signal, so cleanup relies on the supervisor stopping its apps.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
