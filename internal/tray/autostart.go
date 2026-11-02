package tray

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/yvyty/h2ams/internal/daemon"
)

// NoTrayEnv disables starting the tray from "h2ams up" when set to anything
// but "" or "0".
const NoTrayEnv = "H2AMS_NO_TRAY"

// ErrRunning means another h2ams-tray already holds the lock.
var ErrRunning = errors.New("h2ams-tray is already running")

// lockPath is shared by every project, so one tray serves all of them.
func lockPath() (string, error) {
	base, err := daemon.StateBase()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "tray.lock"), nil
}

// Lock makes this process the only h2ams-tray. It returns ErrRunning if
// another tray holds the lock. Call release on exit (the OS also drops the
// lock when the process dies).
func Lock() (release func(), err error) {
	path, err := lockPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return lockFile(path)
}

// Running reports whether an h2ams-tray currently holds the lock.
func Running() bool {
	release, err := Lock()
	if err != nil {
		return errors.Is(err, ErrRunning)
	}
	release()
	return false
}

// ShouldAutostart reports whether "h2ams up" should start the tray: not when
// disabled with H2AMS_NO_TRAY, not over SSH, and on Linux and the BSDs only
// with a graphical session.
func ShouldAutostart(goos string, getenv func(string) string) bool {
	if v := getenv(NoTrayEnv); v != "" && v != "0" {
		return false
	}
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return false
	}
	switch goos {
	case "darwin", "windows":
		return true
	default:
		return getenv("DISPLAY") != "" || getenv("WAYLAND_DISPLAY") != ""
	}
}

// Autostart starts h2ams-tray in the background if it is installed next to
// the running h2ams binary, allowed (ShouldAutostart), and not already
// running. It reports whether it started one. detach is the platform's
// SysProcAttr for a process that outlives the terminal.
func Autostart(detach *syscall.SysProcAttr) (bool, error) {
	if !ShouldAutostart(runtime.GOOS, os.Getenv) {
		return false, nil
	}
	bin, ok := trayBinary()
	if !ok || Running() {
		return false, nil
	}
	path, err := lockPath()
	if err != nil {
		return false, err
	}
	logFile, err := os.OpenFile(filepath.Join(filepath.Dir(path), "tray.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return false, err
	}
	defer logFile.Close()
	c := exec.Command(bin)
	c.Stdout = logFile
	c.Stderr = logFile
	c.SysProcAttr = detach
	if err := c.Start(); err != nil {
		return false, err
	}
	go c.Wait() //nolint:errcheck // reap it if it exits while we are still running
	return true, nil
}

// trayBinary finds h2ams-tray next to the running executable. Only the
// sibling counts: it is the one installed with this h2ams.
func trayBinary() (string, bool) {
	self, err := os.Executable()
	if err != nil {
		return "", false
	}
	return siblingOf(self, exeName("h2ams-tray", runtime.GOOS))
}

// exeName is the file name of the binary base on goos.
func exeName(base, goos string) string {
	if goos == "windows" {
		return base + ".exe"
	}
	return base
}

// siblingOf returns the regular file name in the same directory as self.
func siblingOf(self, name string) (string, bool) {
	bin := filepath.Join(filepath.Dir(self), name)
	if info, err := os.Stat(bin); err != nil || info.IsDir() {
		return "", false
	}
	return bin, true
}
