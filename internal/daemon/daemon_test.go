//go:build unix

package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yvyty/h2ams/internal/config"
	"github.com/yvyty/h2ams/internal/process"
	"github.com/yvyty/h2ams/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.RunHelperIfRequested()
	os.Exit(m.Run())
}

const timeout = 5 * time.Second

// shortTempDir keeps socket paths under the ~104-byte sun_path limit on macOS,
// which t.TempDir() can exceed.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "h2")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func writeConfig(t *testing.T, dir string, apps map[string]string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("version: 1\napps:\n")
	for name, cmd := range apps {
		fmt.Fprintf(&b, "  %s:\n    command: %q\n", name, cmd)
	}
	path := filepath.Join(dir, config.DefaultFileName)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func startDaemon(t *testing.T, opts Options) <-chan error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() { errCh <- Run(context.Background(), opts) }()
	if !testutil.WaitFor(timeout, func() bool { return Running(opts.Paths.Socket) }) {
		t.Fatal("daemon never started listening")
	}
	return errCh
}

func TestUpStatusDown(t *testing.T) {
	dir := shortTempDir(t)
	cfgPath := writeConfig(t, dir, map[string]string{
		"sleeper": testutil.Command(testutil.Sleep, ""),
	})
	opts := Options{ConfigPath: cfgPath, Paths: NewPaths(filepath.Join(dir, "state"))}
	errCh := startDaemon(t, opts)

	var resp Response
	ok := testutil.WaitFor(timeout, func() bool {
		var err error
		resp, err = Call(opts.Paths.Socket, Request{Cmd: CmdStatus}, timeout)
		return err == nil && len(resp.Apps) == 1 && resp.Apps[0].State == process.StateRunning
	})
	if !ok {
		t.Fatalf("app never reported running: %+v", resp)
	}
	pid := resp.Apps[0].PID
	if resp.Apps[0].Name != "sleeper" || pid == 0 || resp.PID != os.Getpid() || resp.ConfigPath != cfgPath {
		t.Errorf("unexpected status response: %+v", resp)
	}

	if err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("second daemon: err = %v, want already running", err)
	}

	resp, err := Call(opts.Paths.Socket, Request{Cmd: CmdDown}, timeout)
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if resp.Apps[0].State != process.StateStopped {
		t.Errorf("after down state = %q", resp.Apps[0].State)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run returned %v", err)
		}
	case <-time.After(timeout):
		t.Fatal("daemon did not exit after down")
	}
	if Running(opts.Paths.Socket) {
		t.Error("socket still accepting after shutdown")
	}
	if testutil.Alive(pid) {
		t.Errorf("app pid %d survived daemon shutdown", pid)
	}
}

func TestContextCancelStopsApps(t *testing.T) {
	dir := shortTempDir(t)
	out := filepath.Join(dir, "pid")
	cfgPath := writeConfig(t, dir, map[string]string{"a": testutil.Command(testutil.WritePID, out)})
	opts := Options{ConfigPath: cfgPath, Paths: NewPaths(filepath.Join(dir, "state"))}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- Run(ctx, opts) }()

	raw, ok := testutil.ReadFile(out, timeout)
	if !ok {
		t.Fatal("app never started")
	}
	var pid int
	fmt.Sscan(raw, &pid)

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if testutil.Alive(pid) {
		t.Errorf("app pid %d survived daemon shutdown", pid)
	}
}

func TestStatusReportsPorts(t *testing.T) {
	dir := shortTempDir(t)
	out := filepath.Join(dir, "port")
	cfgPath := writeConfig(t, dir, map[string]string{"web": testutil.Command(testutil.Listen, out)})
	opts := Options{ConfigPath: cfgPath, Paths: NewPaths(filepath.Join(dir, "state"))}
	errCh := startDaemon(t, opts)
	defer func() {
		Call(opts.Paths.Socket, Request{Cmd: CmdDown}, timeout)
		<-errCh
	}()

	raw, ok := testutil.ReadFile(out, timeout)
	if !ok {
		t.Fatal("app never reported its port")
	}
	var want int
	fmt.Sscan(raw, &want)

	var resp Response
	if !testutil.WaitFor(timeout, func() bool {
		resp, _ = Call(opts.Paths.Socket, Request{Cmd: CmdStatus}, timeout)
		return len(resp.Apps) == 1 && slices.Contains(resp.Apps[0].Ports, want)
	}) {
		t.Fatalf("status never showed port %d: %+v", want, resp.Apps)
	}
}

