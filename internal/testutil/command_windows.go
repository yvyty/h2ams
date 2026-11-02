package testutil

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// Command returns a cmd.exe command line (run as "cmd /C <line>") that runs
// the test binary in mode, writing to outFile where the mode uses one.
// `set "K=V"&&` sets a variable without a trailing space in its value.
func Command(mode, outFile string) string {
	return fmt.Sprintf(`set "%s=%s"&& set "%s=%s"&& "%s" -test.run=%s`,
		modeEnv, mode, outEnv, outFile, os.Args[0], noTests)
}

// Alive reports whether pid is still running.
func Alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	const stillActive = 259
	return code == stillActive
}
