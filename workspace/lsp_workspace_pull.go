package workspace

import (
	"context"
	"errors"
	"slices"
	"time"

	"crude/lsp/lspclient"
	"crude/types/setting"
)

// A server that offers workspace diagnostics (LSP 3.17 workspace/diagnostic)
// reports the whole project when asked, so Crude does not check its closed
// files one at a time. Like VS Code's language client, Crude keeps one
// workspace request per server: when the server ends it, Crude asks again
// after a short rest, and it stops after repeated errors, when the closed-file
// check covers the server instead. **Check closed files** governs the request
// like the check: Off starts none, When idle starts and repeats it only when
// the check's own gate allows, Always whenever the server runs. A running
// request is not cancelled for typing, because the server would start over.

// lspWorkspaceDiagnosticsClient is implemented by transports whose server
// offers workspace diagnostics.
type lspWorkspaceDiagnosticsClient interface {
	WorkspaceDiagnostics() bool
	PullWorkspaceDiagnostics(context.Context) error
}

// workspacePullState is one server instance's workspace request loop.
type workspacePullState struct {
	ctx    context.Context
	cancel context.CancelFunc
	// failed ends the loop for this instance after repeated errors.
	failed bool
}

// VS Code stops after its sixth error in a row.
const workspacePullErrorLimit = 6

// VS Code requests again two seconds after a workspace request ends.
var workspacePullRest = 2 * time.Second

// workspacePullLocked reports whether the server covers the project through
// workspace diagnostics: it offers them, they work, and the user lets Crude
// look at closed files.
func (m *LSPManager) workspacePullLocked(state *lspClientState) bool {
	if m.forceFallback || state == nil || state.client == nil || state.closing || state.stopped || state.workspacePull.failed ||
		!m.settings.Diagnostics || m.settings.ProjectCheckMode() == setting.ProjectCheckOff {
		return false
	}
	client, ok := state.client.(lspWorkspaceDiagnosticsClient)
	return ok && client.WorkspaceDiagnostics()
}

// tickWorkspacePullLocked starts the loop for each server that covers the
// project through workspace diagnostics and stops loops that may not run.
func (m *LSPManager) tickWorkspacePullLocked() {
	for key, state := range m.states {
		wanted := state.status == LSPConnected && m.projectCheckAllowedLocked() && m.workspacePullLocked(state)
		switch {
		case !wanted && state.workspacePull.cancel != nil && state.workspacePull.ctx.Err() == nil:
			state.workspacePull.cancel()
			// The loop releases its slot on return, before a replacement starts.
			if m.projectCheckAllowedLocked() && !state.closing && !state.stopped {
				m.requestProjectCheckLocked()
			}
		case wanted && state.workspacePull.cancel == nil:
			// Late registration can arrive during fallback for this root. End
			// that pass without turning borrowed views into failed checks. Its
			// retained answers still count when the other roots resume.
			if m.checkRunning && m.checkTarget == key {
				m.cancelProjectCheckLocked()
				continue
			}
			// Reconcile sends the queued didClose before trying again. If the
			// first workspace report arrives before then, it can be ignored as
			// an open-file report and never be sent again on the live request.
			if slices.ContainsFunc(m.closedDocuments, func(doc lspClosedDocument) bool { return doc.client == state.client }) {
				continue
			}
			ctx, cancel := context.WithCancel(context.Background())
			state.workspacePull.ctx, state.workspacePull.cancel = ctx, cancel
			m.workspacePulls.Add(1)
			go m.runWorkspacePull(ctx, key, state, state.client.(lspWorkspaceDiagnosticsClient), m.settingsGeneration)
		}
	}
}

func (m *LSPManager) runWorkspacePull(ctx context.Context, key string, state *lspClientState, client lspWorkspaceDiagnosticsClient, generation uint64) {
	defer m.workspacePulls.Done()
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if state.workspacePull.ctx == ctx {
			state.workspacePull.cancel()
			state.workspacePull.ctx, state.workspacePull.cancel = nil, nil
			if !m.closed && m.states[key] == state {
				if !m.workspacePullLocked(state) && m.projectCheckAllowedLocked() {
					m.requestProjectCheckLocked()
				} else {
					m.signalLocked()
				}
			}
		}
	}()
	errorsInRow := 0
	for {
		if !m.awaitWorkspacePullTurn(ctx, key, state, generation) {
			return
		}
		err := client.PullWorkspaceDiagnostics(ctx)
		if ctx.Err() != nil {
			return
		}
		rest := workspacePullRest
		switch {
		case errors.Is(err, lspclient.ErrWorkspacePullRefresh):
			errorsInRow, rest = 0, 0
		case err != nil:
			errorsInRow++
			if m.logger != nil {
				m.logger.Warn("lsp", "workspace diagnostics failed", "server", state.cmd, "error", err.Error())
			}
			if errorsInRow >= workspacePullErrorLimit {
				m.mu.Lock()
				if ctx.Err() == nil && m.states[key] == state && state.workspacePull.ctx == ctx {
					// The closed-file check covers the server from now on.
					state.workspacePull.failed = true
					m.bumpVersionLocked()
					m.requestProjectCheckLocked()
				}
				m.mu.Unlock()
				return
			}
		default:
			errorsInRow = 0
		}
		timer := time.NewTimer(rest)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// awaitWorkspacePullTurn waits until the closed-file gate lets the server's
// workspace request start. It reports false when the loop must end.
func (m *LSPManager) awaitWorkspacePullTurn(ctx context.Context, key string, state *lspClientState, generation uint64) bool {
	for {
		// The probe reads the system; never under the manager lock.
		load := m.loadSample()
		m.mu.Lock()
		if ctx.Err() != nil || m.states[key] != state || generation != m.settingsGeneration ||
			!m.projectCheckAllowedLocked() || !m.workspacePullLocked(state) {
			m.mu.Unlock()
			return false
		}
		wait := m.projectCheckWaitLocked(key, load)
		measuring := load.Warming && wait == "" && m.settings.ProjectCheckMode() != setting.ProjectCheckAlways
		m.mu.Unlock()
		if wait == "" && !measuring {
			return true
		}
		timer := time.NewTimer(projectCheckPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}
