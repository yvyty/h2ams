// Package process starts, stops, and supervises child processes.
package process

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/yvyty/h2ams/internal/config"
)

// State is the lifecycle state of a managed process.
type State string

const (
	StateStarting   State = "starting"
	StateRunning    State = "running"
	StateRestarting State = "restarting"
	StateStopped    State = "stopped"
	StateCompleted  State = "completed" // exited 0 and its policy does not restart it
	StateFailed     State = "failed"
)

// Defaults for optional Spec fields.
const (
	DefaultStopTimeout = 5 * time.Second
	DefaultStableAfter = 10 * time.Second
	maxBackoff         = 30 * time.Second
)

// Spec describes how to run and supervise one process.
type Spec struct {
	Name    string
	Command string   // run through the platform shell
	Dir     string   // working directory; empty means the current one
	Env     []string // full environment; nil inherits the parent's

	Policy      config.RestartPolicy
	MaxAttempts int           // consecutive restarts before giving up
	Backoff     time.Duration // delay before the first restart; doubles each time

	// StableAfter resets the consecutive-restart count once a run lasts this long.
	StableAfter time.Duration
	// StopTimeout is how long Stop waits after a graceful signal before killing.
	StopTimeout time.Duration

	Stdout, Stderr io.Writer // nil discards output
	// Events receives one timestamped line per lifecycle change (start, exit,
	// restart, stop). Usually the same writer as Stdout; nil disables them.
	Events io.Writer

	// Cleanup, if set, runs before every start and after every exit (crash
	// or stop). Docker apps use it to remove a leftover container.
	Cleanup func()
}

// Status is a snapshot of a process, safe to serialize.
type Status struct {
	Name      string    `json:"name"`
	State     State     `json:"state"`
	PID       int       `json:"pid,omitempty"`
	Restarts  int       `json:"restarts"`
	ExitCode  int       `json:"exit_code,omitempty"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at,omitzero"`
}

// Process supervises one command according to its Spec.
type Process struct {
	spec     Spec
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}

	mu     sync.Mutex
	status Status
}

// Start launches the command and supervises it in the background.
func Start(spec Spec) *Process {
	if spec.StopTimeout <= 0 {
		spec.StopTimeout = DefaultStopTimeout
	}
	if spec.StableAfter <= 0 {
		spec.StableAfter = DefaultStableAfter
	}
	p := &Process{
		spec:   spec,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
		status: Status{Name: spec.Name, State: StateStarting},
	}
	go p.supervise()
	return p
}

// Stop terminates the process (if running), disables restarts, and waits.
func (p *Process) Stop() {
	p.stopOnce.Do(func() { close(p.stop) })
	<-p.done
}

// Done is closed once the process is stopped or has failed for good.
func (p *Process) Done() <-chan struct{} { return p.done }

// Status returns a snapshot of the current state.
func (p *Process) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

func (p *Process) supervise() {
	defer close(p.done)
	attempts := 0
	for {
		started := time.Now()
		exitErr, stopped := p.runOnce()
		if stopped {
			p.settle(StateStopped, nil)
			p.event("stopped")
			return
		}
		if time.Since(started) >= p.spec.StableAfter {
			attempts = 0
		}

		failed := exitErr != nil
		if !p.shouldRestart(failed) {
			if failed {
				p.settle(StateFailed, exitErr)
				p.event("failed; restart policy is %s", p.spec.Policy)
			} else {
				p.settle(StateCompleted, nil)
			}
			return
		}
		if attempts >= p.spec.MaxAttempts {
			err := fmt.Errorf("gave up after %d restarts: %w", attempts, lastErr(exitErr))
			p.settle(StateFailed, err)
			p.event("%v", err)
			return
		}

		attempts++
		delay := backoffFor(p.spec.Backoff, attempts)
		p.event("restarting in %v (attempt %d of %d)", delay, attempts, p.spec.MaxAttempts)
		p.update(func(s *Status) {
			s.State = StateRestarting
			s.PID = 0
			s.Restarts++
		})
		select {
		case <-time.After(delay):
		case <-p.stop:
			p.settle(StateStopped, nil)
			p.event("stopped")
			return
		}
	}
}

