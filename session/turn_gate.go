package session

import (
	"context"
	"sync"

	"golang.org/x/sync/semaphore"
)

// turnGateCapacity is the exclusive weight; each turn holds weight one.
const turnGateCapacity = 1 << 30

// turnGate admits concurrent turns (shared) or one context replacement
// (exclusive). It is an RWMutex whose exclusive waiter can stop waiting when
// its context ends, so an admitted compaction can wait for a finishing turn
// without becoming uncancellable. Waiters are served in order: a waiting
// exclusive owner is not starved by later turns. The zero value is ready.
type turnGate struct {
	once sync.Once
	sem  *semaphore.Weighted
}

func (g *turnGate) weighted() *semaphore.Weighted {
	g.once.Do(func() { g.sem = semaphore.NewWeighted(turnGateCapacity) })
	return g.sem
}

// RLock admits one turn. Turns never give up waiting.
func (g *turnGate) RLock() { _ = g.weighted().Acquire(context.Background(), 1) }

func (g *turnGate) RUnlock() { g.weighted().Release(1) }

// TryLock takes exclusive ownership only when nothing holds or awaits the gate.
func (g *turnGate) TryLock() bool { return g.weighted().TryAcquire(turnGateCapacity) }

func (g *turnGate) Lock() { _ = g.weighted().Acquire(context.Background(), turnGateCapacity) }

// LockContext waits for exclusive ownership until ctx ends.
func (g *turnGate) LockContext(ctx context.Context) error {
	return g.weighted().Acquire(ctx, turnGateCapacity)
}

func (g *turnGate) Unlock() { g.weighted().Release(turnGateCapacity) }
