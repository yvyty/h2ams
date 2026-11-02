package process

import (
	"errors"
	"fmt"
	"sync"
)

// Errors returned by per-app actions.
var (
	ErrUnknownApp     = errors.New("unknown app")
	ErrAlreadyRunning = errors.New("app is already running")
	ErrNotRunning     = errors.New("app is not running")
)

// Manager supervises a named set of processes and can start, stop, and
// restart each one individually.
type Manager struct {
	// actions serializes per-app start/stop/restart so two callers (CLI and
	// dashboard, say) cannot interleave on the same app.
	actions sync.Mutex

	mu    sync.Mutex
	order []string
	specs map[string]Spec
	procs map[string]*Process
}

// NewManager returns an empty Manager.
func NewManager() *Manager {
	return &Manager{specs: make(map[string]Spec), procs: make(map[string]*Process)}
}

// Start launches every spec. Names must be unique across the manager.
func (m *Manager) Start(specs []Spec) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range specs {
		if _, ok := m.specs[s.Name]; ok {
			return fmt.Errorf("process %q is already managed", s.Name)
		}
	}
	for _, s := range specs {
		m.specs[s.Name] = s
		m.procs[s.Name] = Start(s)
		m.order = append(m.order, s.Name)
	}
	return nil
}

// StartApp starts an app that is stopped or failed. The restart count starts
// over, since this is a deliberate start rather than a crash recovery.
func (m *Manager) StartApp(name string) error {
	m.actions.Lock()
	defer m.actions.Unlock()

	spec, proc, err := m.lookup(name)
	if err != nil {
		return err
	}
	if isActive(proc) {
		return fmt.Errorf("%s: %w", name, ErrAlreadyRunning)
	}
	m.replace(name, Start(spec))
	return nil
}

// StopApp stops an app and disables its restarts until StartApp is called.
func (m *Manager) StopApp(name string) error {
	m.actions.Lock()
	defer m.actions.Unlock()

	_, proc, err := m.lookup(name)
	if err != nil {
		return err
	}
	if !isActive(proc) {
		return fmt.Errorf("%s: %w", name, ErrNotRunning)
	}
	proc.Stop()
	return nil
}

// RestartApp stops an app (if it is running) and starts it again.
func (m *Manager) RestartApp(name string) error {
	m.actions.Lock()
	defer m.actions.Unlock()

	spec, proc, err := m.lookup(name)
	if err != nil {
		return err
	}
	proc.Stop()
	m.replace(name, Start(spec))
	return nil
}

// StopAll stops every process in parallel and waits for all of them.
func (m *Manager) StopAll() {
	m.actions.Lock()
	defer m.actions.Unlock()

	m.mu.Lock()
	procs := make([]*Process, 0, len(m.procs))
	for _, p := range m.procs {
		procs = append(procs, p)
	}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, p := range procs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Stop()
		}()
	}
	wg.Wait()
}

// Statuses returns a snapshot of every process in start order.
func (m *Manager) Statuses() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.order))
	for _, name := range m.order {
		out = append(out, m.procs[name].Status())
	}
	return out
}

func (m *Manager) lookup(name string) (Spec, *Process, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	spec, ok := m.specs[name]
	if !ok {
		return Spec{}, nil, fmt.Errorf("%w %q", ErrUnknownApp, name)
	}
	return spec, m.procs[name], nil
}

func (m *Manager) replace(name string, p *Process) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.procs[name] = p
}

// isActive reports whether the supervisor loop is still running (the app is
// starting, running, or waiting to restart).
func isActive(p *Process) bool {
	select {
	case <-p.Done():
		return false
	default:
		return true
	}
}
