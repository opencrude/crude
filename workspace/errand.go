package workspace

import (
	"context"
	"errors"

	"crude/agent/session"
)

// workspaceErrandSessionKey stores the workspace's one Errand session identity.
// It is deliberately workspace metadata, not a shell preference: every shell
// that attaches to this workspace sees the same agent.
const workspaceErrandSessionKey = "agent.errand_session"

// liveErrandSession returns the running Errand, or nil when this workspace has
// none. The caller owns the serialized workspace state.
func (w *Workspace) liveErrandSession() *Session {
	for _, sess := range w.Sessions {
		if SessionIsErrand(sess) && sess.AgentID != "" && w.SessionsByAgent[sess.AgentID] == sess {
			return sess
		}
	}
	return nil
}

// PrepareErrandSession returns the workspace's Errand as a lifecycle operation.
// It reuses a live session, resumes the remembered saved one, or creates the
// first one. It never starts a turn.
func (w *Workspace) PrepareErrandSession() (*InvocationLifecycleOperation, error) {
	if w == nil || w.sessionCfg == nil {
		return nil, errors.New("Session configuration is unavailable")
	}
	if w.sender == nil {
		return nil, errors.New("Session event transport is unavailable")
	}
	if live := w.liveErrandSession(); live != nil {
		return &InvocationLifecycleOperation{ready: &SessionLifecycleResult{
			owner: live, agentID: live.AgentID, expectedAgentID: live.AgentID,
		}}, nil
	}
	id := w.errandSessionID
	if existing := w.SessionsByID[id]; existing != nil && !SessionIsErrand(existing) {
		// The user gave the remembered session another root role. Keep that
		// session and create a separate Errand instead of rewriting it.
		id = ""
	}
	cfg, err := invocationConfig(w.sessionCfg, InvocationSelection{MainAgent: session.CategoryErrand})
	if err != nil {
		return nil, err
	}
	op, err := w.prepareSessionLifecycle(id, cfg, w.homeDir, true)
	if err != nil {
		return nil, err
	}
	op.createIfMissing = true
	op.rememberRole = session.CategoryErrand
	op.rememberErrand = true
	return &InvocationLifecycleOperation{op: op}, nil
}

// EnsureErrandSession returns the running or saved Errand for this workspace,
// creating it once when the workspace has none. It does not submit input.
func (w *Workspace) EnsureErrandSession() *NewSessionResult {
	op, err := w.PrepareErrandSession()
	if err != nil {
		return &NewSessionResult{Error: err.Error(), NeedsAccount: errors.Is(err, session.ErrAccountRequired), NeedsModel: errors.Is(err, session.ErrModelRequired)}
	}
	return w.ApplyAgentSession(op.Run(context.Background()))
}

// ErrandAgentID returns the root agent of the running workspace Errand, or ""
// when it is not running. The caller owns the serialized workspace state.
func (w *Workspace) ErrandAgentID() string {
	if live := w.liveErrandSession(); live != nil {
		return live.AgentID
	}
	return ""
}
