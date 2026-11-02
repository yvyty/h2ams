//go:build unix

package process

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yvyty/h2ams/internal/config"
	"github.com/yvyty/h2ams/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.RunHelperIfRequested()
	os.Exit(m.Run())
}

const timeout = 5 * time.Second

func spec(mode, out string) Spec {
	return Spec{
		Name:        "app",
		Command:     testutil.Command(mode, out),
		Policy:      config.RestartOnFailure,
		MaxAttempts: 3,
		Backoff:     10 * time.Millisecond,
		StopTimeout: 2 * time.Second,
	}
}

func waitState(t *testing.T, p *Process, want State) Status {
	t.Helper()
	var st Status
	if !testutil.WaitFor(timeout, func() bool { st = p.Status(); return st.State == want }) {
		t.Fatalf("state = %q, want %q (status %+v)", st.State, want, st)
	}
	return st
}

func TestStartRunsInCwd(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	out := filepath.Join(t.TempDir(), "cwd")
	s := spec(testutil.WriteCwd, out)
	s.Dir = dir

	p := Start(s)
	defer p.Stop()

	st := waitState(t, p, StateRunning)
	if st.PID == 0 || st.StartedAt.IsZero() {
		t.Errorf("running status missing pid/start time: %+v", st)
	}
	got, ok := testutil.ReadFile(out, timeout)
	if !ok {
		t.Fatal("helper never wrote its cwd")
	}
	if got, _ = filepath.EvalSymlinks(got); got != dir {
		t.Errorf("cwd = %q, want %q", got, dir)
	}
}

func TestStopTerminatesProcess(t *testing.T) {
	p := Start(spec(testutil.Sleep, ""))
	pid := waitState(t, p, StateRunning).PID

	p.Stop()

	if st := p.Status(); st.State != StateStopped || st.PID != 0 {
		t.Errorf("after Stop status = %+v", st)
	}
	if testutil.Alive(pid) {
		t.Errorf("pid %d still alive after Stop", pid)
	}
}

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

func TestCrashIsRestarted(t *testing.T) {
	out := filepath.Join(t.TempDir(), "marker")
	p := Start(spec(testutil.CrashOnce, out))
	defer p.Stop()

	if !testutil.WaitFor(timeout, func() bool { st := p.Status(); return st.State == StateRunning && st.Restarts == 1 }) {
		t.Fatalf("expected running after one restart, got %+v", p.Status())
	}
}

func TestMaxAttemptsMarksFailed(t *testing.T) {
	s := spec(testutil.Crash, "")
	s.MaxAttempts = 2
	s.Backoff = 50 * time.Millisecond

	start := time.Now()
	p := Start(s)
	select {
	case <-p.Done():
	case <-time.After(timeout):
		t.Fatalf("process never gave up: %+v", p.Status())
	}

	st := p.Status()
	if st.State != StateFailed || st.Restarts != 2 || st.ExitCode != 1 || st.Error == "" {
		t.Errorf("status = %+v, want failed after 2 restarts with exit 1", st)
	}
	// Backoff: 50ms before restart 1, 100ms before restart 2.
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Errorf("gave up after %v; backoff was not applied", elapsed)
	}
}

func TestRestartPolicies(t *testing.T) {
	tests := []struct {
		name   string
		policy config.RestartPolicy
		mode   string
		want   State
	}{
		{"never does not restart a crash", config.RestartNever, testutil.Crash, StateFailed},
		{"on-failure leaves a clean exit completed", config.RestartOnFailure, testutil.Exit0, StateCompleted},
		{"never leaves a clean exit completed", config.RestartNever, testutil.Exit0, StateCompleted},
		{"always restarts a clean exit", config.RestartAlways, testutil.Exit0, StateFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := spec(tt.mode, "")
			s.Policy = tt.policy
			s.MaxAttempts = 1
			p := Start(s)
			select {
			case <-p.Done():
			case <-time.After(timeout):
				t.Fatalf("process never settled: %+v", p.Status())
			}
			st := p.Status()
			if st.State != tt.want {
				t.Errorf("state = %q, want %q", st.State, tt.want)
			}
			wantRestarts := 0
			if tt.policy == config.RestartAlways {
				wantRestarts = 1
			}
			if st.Restarts != wantRestarts {
				t.Errorf("restarts = %d, want %d", st.Restarts, wantRestarts)
			}
		})
	}
}

