package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/yvyty/h2ams/internal/notify"
	"github.com/yvyty/h2ams/internal/ports"
	"github.com/yvyty/h2ams/internal/process"
	"github.com/yvyty/h2ams/internal/resources"
)

// Monitoring cadence: crash detection every tick, resource sampling every
// sampleEvery ticks.
const (
	monitorTick = time.Second
	sampleEvery = 2
)

// service is the daemon's application logic, shared by the Unix-socket API
// (used by the CLI) and the web dashboard, so both behave identically.
type service struct {
	mgr   *process.Manager
	paths Paths
	info  Response // fields that don't change while the daemon runs

	usageMu sync.Mutex
	usage   map[int]resources.Usage // by process group ID, from the monitor
}

// status returns the full daemon status with ports and resource use attached.
func (s *service) status() Response {
	resp := s.info
	resp.Apps = withPorts(s.mgr.Statuses())
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	for i, app := range resp.Apps {
		if u, ok := s.usage[app.PID]; ok && app.State == process.StateRunning {
			cpu := u.CPUPercent
			resp.Apps[i].CPUPercent = &cpu
			resp.Apps[i].MemoryBytes = u.MemoryBytes
		}
	}
	return resp
}

// monitor samples resource use and, when notifier is non-nil, sends crash-loop
// alerts, until ctx is done.
func (s *service) monitor(ctx context.Context, notifier notify.Notifier, logf func(string, ...any)) {
	sampler := resources.NewSampler()
	detector := newCrashDetector()
	tick := time.NewTicker(monitorTick)
	defer tick.Stop()
	sampleErrLogged := false

	for n := 0; ; n++ {
		statuses := s.mgr.Statuses()
		if n%sampleEvery == 0 {
			if err := s.sample(sampler, statuses); err != nil && !sampleErrLogged {
				logf("resource monitoring unavailable: %v", err)
				sampleErrLogged = true
			}
		}
		for _, msg := range detector.observe(time.Now(), statuses) {
			logf("alert: %s", msg)
			if notifier != nil {
				if err := notifier.Notify("h2ams", msg); err != nil {
					logf("notification failed: %v", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *service) sample(sampler *resources.Sampler, statuses []process.Status) error {
	var pgids []int
	for _, st := range statuses {
		if st.State == process.StateRunning && st.PID != 0 {
			pgids = append(pgids, st.PID)
		}
	}
	usage, err := sampler.Sample(pgids)
	if err != nil {
		return err
	}
	s.usageMu.Lock()
	s.usage = usage
	s.usageMu.Unlock()
	return nil
}

// errBadAction is returned for an action the daemon doesn't know.
var errBadAction = errors.New("unknown action")

// act runs a per-app action: CmdStart, CmdStop, or CmdRestart.
func (s *service) act(cmd, app string) error {
	switch cmd {
	case CmdStart:
		return s.mgr.StartApp(app)
	case CmdStop:
		return s.mgr.StopApp(app)
	case CmdRestart:
		return s.mgr.RestartApp(app)
	default:
		return fmt.Errorf("%w %q", errBadAction, cmd)
	}
}

// hasApp reports whether app is managed by this daemon.
func (s *service) hasApp(app string) bool {
	for _, st := range s.mgr.Statuses() {
		if st.Name == app {
			return true
		}
	}
	return false
}

// withPorts attaches listening ports to running apps. Each app leads its own
// process group, so its PID is also the group ID covering any children.
// Detection is best-effort: on failure apps are reported without ports.
func withPorts(statuses []process.Status) []AppStatus {
	var pgids []int
	for _, st := range statuses {
		if st.State == process.StateRunning && st.PID != 0 {
			pgids = append(pgids, st.PID)
		}
	}
	found, _ := ports.Listening(pgids)

	apps := make([]AppStatus, len(statuses))
	for i, st := range statuses {
		apps[i] = AppStatus{Status: st}
		if st.State == process.StateRunning {
			apps[i].Ports = found[st.PID]
		}
	}
	return apps
}
