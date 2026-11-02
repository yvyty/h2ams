//go:build unix

// These tests depend on /bin/sh job syntax and POSIX signals (SIGTERM
// handlers, process groups). Windows stops apps with taskkill instead.

package process

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yvyty/h2ams/internal/config"
	"github.com/yvyty/h2ams/internal/testutil"
)

func TestStopKillsChildProcesses(t *testing.T) {
	out := filepath.Join(t.TempDir(), "pid")
	s := spec(testutil.WritePID, out)
	s.Command += " & wait" // the helper becomes a grandchild under the shell

	p := Start(s)
	waitState(t, p, StateRunning)
	raw, ok := testutil.ReadFile(out, timeout)
	if !ok {
		t.Fatal("grandchild never wrote its pid")
	}
	child, _ := strconv.Atoi(raw)

	p.Stop()

	if !testutil.WaitFor(timeout, func() bool { return !testutil.Alive(child) }) {
		t.Errorf("grandchild %d survived Stop", child)
	}
}

func TestStopEscalatesToKill(t *testing.T) {
	out := filepath.Join(t.TempDir(), "ready")
	s := spec(testutil.IgnoreTerm, out)
	s.StopTimeout = 100 * time.Millisecond

	p := Start(s)
	pid := waitState(t, p, StateRunning).PID
	if _, ok := testutil.ReadFile(out, timeout); !ok {
		t.Fatal("helper never became ready")
	}

	start := time.Now()
	p.Stop()

	if testutil.Alive(pid) {
		t.Errorf("pid %d ignored SIGTERM and was not killed", pid)
	}
	if elapsed := time.Since(start); elapsed < s.StopTimeout {
		t.Errorf("Stop returned after %v, before the %v grace period", elapsed, s.StopTimeout)
	}
}

// TestStopGivesChildrenTheGracePeriod covers a leader that exits on SIGTERM
// right away (as /bin/sh does on Linux) while its child is still shutting
// down: the child must get the grace period, not an immediate SIGKILL.
func TestStopGivesChildrenTheGracePeriod(t *testing.T) {
	dir := t.TempDir()
	done := filepath.Join(dir, "done")
	// The child traps TERM and needs 300ms to finish; the wrapper shell dies on TERM.
	child := fmt.Sprintf(`trap 'sleep 0.3; echo clean > %q; exit 0' TERM; while :; do sleep 0.05; done`, done)
	s := Spec{Name: "wrapped", Command: fmt.Sprintf("sh -c %q; echo wrapper-done", child), Policy: config.RestartNever, StopTimeout: 3 * time.Second}
	p := Start(s)
	waitState(t, p, StateRunning)
	time.Sleep(200 * time.Millisecond) // let the child install its trap

	p.Stop()
	if data, _ := os.ReadFile(done); strings.TrimSpace(string(data)) != "clean" {
		t.Errorf("child was killed before finishing its shutdown (marker %q)", data)
	}
}
