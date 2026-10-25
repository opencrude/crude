package viewmodel

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// clientIOCommand retains a shell-owned command through actual completion. A
// command cancelled before execution never starts; workspace switches cancel
// running I/O and StopAsyncIO joins it before removing caches/closing views.
func (m *App) clientIOCommand(parent context.Context, run func(context.Context) tea.Msg) tea.Cmd {
	state, generation := asyncIOStateFor(m), m.workspaceSwitchGen.Load()
	state.mu.Lock()
	epoch := state.clientIOEpoch
	state.mu.Unlock()
	return func() tea.Msg {
		ctx, release, err := m.beginClientIO(state, parent, generation, epoch)
		if err != nil {
			return nil
		}
		defer release()
		return run(ctx)
	}
}

func (m *App) beginClientIO(state *appAsyncIOState, parent context.Context, generation, epoch uint64) (context.Context, func(), error) {
	ctx, cancel := context.WithCancel(parent)
	state.mu.Lock()
	if ctx.Err() != nil || generation != m.workspaceSwitchGen.Load() || epoch != state.clientIOEpoch || m.asyncIOStopped.Load() {
		state.mu.Unlock()
		cancel()
		return nil, nil, context.Canceled
	}
	state.clientIONext++
	id := state.clientIONext
	state.clientIOJobs[id] = cancel
	state.clientIOWG.Add(1)
	state.mu.Unlock()
	return ctx, func() {
		cancel()
		state.mu.Lock()
		delete(state.clientIOJobs, id)
		state.mu.Unlock()
		state.clientIOWG.Done()
	}, nil
}

func (m *App) cancelClientIOOperations() {
	state := m.asyncIO.Load()
	if state == nil {
		return
	}
	state.mu.Lock()
	state.clientIOEpoch++
	for _, cancel := range state.clientIOJobs {
		cancel()
	}
	state.mu.Unlock()
}
