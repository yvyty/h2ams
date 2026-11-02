//go:build unix

package testutil

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// Command returns a /bin/sh command line that runs the test binary in mode,
// writing to outFile where the mode uses one.
func Command(mode, outFile string) string {
	return fmt.Sprintf("%s=%s %s=%s %s -test.run=%s",
		modeEnv, mode, outEnv, quote(outFile), quote(os.Args[0]), noTests)
}

func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Alive reports whether pid can still be signaled.
func Alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
