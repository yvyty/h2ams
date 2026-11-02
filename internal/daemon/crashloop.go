package daemon

import (
	"fmt"
	"time"

	"github.com/yvyty/h2ams/internal/process"
)

// Crash-loop thresholds: this many automatic restarts within the window.
const (
	crashLoopRestarts = 3
	crashLoopWindow   = time.Minute
)

// crashDetector turns a stream of status snapshots into alerts: once when an
// app restarts crashLoopRestarts times within crashLoopWindow, and once when
// it gives up (failed). Alerts re-arm after the app settles down.
type crashDetector struct {
	apps map[string]*crashState
}

type crashState struct {
	lastRestarts int
	recent       []time.Time // restart times inside the window
	loopAlerted  bool
	failAlerted  bool
}

func newCrashDetector() *crashDetector {
	return &crashDetector{apps: make(map[string]*crashState)}
}

// observe records a snapshot taken at now and returns any new alert messages.
func (d *crashDetector) observe(now time.Time, statuses []process.Status) []string {
	var alerts []string
	for _, st := range statuses {
		s, ok := d.apps[st.Name]
		if !ok {
			s = &crashState{lastRestarts: st.Restarts}
			d.apps[st.Name] = s
		}

		switch {
		case st.Restarts > s.lastRestarts:
			for range st.Restarts - s.lastRestarts {
				s.recent = append(s.recent, now)
			}
		case st.Restarts < s.lastRestarts:
			// A manual start/restart reset the count: a fresh episode.
			*s = crashState{}
		}
		s.lastRestarts = st.Restarts

		cutoff := now.Add(-crashLoopWindow)
		for len(s.recent) > 0 && s.recent[0].Before(cutoff) {
			s.recent = s.recent[1:]
		}

		if len(s.recent) >= crashLoopRestarts && !s.loopAlerted {
			s.loopAlerted = true
			alerts = append(alerts, fmt.Sprintf("%s is crash-looping: %d restarts in the last %v", st.Name, len(s.recent), crashLoopWindow))
		}
		if len(s.recent) == 0 && st.State == process.StateRunning {
			s.loopAlerted = false
		}

		if st.State == process.StateFailed {
			if !s.failAlerted {
				s.failAlerted = true
				msg := st.Name + " failed"
				if st.Error != "" {
					msg += ": " + st.Error
				}
				alerts = append(alerts, msg)
			}
		} else {
			s.failAlerted = false
		}
	}
	return alerts
}
