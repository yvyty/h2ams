// Package notify shows desktop notifications.
package notify

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Notifier delivers a short message to the user.
type Notifier interface {
	Notify(title, message string) error
}

// Desktop returns a Notifier for the current OS: osascript on macOS and
// notify-send (libnotify) on Linux.
func Desktop() Notifier { return desktop{goos: runtime.GOOS} }

type desktop struct{ goos string }

func (d desktop) Notify(title, message string) error {
	name, args, err := command(d.goos, title, message)
	if err != nil {
		return err
	}
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("desktop notifications need %s: %w", name, err)
	}
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// command returns the program and arguments that show a notification on goos.
func command(goos, title, message string) (string, []string, error) {
	switch goos {
	case "darwin":
		script := fmt.Sprintf("display notification %s with title %s", appleScriptString(message), appleScriptString(title))
		return "osascript", []string{"-e", script}, nil
	case "linux", "freebsd", "openbsd", "netbsd":
		return "notify-send", []string{"--app-name=h2ams", "--", title, message}, nil
	default:
		return "", nil, fmt.Errorf("desktop notifications on %s: %w", goos, errors.ErrUnsupported)
	}
}

// appleScriptString quotes s as an AppleScript string literal, so app names
// and error text can never break out of the script.
func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
