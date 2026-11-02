package process

import "syscall"

// sysProcAttr puts the child in its own process group and has the kernel kill
// it if the supervisor dies without cleaning up.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
