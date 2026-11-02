//go:build linux || darwin

package resources

import (
	"os"
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
