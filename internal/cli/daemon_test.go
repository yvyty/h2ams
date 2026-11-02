package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yvyty/h2ams/internal/config"
	"github.com/yvyty/h2ams/internal/daemon"
	"github.com/yvyty/h2ams/internal/process"
	"github.com/yvyty/h2ams/internal/testutil"
)

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

func TestActionsWhenNotRunning(t *testing.T) {
	t.Setenv(daemon.StateDirEnv, t.TempDir())
	cfg := filepath.Join(t.TempDir(), "h2ams.yml")
	for _, action := range []string{"start", "stop", "restart"} {
		if _, err := run(t, action, "api", "--config", cfg); err == nil || !strings.Contains(err.Error(), `run "h2ams up" first`) {
			t.Errorf("%s: err = %v", action, err)
		}
	}
}

func TestUpRejectsUnknownEnvironment(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "h2ams.yml")
	os.WriteFile(cfg, []byte("version: 1\nenvironments:\n  dev:\n    apps: [a]\napps:\n  a:\n    command: x\n"), 0o644)
	if _, err := run(t, "up", "-e", "testing", "--config", cfg); err == nil || !strings.Contains(err.Error(), `unknown environment "testing"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestEnvsCommand(t *testing.T) {
	t.Setenv(daemon.StateDirEnv, t.TempDir())
	cfg := filepath.Join(t.TempDir(), "h2ams.yml")
	os.WriteFile(cfg, []byte(`version: 1
default_environment: dev
environments:
  dev:
    apps: [api, web]
  testing:
    apps: [api, db]
apps:
  api: {command: x}
  web: {command: x}
  db: {command: x}
`), 0o644)
	out, err := run(t, "envs", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ENVIRONMENT", "dev *", "api, web", "default", "testing", "api, db"} {
		if !strings.Contains(out, want) {
			t.Errorf("envs output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "active") {
		t.Errorf("no daemon is running, but an environment is marked active:\n%s", out)
	}
}

func TestPrintEnvsMarksActive(t *testing.T) {
	cfg, err := config.Parse([]byte("version: 1\nenvironments:\n  dev:\n    apps: [a]\n  testing:\n    apps: [a]\napps:\n  a: {command: x}\n"))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	printEnvs(&b, cfg, "testing", true)
	lines := strings.Split(b.String(), "\n")
	var testingLine, allLine string
	for _, l := range lines {
		if strings.HasPrefix(l, "testing") {
			testingLine = l
		}
		if strings.HasPrefix(l, "(all apps) *") {
			allLine = l
		}
	}
	if !strings.Contains(testingLine, "active") {
		t.Errorf("testing not marked active:\n%s", b.String())
	}
	if allLine == "" || !strings.Contains(allLine, "default") || strings.Contains(allLine, "active") {
		t.Errorf("without default_environment, all apps should be the default:\n%s", b.String())
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
	printStatus(&b, daemon.Response{Environment: "dev", Dashboard: "http://127.0.0.1:9999", Apps: []daemon.AppStatus{
		{Status: process.Status{Name: "api", State: process.StateRunning, PID: 42, StartedAt: time.Now().Add(-3 * time.Second)}, Ports: []int{8080, 8443}, CPUPercent: ptr(12.34), MemoryBytes: 52 << 20},
		{Status: process.Status{Name: "web", State: process.StateFailed, Restarts: 5, Error: "gave up"}},
		{Status: process.Status{Name: "db", State: process.StateRunning, PID: 7}, Ports: []int{5432}, Container: &daemon.ContainerInfo{Name: "h2ams-x-db", Image: "postgres:16", State: "running"}},
	}})
	out := b.String()
	for _, want := range []string{"Environment: dev", "Dashboard:   http://127.0.0.1:9999", "APP", "PORTS", "CPU", "MEM", "12.3%", "52.0 MiB", "api", "running", "42", "8080,8443", "3s", "web", "failed", "gave up", "container running (postgres:16)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestFormatBytes(t *testing.T) {
	for n, want := range map[uint64]string{0: "0 B", 512: "512 B", 2048: "2.0 KiB", 52 << 20: "52.0 MiB", 3 << 30: "3.0 GiB"} {
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", n, got, want)
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

	bin := filepath.Join(work, "h2ams"+exeSuffix)
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/yvyty/h2ams/cmd/h2ams").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cfg := filepath.Join(work, "h2ams.yml")
	ln, err := net.Listen("tcp", "127.0.0.1:0") // find a free dashboard port
	if err != nil {
		t.Fatal(err)
	}
	dashPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	os.WriteFile(cfg, []byte(fmt.Sprintf("version: 1\ndashboard:\n  port: %d\napps:\n  sleeper:\n    command: %q\n", dashPort, testutil.Command(testutil.Print, "hello-from-app"))), 0o644)

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

	dashURL := fmt.Sprintf("http://127.0.0.1:%d", dashPort)
	if out := h2ams("up"); !strings.Contains(out, "h2ams started") || !strings.Contains(out, "sleeper") || !strings.Contains(out, "Dashboard:   "+dashURL) {
		t.Fatalf("up output:\n%s", out)
	}
	res, err := http.Get(dashURL + "/api/status")
	if err != nil {
		t.Fatalf("dashboard unreachable: %v", err)
	}
	res.Body.Close()

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

	if out := h2ams("logs", "sleeper"); !strings.Contains(out, "hello-from-app") || !strings.Contains(out, "sleeper: started") {
		t.Fatalf("logs output:\n%s", out)
	}

	var st daemon.Response
	if err := json.Unmarshal([]byte(h2ams("status", "--json")), &st); err != nil || st.APIVersion != "v1" || st.Socket == "" || len(st.Apps) != 1 {
		t.Fatalf("status --json: %+v %v", st, err)
	}

	if out := h2ams("restart", "sleeper"); !strings.Contains(out, "sleeper") {
		t.Fatalf("restart output:\n%s", out)
	}
	if out := h2ams("stop", "sleeper"); !strings.Contains(out, "stopped") {
		t.Fatalf("stop output:\n%s", out)
	}
	if out := h2ams("start", "sleeper"); !strings.Contains(out, "sleeper") {
		t.Fatalf("start output:\n%s", out)
	}
	if out := h2ams("logs", "sleeper", "-n", "0"); strings.Count(out, "sleeper: started") != 3 {
		t.Fatalf("expected 3 starts after restart/stop/start:\n%s", out)
	}

	if out := h2ams("down"); !strings.Contains(out, "Stopped 1 app") {
		t.Fatalf("down output:\n%s", out)
	}
	if out := h2ams("status"); !strings.Contains(out, "not running") {
		t.Fatalf("status after down:\n%s", out)
	}
	if out := h2ams("logs", "sleeper"); !strings.Contains(out, "hello-from-app") || !strings.Contains(out, "sleeper: stopped") {
		t.Fatalf("logs after down:\n%s", out)
	}
}

func TestLogsCommand(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(daemon.StateDirEnv, stateDir)
	cfg := filepath.Join(t.TempDir(), "h2ams.yml")
	os.WriteFile(cfg, []byte("version: 1\napps:\n  api:\n    command: x\n"), 0o644)

	if _, err := run(t, "logs", "ghost", "--config", cfg); err == nil || !strings.Contains(err.Error(), `unknown app "ghost"`) {
		t.Errorf("unknown app: err = %v", err)
	}
	if out, err := run(t, "logs", "api", "--config", cfg); err != nil || !strings.Contains(out, "no logs yet") {
		t.Errorf("missing log: out=%q err=%v", out, err)
	}

	paths, _ := daemon.PathsFor(cfg)
	os.MkdirAll(paths.LogDir, 0o700)
	os.WriteFile(paths.AppLog("api"), []byte("l1\nl2\nl3\n"), 0o600)
	if out, err := run(t, "logs", "api", "-n", "2", "--config", cfg); err != nil || out != "l2\nl3\n" {
		t.Errorf("logs -n 2: out=%q err=%v", out, err)
	}
}
