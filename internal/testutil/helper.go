// Package testutil provides a fake child process for supervisor tests: the
// test binary re-executes itself in a helper mode instead of running tests.
package testutil

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// noTests is a -test.run pattern that matches no test, in case a helper is
// ever started without its mode.
const noTests = "H2amsHelperMatchesNoTest"

const (
	modeEnv = "H2AMS_TEST_HELPER"
	outEnv  = "H2AMS_TEST_OUT"
)

// Helper modes.
const (
	Sleep      = "sleep"       // run until signaled
	Crash      = "crash"       // exit 1 immediately
	Exit0      = "exit0"       // exit 0 immediately
	CrashOnce  = "crash-once"  // exit 1 the first time (no out file), then sleep
	WriteCwd   = "write-cwd"   // write working directory to out file, then sleep
	WritePID   = "write-pid"   // write own PID to out file, then sleep
	IgnoreTerm = "ignore-term" // ignore SIGTERM, then sleep
	Listen     = "listen"      // listen on a free TCP port, write it to out file, then sleep
	Print      = "print"       // print $H2AMS_TEST_OUT to stdout and stderr, then sleep
	Fail       = "fail"        // print "out-line" to stdout and "err-line" to stderr, then exit 1
	Lines      = "lines"       // print line-1 .. line-5, then sleep
	Hello      = "hello"       // print hello-<pid>, then sleep
)

// RunHelperIfRequested must be called first in TestMain. In helper mode it
// performs the mode's behavior and exits; otherwise it returns.
func RunHelperIfRequested() {
	mode := os.Getenv(modeEnv)
	if mode == "" {
		return
	}
	out := os.Getenv(outEnv)
	switch mode {
	case Crash:
		os.Exit(1)
	case Exit0:
		os.Exit(0)
	case CrashOnce:
		if _, err := os.Stat(out); err != nil {
			_ = os.WriteFile(out, []byte("crashed"), 0o644)
			os.Exit(1)
		}
	case WriteCwd:
		wd, _ := os.Getwd()
		writeAtomic(out, wd)
	case WritePID:
		writeAtomic(out, strconv.Itoa(os.Getpid()))
	case IgnoreTerm:
		signal.Ignore(syscall.SIGTERM)
		writeAtomic(out, "ready")
	case Listen:
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		defer ln.Close()
		writeAtomic(out, strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
	case Print:
		fmt.Fprintln(os.Stdout, out)
		fmt.Fprintln(os.Stderr, out+" (stderr)")
	case Fail:
		fmt.Fprintln(os.Stdout, "out-line")
		fmt.Fprintln(os.Stderr, "err-line")
		os.Exit(1)
	case Lines:
		for i := 1; i <= 5; i++ {
			fmt.Fprintf(os.Stdout, "line-%d\n", i)
		}
	case Hello:
		fmt.Fprintf(os.Stdout, "hello-%d\n", os.Getpid())
	case Sleep:
	default:
		fmt.Fprintf(os.Stderr, "unknown helper mode %q\n", mode)
		os.Exit(2)
	}
	time.Sleep(time.Hour)
	os.Exit(0)
}

func writeAtomic(path, content string) {
	tmp := path + ".tmp"
	_ = os.WriteFile(tmp, []byte(content), 0o644)
	_ = os.Rename(tmp, path)
}

// WaitFor polls cond every 10ms until it returns true or timeout elapses.
func WaitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// ReadFile polls until path exists and returns its trimmed content.
func ReadFile(path string, timeout time.Duration) (string, bool) {
	var data []byte
	ok := WaitFor(timeout, func() bool {
		var err error
		data, err = os.ReadFile(path)
		return err == nil
	})
	return strings.TrimSpace(string(data)), ok
}