// runOnce runs the command until it exits or Stop is called.
func (p *Process) runOnce() (exitErr error, stopped bool) {
	if p.spec.Cleanup != nil {
		p.spec.Cleanup()
		defer p.spec.Cleanup()
	}
	cmd := newShellCommand(p.spec.Command)
	cmd.Dir = p.spec.Dir
	cmd.Env = p.spec.Env
	cmd.Stdout = p.spec.Stdout
	cmd.Stderr = p.spec.Stderr
	cmd.WaitDelay = time.Second

	if err := cmd.Start(); err != nil {
		p.recordExit(-1, err)
		p.event("failed to start: %v", err)
		return err, false
	}
	pid := cmd.Process.Pid
	p.event("started (pid %d)", pid)
	p.update(func(s *Status) {
		s.State = StateRunning
		s.PID = pid
		s.Error = ""
		s.ExitCode = 0
		s.StartedAt = time.Now()
	})

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	select {
	case err := <-waitErr:
		killGroup(pid) // reap anything the command left behind in its group
		p.recordExit(exitCode(err), err)
		p.event("exited: %v", describeExit(err))
		return err, false
	case <-p.stop:
		p.terminate(pid, waitErr)
		return nil, true
	}
}

// terminate asks the process group to exit, then kills it after StopTimeout.
//
// The leader exiting is not the end: on Linux /bin/sh (dash) does not exec a
// single command, so the leader is a shell that dies on SIGTERM at once while
// the real app is still shutting down. The rest of the group gets whatever is
// left of the grace period before it is killed.
func (p *Process) terminate(pid int, waitErr <-chan error) {
	terminateGroup(pid)
	deadline := time.NewTimer(p.spec.StopTimeout)
	defer deadline.Stop()
	select {
	case <-waitErr:
	case <-deadline.C:
		killGroup(pid)
		<-waitErr
		return
	}
	poll := time.NewTicker(20 * time.Millisecond)
	defer poll.Stop()
	for groupAlive(pid) {
		select {
		case <-deadline.C:
			killGroup(pid)
			return
		case <-poll.C:
		}
	}
	killGroup(pid) // in case something appeared between the check and here
}

func (p *Process) shouldRestart(failed bool) bool {
	switch p.spec.Policy {
	case config.RestartAlways:
		return true
	case config.RestartOnFailure:
		return failed
	default:
		return false
	}
}

func (p *Process) recordExit(code int, err error) {
	p.update(func(s *Status) {
		s.ExitCode = code
		if err != nil {
			s.Error = err.Error()
		} else {
			s.Error = ""
		}
	})
}

func (p *Process) settle(state State, err error) {
	p.update(func(s *Status) {
		s.State = state
		s.PID = 0
		if err != nil {
			s.Error = err.Error()
		}
	})
}

func (p *Process) event(format string, args ...any) {
	if p.spec.Events == nil {
		return
	}
	fmt.Fprintf(p.spec.Events, "[h2ams %s] %s: %s\n",
		time.Now().Format(time.RFC3339), p.spec.Name, fmt.Sprintf(format, args...))
}

func describeExit(err error) string {
	if err == nil {
		return "exit status 0"
	}
	return err.Error()
}

func (p *Process) update(fn func(*Status)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(&p.status)
}

// backoffFor returns the delay before restart number attempt (1-based).
func backoffFor(base time.Duration, attempt int) time.Duration {
	d := base
	for i := 1; i < attempt && d < maxBackoff; i++ {
		d *= 2
	}
	return min(d, maxBackoff)
}

func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

func lastErr(err error) error {
	if err == nil {
		return errors.New("process exited")
	}
	return err
}
