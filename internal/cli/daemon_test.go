//go:build unix

package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yvyty/h2ams/internal/daemon"
	"github.com/yvyty/h2ams/internal/process"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestStatusAndDownWhenNotRunning(t *testing.T) {
	t.Setenv(daemon.StateDirEnv, t.TempDir())
	cfg := filepath.Join(t.TempDir(), "h2ams.yml")
	for _, sub := range []string{"status", "down"} {
		out, err := run(t, sub, "--config", cfg)
		if err != nil || !strings.Contains(out, "not running") {
			t.Errorf("%s: out=%q err=%v", sub, out, err)
		}
	}
}

func TestUpRejectsInvalidConfig(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "h2ams.yml")
	os.WriteFile(cfg, []byte("version: 1\napps:\n  a:\n    cwd: .\n"), 0o644)
	if _, err := run(t, "up", "--config", cfg); err == nil || !strings.Contains(err.Error(), "command: required") {
		t.Fatalf("err = %v, want validation error", err)
	}
}

func TestPrintStatus(t *testing.T) {
	var b bytes.Buffer
	printStatus(&b, daemon.Response{Apps: []process.Status{
		{Name: "api", State: process.StateRunning, PID: 42, StartedAt: time.Now().Add(-3 * time.Second)},
		{Name: "web", State: process.StateFailed, Restarts: 5, Error: "gave up"},
	}})
	out := b.String()
	for _, want := range []string{"APP", "api", "running", "42", "3s", "web", "failed", "gave up"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestBinaryUpStatusDown builds the real binary and drives the detached daemon.
func TestBinaryUpStatusDown(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	work, err := os.MkdirTemp("", "h2cli") // short path for the socket
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(work) })

	bin := filepath.Join(work, "h2ams")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/yvyty/h2ams/cmd/h2ams").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cfg := filepath.Join(work, "h2ams.yml")
	os.WriteFile(cfg, []byte("version: 1\napps:\n  sleeper:\n    command: sleep 60\n"), 0o644)

	h2ams := func(args ...string) string {
		t.Helper()
		c := exec.Command(bin, append(args, "--config", cfg)...)
		c.Env = append(os.Environ(), daemon.StateDirEnv+"="+filepath.Join(work, "state"))
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("h2ams %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	t.Cleanup(func() { exec.Command(bin, "down", "--config", cfg).Run() })

	if out := h2ams("up"); !strings.Contains(out, "h2ams started") || !strings.Contains(out, "sleeper") {
		t.Fatalf("up output:\n%s", out)
	}

	deadline := time.Now().Add(5 * time.Second)
	var out string
	for time.Now().Before(deadline) {
		if out = h2ams("status"); strings.Contains(out, "running") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(out, "sleeper") || !strings.Contains(out, "running") {
		t.Fatalf("status never showed sleeper running:\n%s", out)
	}

	if out := h2ams("down"); !strings.Contains(out, "Stopped 1 app") {
		t.Fatalf("down output:\n%s", out)
	}
	if out := h2ams("status"); !strings.Contains(out, "not running") {
		t.Fatalf("status after down:\n%s", out)
	}
}
