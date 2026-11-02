package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yvyty/h2ams/internal/config"
	"github.com/yvyty/h2ams/internal/process"
	"github.com/yvyty/h2ams/internal/testutil"
)

const envConfig = `version: 1
default_environment: dev
dashboard:
  enabled: false
environments:
  dev:
    apps: [api, web]
  testing:
    apps: [api, testdb]
apps:
  api:
    command: %[1]q
  web:
    command: %[1]q
  testdb:
    command: %[1]q
`

func runEnv(t *testing.T, yaml, env string) (Response, error) {
	t.Helper()
	dir := shortTempDir(t)
	cfgPath := filepath.Join(dir, config.DefaultFileName)
	os.WriteFile(cfgPath, []byte(yaml), 0o644)
	opts := Options{ConfigPath: cfgPath, Environment: env, Paths: NewPaths(filepath.Join(dir, "state"))}

	errCh := make(chan error, 1)
	go func() { errCh <- Run(context.Background(), opts) }()
	if !testutil.WaitFor(timeout, func() bool { return Running(opts.Paths.Socket) }) {
		select {
		case err := <-errCh:
			return Response{}, err
		default:
			t.Fatal("daemon never started")
		}
	}
	defer func() {
		Call(opts.Paths.Socket, Request{Cmd: CmdDown}, timeout)
		<-errCh
	}()
	return Call(opts.Paths.Socket, Request{Cmd: CmdStatus}, timeout)
}

func appNames(resp Response) []string {
	var names []string
	for _, a := range resp.Apps {
		names = append(names, a.Name)
	}
	slices.Sort(names)
	return names
}

func TestEnvironmentSelectsApps(t *testing.T) {
	yaml := fmt.Sprintf(envConfig, testutil.Command(testutil.Sleep, ""))
	tests := []struct {
		env       string
		wantEnv   string
		wantApps  []string
		wantError string
	}{
		{"testing", "testing", []string{"api", "testdb"}, ""},
		{"dev", "dev", []string{"api", "web"}, ""},
		{"", "dev", []string{"api", "web"}, ""}, // default environment
		{"prod", "", nil, `unknown environment "prod"`},
	}
	for _, tt := range tests {
		t.Run("env="+tt.env, func(t *testing.T) {
			resp, err := runEnv(t, yaml, tt.env)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("err = %v, want %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if resp.Environment != tt.wantEnv {
				t.Errorf("environment = %q, want %q", resp.Environment, tt.wantEnv)
			}
			if got := appNames(resp); !slices.Equal(got, tt.wantApps) {
				t.Errorf("apps = %v, want %v", got, tt.wantApps)
			}
		})
	}
}

func TestNoDefaultEnvironmentStartsAllApps(t *testing.T) {
	yaml := strings.Replace(fmt.Sprintf(envConfig, testutil.Command(testutil.Sleep, "")), "default_environment: dev\n", "", 1)
	resp, err := runEnv(t, yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Environment != "" || !slices.Equal(appNames(resp), []string{"api", "testdb", "web"}) {
		t.Errorf("env %q apps %v, want all apps", resp.Environment, appNames(resp))
	}
}

func TestStatusReportsResources(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("CPU and memory are not measured on Windows yet")
	}
	dir := shortTempDir(t)
	cfgPath := writeConfig(t, dir, map[string]string{"api": testutil.Command(testutil.Sleep, "")})
	opts := Options{ConfigPath: cfgPath, Paths: NewPaths(filepath.Join(dir, "state"))}
	errCh := startDaemon(t, opts)
	defer func() {
		Call(opts.Paths.Socket, Request{Cmd: CmdDown}, timeout)
		<-errCh
	}()

	var app AppStatus
	if !testutil.WaitFor(timeout, func() bool {
		resp, err := Call(opts.Paths.Socket, Request{Cmd: CmdStatus}, timeout)
		if err != nil || len(resp.Apps) != 1 {
			return false
		}
		app = resp.Apps[0]
		return app.CPUPercent != nil && app.MemoryBytes > 0
	}) {
		t.Fatalf("status never reported CPU and memory: %+v", app)
	}
	if *app.CPUPercent < 0 || app.MemoryBytes < 1<<20 {
		t.Errorf("implausible usage: cpu=%v mem=%d", *app.CPUPercent, app.MemoryBytes)
	}
}

type fakeNotifier struct {
	mu   sync.Mutex
	msgs []string
}

func (f *fakeNotifier) Notify(title, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, title+": "+msg)
	return nil
}

func (f *fakeNotifier) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.msgs)
}

func crashingDaemon(t *testing.T, notifications bool, n *fakeNotifier) Paths {
	t.Helper()
	dir := shortTempDir(t)
	cfgPath := filepath.Join(dir, config.DefaultFileName)
	os.WriteFile(cfgPath, []byte(fmt.Sprintf(`version: 1
dashboard:
  enabled: false
notifications:
  crash_loop: %v
apps:
  flaky:
    command: exit 1
    restart:
      max_attempts: 4
      backoff: 10ms
`, notifications)), 0o644)
	opts := Options{ConfigPath: cfgPath, Paths: NewPaths(filepath.Join(dir, "state")), Notifier: n}
	errCh := startDaemon(t, opts)
	t.Cleanup(func() {
		Call(opts.Paths.Socket, Request{Cmd: CmdDown}, timeout)
		<-errCh
	})
	waitAppState(t, opts.Paths.Socket, "flaky", process.StateFailed)
	return opts.Paths
}

func TestCrashLoopNotifies(t *testing.T) {
	n := &fakeNotifier{}
	crashingDaemon(t, true, n)

	if !testutil.WaitFor(timeout, func() bool { return len(n.all()) >= 2 }) {
		t.Fatalf("notifications = %q, want crash loop and failed", n.all())
	}
	msgs := strings.Join(n.all(), "\n")
	if !strings.Contains(msgs, "h2ams: flaky is crash-looping") || !strings.Contains(msgs, "h2ams: flaky failed: gave up after 4 restarts") {
		t.Errorf("notifications:\n%s", msgs)
	}
}

func TestCrashLoopNotificationsAreOptIn(t *testing.T) {
	n := &fakeNotifier{}
	crashingDaemon(t, false, n)
	// The monitor ticks every second; give it two ticks to (not) fire.
	if testutil.WaitFor(2*monitorTick+500e6, func() bool { return len(n.all()) > 0 }) {
		t.Errorf("notified although notifications are disabled: %q", n.all())
	}
}
