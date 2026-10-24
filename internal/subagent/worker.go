package subagent

import (
	"context"
	"errors"
)

// ErrUnsupportedCapability means a configured child cannot execute a requested tool.
var ErrUnsupportedCapability = errors.New("subagent: unsupported capability")

// WorkerRunner executes one task and must honor context cancellation. Production
// managers use RealWorker. Tests can inject a deterministic implementation.
type WorkerRunner interface {
	Run(context.Context) (*Result, error)
}

// WorkerFactory builds a fresh runner for each task. Supplying a factory is an
// explicit dependency override; provider failures never select another runner.
type WorkerFactory func(RealWorkerConfig) (WorkerRunner, error)

// SetWorkerFactory overrides worker construction. Passing nil restores the
// production text-only worker. Tool-capable delegation uses SetDriverFactory.
func (m *Manager) SetWorkerFactory(factory WorkerFactory) {
	m.mu.Lock()
	m.workerFactory = factory
	m.guardedWorkers = false
	m.mu.Unlock()
}

// SetGuardedWorkerFactory is trusted application wiring: every effectful child
// adapter must inherit the parent's execution guard. Delegation itself then
// needs no project-wide lock around its entire nested model/tool loop.
func (m *Manager) SetGuardedWorkerFactory(factory WorkerFactory) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workerFactory = factory
	m.guardedWorkers = factory != nil
}
func (m *Manager) GuardsWorkerTools() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.guardedWorkers
}
