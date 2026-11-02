//go:build unix

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yvyty/h2ams/internal/daemon"
	"github.com/yvyty/h2ams/internal/testutil"
	"github.com/yvyty/h2ams/internal/tray"
)

// TestUpStartsTray checks that "h2ams up" launches the h2ams-tray installed
// next to it, but not when one is already running or with --no-tray.
func TestUpStartsTray(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	work, err := os.MkdirTemp("", "h2tr") // short path for the socket
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(work) })
	state := filepath.Join(work, "state")
	t.Setenv(daemon.StateDirEnv, state) // for tray.Lock in this process

	bin := filepath.Join(work, "h2ams")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/yvyty/h2ams/cmd/h2ams").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	marker := filepath.Join(work, "tray-launches")
	fake := fmt.Sprintf("#!/bin/sh\necho launched >> %q\n", marker)
	if err := os.WriteFile(filepath.Join(work, "h2ams-tray"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	launches := func() int {
		data, _ := os.ReadFile(marker)
		return strings.Count(string(data), "launched")
	}

	up := func(name string, extra ...string) string {
		t.Helper()
		dir := filepath.Join(work, name)
		os.MkdirAll(dir, 0o755)
		cfg := filepath.Join(dir, "h2ams.yml")
		os.WriteFile(cfg, []byte("version: 1\ndashboard: {enabled: false}\napps:\n  a: {command: sleep 60}\n"), 0o644)
		t.Cleanup(func() { exec.Command(bin, "down", "--config", cfg).Run() })
		c := exec.Command(bin, append([]string{"up", "--config", cfg}, extra...)...)
		c.Env = append(os.Environ(), daemon.StateDirEnv+"="+state, tray.NoTrayEnv+"=", "SSH_CONNECTION=", "SSH_TTY=", "DISPLAY=:0")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("up %s: %v\n%s", name, err, out)
		}
		return string(out)
	}

	out := up("one")
	if !strings.Contains(out, "h2ams-tray started") {
		t.Errorf("up did not report starting the tray:\n%s", out)
	}
	if !testutil.WaitFor(5*time.Second, func() bool { return launches() == 1 }) {
		t.Fatalf("tray launched %d times, want 1", launches())
	}

	release, err := tray.Lock() // a tray is now running
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if out := up("two"); strings.Contains(out, "h2ams-tray started") {
		t.Errorf("up started a second tray:\n%s", out)
	}
	release()

	if out := up("three", "--no-tray"); strings.Contains(out, "h2ams-tray started") {
		t.Errorf("--no-tray still started the tray:\n%s", out)
	}
	time.Sleep(200 * time.Millisecond)
	if n := launches(); n != 1 {
		t.Errorf("tray launched %d times in total, want 1", n)
	}
}
