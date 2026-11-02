package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/yvyty/h2ams/internal/process"
)

func snap(state process.State, restarts int) []process.Status {
	return []process.Status{{Name: "api", State: state, Restarts: restarts, Error: "exit status 1"}}
}

func TestCrashDetectorLoop(t *testing.T) {
	d := newCrashDetector()
	t0 := time.Unix(10_000, 0)

	steps := []struct {
		at       time.Duration
		state    process.State
		restarts int
		want     string // substring of the single expected alert, or ""
	}{
		{0, process.StateRunning, 0, ""},
		{5 * time.Second, process.StateRestarting, 1, ""},
		{10 * time.Second, process.StateRestarting, 2, ""},
		{15 * time.Second, process.StateRestarting, 3, "api is crash-looping: 3 restarts in the last 1m0s"},
		{20 * time.Second, process.StateRestarting, 4, ""}, // already alerted for this episode
		{90 * time.Second, process.StateRunning, 4, ""},    // window empties; stable → re-arm
		{91 * time.Second, process.StateRestarting, 5, ""},
		{92 * time.Second, process.StateRestarting, 6, ""},
		{93 * time.Second, process.StateRestarting, 7, "crash-looping"}, // new episode alerts again
	}
	for i, s := range steps {
		alerts := d.observe(t0.Add(s.at), snap(s.state, s.restarts))
		switch {
		case s.want == "" && len(alerts) != 0:
			t.Errorf("step %d: unexpected alerts %q", i, alerts)
		case s.want != "" && (len(alerts) != 1 || !strings.Contains(alerts[0], s.want)):
			t.Errorf("step %d: alerts = %q, want one containing %q", i, alerts, s.want)
		}
	}
}

func TestCrashDetectorSlowRestartsAreNotALoop(t *testing.T) {
	d := newCrashDetector()
	t0 := time.Unix(10_000, 0)
	for i := 1; i <= 10; i++ {
		if alerts := d.observe(t0.Add(time.Duration(i)*40*time.Second), snap(process.StateRunning, i)); len(alerts) != 0 {
			t.Fatalf("restart every 40s flagged as a loop: %q", alerts)
		}
	}
}

func TestCrashDetectorFailed(t *testing.T) {
	d := newCrashDetector()
	now := time.Unix(10_000, 0)
	d.observe(now, snap(process.StateRunning, 0))
	alerts := d.observe(now.Add(time.Second), snap(process.StateFailed, 0))
	if len(alerts) != 1 || alerts[0] != "api failed: exit status 1" {
		t.Fatalf("alerts = %q", alerts)
	}
	if alerts := d.observe(now.Add(2*time.Second), snap(process.StateFailed, 0)); len(alerts) != 0 {
		t.Errorf("failed alerted twice: %q", alerts)
	}

	// Manually started again, then fails again: alert again.
	d.observe(now.Add(3*time.Second), snap(process.StateRunning, 0))
	if alerts := d.observe(now.Add(4*time.Second), snap(process.StateFailed, 0)); len(alerts) != 1 {
		t.Errorf("second failure not alerted: %q", alerts)
	}
}

func TestCrashDetectorManualRestartResets(t *testing.T) {
	d := newCrashDetector()
	t0 := time.Unix(10_000, 0)
	d.observe(t0, snap(process.StateRunning, 0))
	d.observe(t0.Add(time.Second), snap(process.StateRestarting, 2))
	// Manual restart: fresh process, count back to 0.
	d.observe(t0.Add(2*time.Second), snap(process.StateRunning, 0))
	if alerts := d.observe(t0.Add(3*time.Second), snap(process.StateRestarting, 1)); len(alerts) != 0 {
		t.Errorf("restarts before the manual restart were counted: %q", alerts)
	}
}
