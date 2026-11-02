//go:build unix

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yvyty/h2ams/internal/config"
	"github.com/yvyty/h2ams/internal/process"
	"github.com/yvyty/h2ams/internal/testutil"
)

// fakeDocker is a stand-in docker CLI: it logs every invocation, and "run"
// behaves like a foreground container that exits on SIGTERM.
const fakeDocker = `#!/bin/sh
echo "$*" >> "$FAKE_DOCKER_LOG"
state="$FAKE_DOCKER_STATE"
case "$1" in
run)
  name=""; prev=""
  for a in "$@"; do [ "$prev" = "--name" ] && name="$a"; prev="$a"; done
  echo running > "$state/$name"
  echo "fake container $name up"
  trap 'echo exited > "$state/$name"; echo "fake container got SIGTERM"; exit 0' TERM
  while :; do sleep 0.1; done ;;
rm)
  for a in "$@"; do rm -f "$state/$a"; done ;;
inspect)
  shift 3
  rc=0
  for n in "$@"; do
    if [ -f "$state/$n" ]; then echo "/$n $(cat "$state/$n")"; else echo "Error: No such object: $n" >&2; rc=1; fi
  done
  exit $rc ;;
esac
`

// installFakeDocker points h2ams at fakeDocker and returns its call log path.
func installFakeDocker(t *testing.T) (logPath string) {
	t.Helper()
	dir := shortTempDir(t)
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(fakeDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(dir, "state")
	os.Mkdir(stateDir, 0o755)
	logPath = filepath.Join(dir, "calls.log")
	t.Setenv(DockerEnv, bin)
	t.Setenv("FAKE_DOCKER_LOG", logPath)
	t.Setenv("FAKE_DOCKER_STATE", stateDir)
	return logPath
}

func readCalls(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}

func TestDockerRunCommand(t *testing.T) {
	t.Setenv(DockerEnv, "")
	app := config.App{
		Env: map[string]string{"B": "two words", "A": "1"},
		Docker: &config.Docker{
			Image:   "postgres:16",
			Ports:   []string{"5432:5432"},
			Volumes: []string{"./data:/var/lib/postgresql/data", "pgcache:/cache", "/abs:/abs:ro"},
			Args:    []string{"-c", "log_statement=all"},
		},
	}
	got := dockerRunCommand("abc123", "h2ams-abc123-db", "/proj", app)
	want := "exec docker run --rm --name h2ams-abc123-db --label h2ams.project=abc123 --label h2ams.app=db " +
		"-p 5432:5432 -e A=1 -e 'B=two words' " +
		"-v /proj/data:/var/lib/postgresql/data -v pgcache:/cache -v /abs:/abs:ro " +
		"postgres:16 -c log_statement=all"
	if got != want {
		t.Errorf("dockerRunCommand =\n%s\nwant\n%s", got, want)
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"plain":     "plain",
		"a b":       "'a b'",
		"it's":      `'it'\''s'`,
		"":          "''",
		"$(rm -rf)": "'$(rm -rf)'",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestDockerAppLifecycle(t *testing.T) {
	calls := installFakeDocker(t)
	dir := shortTempDir(t)
	cfgPath := filepath.Join(dir, config.DefaultFileName)
	os.WriteFile(cfgPath, []byte(fmt.Sprintf(`version: 1
dashboard:
  enabled: false
apps:
  db:
    docker:
      image: postgres:16
      ports: ["15432:5432"]
      volumes: ["./data:/data"]
    env:
      POSTGRES_PASSWORD: dev
  native:
    command: %q
`, testutil.Command(testutil.Sleep, ""))), 0o644)
	paths := NewPaths(filepath.Join(dir, "state"))
	errCh := startDaemon(t, Options{ConfigPath: cfgPath, Paths: paths})
	stopped := false
	defer func() {
		if !stopped {
			Call(paths.Socket, Request{Cmd: CmdDown}, timeout)
			<-errCh
		}
	}()
	sock := paths.Socket
	container := "h2ams-" + filepath.Base(paths.Dir) + "-db"

	// Status: container state (from docker inspect), published ports, no host CPU/memory.
	var db AppStatus
	if !testutil.WaitFor(2*timeout, func() bool {
		db = waitAppState(t, sock, "db", process.StateRunning)
		return db.Container != nil && db.Container.State == "running"
	}) {
		t.Fatalf("container state never reported: %+v", db)
	}
	if db.Container.Name != container || db.Container.Image != "postgres:16" || len(db.Ports) != 1 || db.Ports[0] != 15432 || db.CPUPercent != nil {
		t.Errorf("db status = %+v (container %+v)", db, db.Container)
	}
	if native := waitAppState(t, sock, "native", process.StateRunning); native.Container != nil {
		t.Errorf("native app reported a container: %+v", native.Container)
	}

	log := readCalls(calls)
	for _, want := range []string{
		"rm -f " + container, // leftover cleanup before the first start
		"run --rm --name " + container,
		"-p 15432:5432",
		"-e POSTGRES_PASSWORD=dev",
		"-v " + filepath.Join(dir, "data") + ":/data",
		"postgres:16",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("docker calls missing %q:\n%s", want, log)
		}
	}
	if logs, _ := os.ReadFile(paths.AppLog("db")); !strings.Contains(string(logs), "fake container "+container+" up") {
		t.Errorf("container output not in app log: %s", logs)
	}

	// Stop: SIGTERM reaches the container, then it is removed.
	before := strings.Count(readCalls(calls), "rm -f "+container)
	if _, err := Call(sock, Request{Cmd: CmdStop, App: "db"}, timeout); err != nil {
		t.Fatal(err)
	}
	waitAppState(t, sock, "db", process.StateStopped)
	if logs, _ := os.ReadFile(paths.AppLog("db")); !strings.Contains(string(logs), "fake container got SIGTERM") {
		t.Errorf("container did not get SIGTERM: %s", logs)
	}
	if after := strings.Count(readCalls(calls), "rm -f "+container); after <= before {
		t.Errorf("container not removed after stop")
	}
	if !testutil.WaitFor(2*timeout, func() bool {
		resp, _ := Call(sock, Request{Cmd: CmdStatus}, timeout)
		a, _ := appByName(resp, "db")
		return a.Container != nil && a.Container.State == "" && len(a.Ports) == 0
	}) {
		t.Error("stopped container still reported")
	}

	// Start again, then down removes it.
	if _, err := Call(sock, Request{Cmd: CmdStart, App: "db"}, timeout); err != nil {
		t.Fatal(err)
	}
	waitAppState(t, sock, "db", process.StateRunning)
	runs := strings.Count(readCalls(calls), "run --rm --name "+container)
	if runs != 2 {
		t.Errorf("docker run called %d times, want 2", runs)
	}
	Call(sock, Request{Cmd: CmdDown}, timeout)
	<-errCh
	stopped = true
	if !strings.HasSuffix(strings.TrimSpace(readCalls(calls)), "rm -f "+container) {
		t.Errorf("down did not end with removing the container:\n%s", readCalls(calls))
	}
}

func TestDockerCrashIsRestarted(t *testing.T) {
	installFakeDocker(t)
	// "docker" that fails immediately, like a container that crashes.
	bin := filepath.Join(shortTempDir(t), "docker")
	os.WriteFile(bin, []byte("#!/bin/sh\ncase \"$1\" in run) echo boom; exit 3;; esac\n"), 0o755)
	t.Setenv(DockerEnv, bin)

	dir := shortTempDir(t)
	cfgPath := filepath.Join(dir, config.DefaultFileName)
	os.WriteFile(cfgPath, []byte("version: 1\ndashboard:\n  enabled: false\napps:\n  db:\n    docker:\n      image: x\n    restart:\n      max_attempts: 2\n      backoff: 10ms\n"), 0o644)
	paths := NewPaths(filepath.Join(dir, "state"))
	errCh := startDaemon(t, Options{ConfigPath: cfgPath, Paths: paths})
	defer func() {
		Call(paths.Socket, Request{Cmd: CmdDown}, timeout)
		<-errCh
	}()
	db := waitAppState(t, paths.Socket, "db", process.StateFailed)
	if db.Restarts != 2 || db.ExitCode != 3 {
		t.Errorf("crashing container: %+v", db)
	}
}

// TestRealDocker runs a real container. It is opt-in because it needs a
// running Docker daemon and pulls an image: H2AMS_TEST_DOCKER=1 go test ./...
func TestRealDocker(t *testing.T) {
	if os.Getenv("H2AMS_TEST_DOCKER") != "1" {
		t.Skip("set H2AMS_TEST_DOCKER=1 to run against a real Docker daemon")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker is not available: %v", err)
	}
	t.Setenv(DockerEnv, "")
	dir := shortTempDir(t)
	cfgPath := filepath.Join(dir, config.DefaultFileName)
	os.WriteFile(cfgPath, []byte(`version: 1
dashboard:
  enabled: false
apps:
  box:
    docker:
      image: busybox:1.36
      args: ["sh", "-c", "echo hello-from-container; exec sleep 300"]
`), 0o644)
	paths := NewPaths(filepath.Join(dir, "state"))
	errCh := startDaemon(t, Options{ConfigPath: cfgPath, Paths: paths})
	defer func() {
		Call(paths.Socket, Request{Cmd: CmdDown}, timeout)
		<-errCh
	}()
	var box AppStatus
	if !testutil.WaitFor(120e9, func() bool { // first run may pull the image
		resp, _ := Call(paths.Socket, Request{Cmd: CmdStatus}, timeout)
		box, _ = appByName(resp, "box")
		return box.Container != nil && box.Container.State == "running"
	}) {
		t.Fatalf("container never running: %+v", box)
	}
	if _, err := Call(paths.Socket, Request{Cmd: CmdStop, App: "box"}, 30e9); err != nil {
		t.Fatal(err)
	}
	if out, _ := exec.Command("docker", "ps", "-aq", "--filter", "name="+box.Container.Name).Output(); strings.TrimSpace(string(out)) != "" {
		t.Errorf("container still exists after stop: %s", out)
	}
}

// TestDockerPortsWaitForContainer: while the image is still pulling (the
// docker CLI runs but no container exists yet), no ports are reported.
func TestDockerPortsWaitForContainer(t *testing.T) {
	dir := shortTempDir(t)
	bin := filepath.Join(dir, "docker")
	// "run" never creates a container; "inspect" finds nothing.
	os.WriteFile(bin, []byte("#!/bin/sh\ncase \"$1\" in run) echo pulling; exec sleep 60;; inspect) exit 1;; esac\n"), 0o755)
	t.Setenv(DockerEnv, bin)

	cfgPath := filepath.Join(dir, config.DefaultFileName)
	os.WriteFile(cfgPath, []byte("version: 1\ndashboard:\n  enabled: false\napps:\n  db:\n    docker:\n      image: x\n      ports: [\"15433:5432\"]\n"), 0o644)
	paths := NewPaths(filepath.Join(dir, "state"))
	errCh := startDaemon(t, Options{ConfigPath: cfgPath, Paths: paths})
	defer func() {
		Call(paths.Socket, Request{Cmd: CmdDown}, timeout)
		<-errCh
	}()
	db := waitAppState(t, paths.Socket, "db", process.StateRunning)
	if len(db.Ports) != 0 || db.Container == nil || db.Container.State != "" {
		t.Errorf("ports reported before the container exists: %+v (container %+v)", db, db.Container)
	}
}
