package realtime

import (
	"context"
	"sync"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// turnCoordinator serializes assistant turns within one authenticated session
// while allowing unrelated sessions to run concurrently.
type turnCoordinator struct {
	mu    sync.Mutex
	gates map[string]*turnGate
}

type turnGate struct {
	token chan struct{}
	refs  int
}

func newTurnCoordinator() *turnCoordinator {
	return &turnCoordinator{gates: make(map[string]*turnGate)}
}

func (coordinator *turnCoordinator) acquire(
	ctx context.Context,
	scope tool.Scope,
) (func(), error) {
	key := scope.UserID + "\x00" + scope.SessionID
	coordinator.mu.Lock()
	gate := coordinator.gates[key]
	if gate == nil {
		gate = &turnGate{token: make(chan struct{}, 1)}
		coordinator.gates[key] = gate
	}
	gate.refs++
	coordinator.mu.Unlock()

	select {
	case gate.token <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() {
				<-gate.token
				coordinator.releaseReference(key, gate)
			})
		}, nil
	case <-ctx.Done():
		coordinator.releaseReference(key, gate)
		return nil, ctx.Err()
	}
}

func (coordinator *turnCoordinator) releaseReference(key string, gate *turnGate) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	gate.refs--
	if gate.refs == 0 && coordinator.gates[key] == gate {
		delete(coordinator.gates, key)
	}
}
