package cli

import "syscall"

const detachedProcess = 0x00000008 // DETACHED_PROCESS

// detachAttr starts the daemon without a console, in its own process group.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}