func TestLogsSurviveRestart(t *testing.T) {
	dir := shortTempDir(t)
	cfgPath := filepath.Join(dir, config.DefaultFileName)
	os.WriteFile(cfgPath, []byte(`version: 1
apps:
  flaky:
    command: echo out-line; echo err-line >&2; exit 1
    restart:
      max_attempts: 1
      backoff: 10ms
`), 0o644)
	paths := NewPaths(filepath.Join(dir, "state"))
	errCh := startDaemon(t, Options{ConfigPath: cfgPath, Paths: paths})

	if !testutil.WaitFor(timeout, func() bool {
		resp, _ := Call(paths.Socket, Request{Cmd: CmdStatus}, timeout)
		return len(resp.Apps) == 1 && resp.Apps[0].State == process.StateFailed
	}) {
		t.Fatal("app never reached failed")
	}
	Call(paths.Socket, Request{Cmd: CmdDown}, timeout)
	<-errCh

	data, err := os.ReadFile(paths.AppLog("flaky"))
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	for _, want := range []string{"flaky: started", "flaky: restarting in", "flaky: gave up after 1 restarts"} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q:\n%s", want, log)
		}
	}
	if strings.Count(log, "out-line") != 2 || strings.Count(log, "err-line") != 2 {
		t.Errorf("expected stdout and stderr from both runs:\n%s", log)
	}
}

func TestStaleSocketIsReplaced(t *testing.T) {
	dir := shortTempDir(t)
	cfgPath := writeConfig(t, dir, map[string]string{"a": testutil.Command(testutil.Sleep, "")})
	paths := NewPaths(filepath.Join(dir, "state"))
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	errCh := startDaemon(t, Options{ConfigPath: cfgPath, Paths: paths})
	if _, err := Call(paths.Socket, Request{Cmd: CmdDown}, timeout); err != nil {
		t.Fatal(err)
	}
	<-errCh
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, config.DefaultFileName)
	os.WriteFile(path, []byte("version: 1\n"), 0o644)
	if err := Run(context.Background(), Options{ConfigPath: path, Paths: NewPaths(dir)}); err == nil {
		t.Fatal("expected config error")
	}
}

func TestSocketPathTooLong(t *testing.T) {
	dir := shortTempDir(t)
	cfgPath := writeConfig(t, dir, map[string]string{"a": "true"})
	paths := NewPaths(filepath.Join(dir, strings.Repeat("x", 120)))
	err := Run(context.Background(), Options{ConfigPath: cfgPath, Paths: paths})
	if err == nil || !strings.Contains(err.Error(), StateDirEnv) {
		t.Fatalf("err = %v, want a clear path-length error", err)
	}
}

func TestCallNotRunning(t *testing.T) {
	_, err := Call(filepath.Join(shortTempDir(t), "none.sock"), Request{Cmd: CmdStatus}, time.Second)
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

func TestUnknownCommand(t *testing.T) {
	dir := shortTempDir(t)
	cfgPath := writeConfig(t, dir, map[string]string{"a": testutil.Command(testutil.Sleep, "")})
	opts := Options{ConfigPath: cfgPath, Paths: NewPaths(filepath.Join(dir, "state"))}
	errCh := startDaemon(t, opts)
	defer func() {
		Call(opts.Paths.Socket, Request{Cmd: CmdDown}, timeout)
		<-errCh
	}()

	if _, err := Call(opts.Paths.Socket, Request{Cmd: "dance"}, timeout); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("err = %v, want unknown command", err)
	}
}

func TestSpecs(t *testing.T) {
	cfg, err := config.Parse([]byte(`
version: 1
apps:
  rel:
    command: run
    cwd: sub
    env: {FOO: bar}
  abs:
    command: run
    cwd: /tmp
  none:
    command: run
`))
	if err != nil {
		t.Fatal(err)
	}
	specs := Specs(cfg, "/base", []string{"rel", "abs", "none"}, nil)
	wantDirs := []string{"/base/sub", "/tmp", "/base"}
	for i, s := range specs {
		if s.Dir != wantDirs[i] {
			t.Errorf("%s dir = %q, want %q", s.Name, s.Dir, wantDirs[i])
		}
	}
	if !slices.Contains(specs[0].Env, "FOO=bar") {
		t.Error("app env not merged")
	}
	if specs[0].MaxAttempts != config.DefaultMaxAttempts || specs[0].Backoff != config.DefaultBackoff {
		t.Errorf("restart defaults not carried over: %+v", specs[0])
	}
}

func TestPathsForIsStablePerConfig(t *testing.T) {
	t.Setenv(StateDirEnv, "/state")
	a, _ := PathsFor("/proj/a/h2ams.yml")
	a2, _ := PathsFor("/proj/a/h2ams.yml")
	b, _ := PathsFor("/proj/b/h2ams.yml")
	if a != a2 || a == b || filepath.Dir(a.Dir) != "/state" {
		t.Errorf("paths a=%+v a2=%+v b=%+v", a, a2, b)
	}
}
