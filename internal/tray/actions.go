package tray

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yvyty/h2ams/internal/daemon"
)

// Runner carries out menu actions using the h2ams CLI and API.
type Runner struct {
	H2ams string // path to the h2ams binary
}

// FindH2ams locates the h2ams binary: next to this executable first (the
// usual install), then on PATH.
func FindH2ams() (string, error) {
	name := "h2ams"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if self, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(self), name)
		if info, err := os.Stat(sibling); err == nil && !info.IsDir() {
			return sibling, nil
		}
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", errors.New("h2ams binary not found next to h2ams-tray or on PATH")
	}
	return path, nil
}

// Run performs a. Refresh and Quit are handled by the GUI, not here.
func (r Runner) Run(a Action) error {
	switch a.Kind {
	case ActUp:
		return r.h2ams(upArgs(a)...)
	case ActDown:
		return r.down(a.ConfigPath)
	case ActSwitch:
		if err := r.down(a.ConfigPath); err != nil && !errors.Is(err, daemon.ErrNotRunning) {
			return err
		}
		return r.h2ams(upArgs(a)...)
	case ActOpen:
		name, args := openCommand(runtime.GOOS, a.URL)
		return exec.Command(name, args...).Start()
	default:
		return fmt.Errorf("action %q is handled by the tray itself", a.Kind)
	}
}

func upArgs(a Action) []string {
	args := []string{"up", "--config", a.ConfigPath}
	if a.Env != "" {
		args = append(args, "--env", a.Env)
	}
	return args
}

func (r Runner) down(configPath string) error {
	paths, err := daemon.PathsFor(configPath)
	if err != nil {
		return err
	}
	_, err = daemon.Call(paths.Socket, daemon.Request{Cmd: daemon.CmdDown}, time.Minute)
	return err
}

// h2ams runs the CLI and turns a failure into its own error message.
func (r Runner) h2ams(args ...string) error {
	out, err := exec.Command(r.H2ams, args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		msg = strings.TrimPrefix(msg, "Error: ")
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}

// openCommand opens url in the default browser on goos.
func openCommand(goos, url string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		return "xdg-open", []string{url}
	}
}