func TestStableRunResetsAttempts(t *testing.T) {
	s := spec(testutil.Crash, "")
	s.MaxAttempts = 1
	s.StableAfter = time.Nanosecond // every run counts as stable
	p := Start(s)
	defer p.Stop()

	if !testutil.WaitFor(timeout, func() bool { return p.Status().Restarts >= 3 }) {
		t.Fatalf("expected restarts to continue past max_attempts, got %+v", p.Status())
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestEventsAreLogged(t *testing.T) {
	var events syncBuffer
	s := spec(testutil.CrashOnce, filepath.Join(t.TempDir(), "marker"))
	s.Events = &events
	p := Start(s)
	if !testutil.WaitFor(timeout, func() bool { return p.Status().Restarts == 1 && p.Status().State == StateRunning }) {
		t.Fatalf("never restarted: %+v", p.Status())
	}
	p.Stop()

	log := events.String()
	for _, want := range []string{"app: started (pid", "app: exited: exit status 1", "app: restarting in 10ms (attempt 1 of 3)", "app: stopped"} {
		if !strings.Contains(log, want) {
			t.Errorf("events missing %q:\n%s", want, log)
		}
	}
}

func TestCleanupRunsAroundEachRun(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	s := spec(testutil.CrashOnce, filepath.Join(t.TempDir(), "marker"))
	s.Cleanup = func() { mu.Lock(); calls++; mu.Unlock() }
	p := Start(s)
	if !testutil.WaitFor(timeout, func() bool { st := p.Status(); return st.State == StateRunning && st.Restarts == 1 }) {
		t.Fatalf("never restarted: %+v", p.Status())
	}
	p.Stop()
	mu.Lock()
	defer mu.Unlock()
	// Two runs (crash, then stopped), each with a cleanup before and after.
	if calls != 4 {
		t.Errorf("cleanup ran %d times, want 4", calls)
	}
}

func TestBackoffFor(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{10, maxBackoff},
	}
	for _, tt := range tests {
		if got := backoffFor(time.Second, tt.attempt); got != tt.want {
			t.Errorf("backoffFor(1s, %d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

func TestManagerAppActions(t *testing.T) {
	m := NewManager()
	s := spec(testutil.Sleep, "")
	s.Name = "api"
	if err := m.Start([]Spec{s}); err != nil {
		t.Fatal(err)
	}
	defer m.StopAll()
	status := func() Status { return m.Statuses()[0] }
	waitRunning := func() Status {
		t.Helper()
		var st Status
		if !testutil.WaitFor(timeout, func() bool { st = status(); return st.State == StateRunning }) {
			t.Fatalf("not running: %+v", st)
		}
		return st
	}
	first := waitRunning().PID

	if err := m.StartApp("api"); !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("StartApp on running app: err = %v", err)
	}

	if err := m.RestartApp("api"); err != nil {
		t.Fatalf("RestartApp: %v", err)
	}
	second := waitRunning().PID
	if second == first || testutil.Alive(first) {
		t.Errorf("restart kept old pid %d (new %d, old alive=%v)", first, second, testutil.Alive(first))
	}

	if err := m.StopApp("api"); err != nil {
		t.Fatalf("StopApp: %v", err)
	}
	if st := status(); st.State != StateStopped || testutil.Alive(second) {
		t.Errorf("after StopApp: %+v", st)
	}
	if err := m.StopApp("api"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("StopApp on stopped app: err = %v", err)
	}

	if err := m.StartApp("api"); err != nil {
		t.Fatalf("StartApp: %v", err)
	}
	waitRunning()

	for _, action := range []func(string) error{m.StartApp, m.StopApp, m.RestartApp} {
		if err := action("ghost"); !errors.Is(err, ErrUnknownApp) {
			t.Errorf("action on unknown app: err = %v", err)
		}
	}
}

func TestManagerStartsFailedApp(t *testing.T) {
	m := NewManager()
	s := spec(testutil.Crash, "")
	s.Name, s.Policy = "job", config.RestartNever
	m.Start([]Spec{s})
	defer m.StopAll()
	if !testutil.WaitFor(timeout, func() bool { return m.Statuses()[0].State == StateFailed }) {
		t.Fatalf("never failed: %+v", m.Statuses())
	}
	if err := m.StartApp("job"); err != nil {
		t.Fatalf("StartApp on failed app: %v", err)
	}
}

func TestManager(t *testing.T) {
	m := NewManager()
	a, b := spec(testutil.Sleep, ""), spec(testutil.Sleep, "")
	a.Name, b.Name = "a", "b"
	if err := m.Start([]Spec{a, b}); err != nil {
		t.Fatal(err)
	}
	if err := m.Start([]Spec{a}); err == nil {
		t.Error("expected duplicate name error")
	}

	if !testutil.WaitFor(timeout, func() bool {
		for _, st := range m.Statuses() {
			if st.State != StateRunning {
				return false
			}
		}
		return true
	}) {
		t.Fatalf("not all running: %+v", m.Statuses())
	}

	m.StopAll()
	sts := m.Statuses()
	if len(sts) != 2 || sts[0].Name != "a" || sts[1].Name != "b" {
		t.Fatalf("statuses not in start order: %+v", sts)
	}
	for _, st := range sts {
		if st.State != StateStopped {
			t.Errorf("%s state = %q after StopAll", st.Name, st.State)
		}
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
