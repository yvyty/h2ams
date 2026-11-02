package process

import (
	"fmt"
	"sync"
)

// Manager supervises a named set of processes.
type Manager struct {
	mu    sync.Mutex
	order []string
	procs map[string]*Process
}

// NewManager returns an empty Manager.
func NewManager() *Manager {
	return &Manager{procs: make(map[string]*Process)}
}

// Start launches every spec. Names must be unique across the manager.
func (m *Manager) Start(specs []Spec) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range specs {
		if _, ok := m.procs[s.Name]; ok {
			return fmt.Errorf("process %q is already managed", s.Name)
		}
	}
	for _, s := range specs {
		m.procs[s.Name] = Start(s)
		m.order = append(m.order, s.Name)
	}
	return nil
}

// StopAll stops every process in parallel and waits for all of them.
func (m *Manager) StopAll() {
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
