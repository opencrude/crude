package fileio

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"crude/types/command"
)

// TaskOutput is a running operation's output stream and latest measured progress.
type TaskOutput struct{ state *asyncCmdState }

func (o TaskOutput) Write(p []byte) (int, error) { return o.state.Write(p) }

// Report keeps only the latest counters. Only phase transitions enter the log.
func (o TaskOutput) Report(p command.Progress) {
	st := o.state
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.info.Status != AsyncRunning || st.info.Progress == p {
		return
	}
	changed := st.info.Progress.Phase != p.Phase
	st.info.Progress = p
	if changed {
		_, _ = io.WriteString(st, p.Phase+"\n")
	}
}

// StartTask admits exact, already authorized work. Like an untracked process it
// returns inline or promotes without restarting. The original request owns the
// wait before promotion; the manager cancels and joins even unpublished tasks
// at shutdown. Key excludes concurrent work on the same resource, not retries.
func (m *AsyncCommandManager) StartTask(parent context.Context, key, label, cwd string, run func(context.Context, TaskOutput) error) (*UntrackedCmd, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return nil, errors.New("background job manager is closing")
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	if other := m.tasks[key]; other != nil {
		other.mu.Lock()
		handle := other.info.Handle
		other.mu.Unlock()
		if handle != "" {
			return nil, fmt.Errorf("%s is already running as %s; use its existing job", label, handle)
		}
		return nil, fmt.Errorf("%s is already running", label)
	}
	ctx, stop := context.WithCancelCause(context.WithoutCancel(parent))
	cancel := func() { stop(command.ErrStopped) }
	st := &asyncCmdState{cancel: cancel, done: make(chan struct{}), info: AsyncCmdInfo{
		TaskID: rand.Text(), Command: label, CWD: cwd, StartedAt: time.Now(), Status: AsyncRunning,
	}}
	if m.tasks == nil {
		m.tasks = make(map[string]*asyncCmdState)
	}
	m.tasks[key] = st
	go func() {
		var err error
		defer func() {
			if recover() != nil {
				err = errors.New("background operation stopped unexpectedly; inspect its state before retrying")
			}
			cause := ctx.Err()
			if errors.Is(err, context.DeadlineExceeded) {
				cause = context.DeadlineExceeded
			}
			st.publishOutcome(cause, err, nil, false)
			cancel()
			m.mu.Lock()
			delete(m.tasks, key)
			close(st.done)
			m.mu.Unlock()
			m.notifySettled(st)
		}()
		err = run(ctx, TaskOutput{state: st})
	}()
	return &UntrackedCmd{state: st, cancel: cancel, logger: m.logger}, nil
}
