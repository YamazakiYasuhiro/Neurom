package module

import (
	"context"
	"fmt"
	"sync"

	"github.com/axsh/neurom/internal/bus"
)

// Manager handles the lifecycle of VM modules (Start, Stop).
type Manager struct {
	mu      sync.Mutex
	modules []Module
	started bool
	bus     bus.Bus
	cancel  context.CancelFunc
}

// NewManager creates a new Module Manager.
func NewManager(b bus.Bus) *Manager {
	return &Manager{
		bus: b,
	}
}

// Register adds a module to the manager.
func (m *Manager) Register(mod Module) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modules = append(m.modules, mod)
}

// StartAll starts all registered modules in separate goroutines.
func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.started {
		return fmt.Errorf("modules already started")
	}

	startCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.started = true

	for _, mod := range m.modules {
		// Attempt to start module immediately. If it needs block, it should handle its own goroutine internally.
		// Usually Start() spawns a goroutine and returns nil.
		if err := mod.Start(startCtx, m.bus); err != nil {
			cancel() // cancel others on failure
			return fmt.Errorf("failed to start module %s: %w", mod.Name(), err)
		}
	}

	return nil
}

// StopAll signals all modules to stop and waits for completion (if Stop blocks).
func (m *Manager) StopAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return nil
	}

	if m.cancel != nil {
		m.cancel()
	}

	var errs []error
	for _, mod := range m.modules {
		if err := mod.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("module %s stop failed: %w", mod.Name(), err))
		}
	}

	m.started = false

	if len(errs) > 0 {
		return fmt.Errorf("errors during StopAll: %v", errs)
	}

	return nil
}
