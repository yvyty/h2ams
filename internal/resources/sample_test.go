//go:build linux || darwin

package resources

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
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

func TestSampleReportsRunningProcess(t *testing.T) {
	p := process.Start(process.Spec{
		Name:    "app",
		Command: testutil.Command(testutil.Sleep, ""),
		Policy:  config.RestartNever,
	})
	defer p.Stop()
	if !testutil.WaitFor(5*time.Second, func() bool { return p.Status().PID != 0 }) {
		t.Fatal("process never started")
	}
	pgid := p.Status().PID

	s := NewSampler()
	var u Usage
	// Two samples so Linux can compute a CPU delta.
	for range 2 {
		got, err := s.Sample([]int{pgid})
		if err != nil {
			t.Fatalf("Sample: %v", err)
		}
		u = got[pgid]
		time.Sleep(50 * time.Millisecond)
	}
	// A Go test binary has at least a few MB resident; anything under 100 GB is sane.
	if u.MemoryBytes < 1<<20 || u.MemoryBytes > 100<<30 {
		t.Errorf("memory = %d bytes, want a plausible RSS", u.MemoryBytes)
	}
	if u.CPUPercent < 0 {
		t.Errorf("cpu = %v, want >= 0", u.CPUPercent)
	}

	got, err := s.Sample([]int{999999999})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("unknown group reported usage: %v", got)
	}
}

// TestSampleCountsDescendantsInOtherGroups starts a child in a new process
// group (like Erlang's port programs) and checks its memory is attributed to
// the parent's group.
func TestSampleCountsDescendantsInOtherGroups(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no process groups")
	}
	// The child moves itself into a new group (setpgrp) and holds ~60 MB.
	cmd := exec.Command("/bin/sh", "-c", "perl -e 'setpgrp(0, 0); $x = q(a) x 60_000_000; sleep 30' & wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = exec.Command("pkill", "-KILL", "-P", strconv.Itoa(cmd.Process.Pid)).Run()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	}()

	s := NewSampler()
	var u Usage
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := s.Sample([]int{cmd.Process.Pid})
		if err != nil {
			t.Skip(err)
		}
		u = got[cmd.Process.Pid]
		if u.MemoryBytes > 50<<20 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("child in its own group not counted: %+v", u)
}
