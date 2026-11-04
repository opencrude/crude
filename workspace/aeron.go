package workspace

import (
	"context"
	"errors"

	"crude/session"
)

// workspaceAeronSessionKey stores the workspace's one Aeron session identity.
// It is deliberately workspace metadata, not a shell preference: every shell
// that attaches to this workspace sees the same agent.
const workspaceAeronSessionKey = "agent.aeron_session"

// liveAeronSession returns the running Aeron, or nil when this workspace has
// none. The caller owns the serialized workspace state.
func (w *Workspace) liveAeronSession() *Session {
	for _, sess := range w.Sessions {
		if SessionIsAeron(sess) && sess.AgentID != "" && w.SessionsByAgent[sess.AgentID] == sess {
			return sess
		}
	}
	return nil
}

// PrepareAeronSession returns the workspace's Aeron as a lifecycle operation.
// It reuses a live session, resumes the remembered saved one, or creates the
// first one. It never starts a turn.
func (w *Workspace) PrepareAeronSession() (*InvocationLifecycleOperation, error) {
	if w == nil || w.sessionCfg == nil {
		return nil, errors.New("Session configuration is unavailable")
	}
	if w.sender == nil {
		return nil, errors.New("Session event transport is unavailable")
	}
	if live := w.liveAeronSession(); live != nil {
		return &InvocationLifecycleOperation{ready: &SessionLifecycleResult{
			owner: live, agentID: live.AgentID, expectedAgentID: live.AgentID,
		}}, nil
	}
	id := w.aeronSessionID
	if existing := w.SessionsByID[id]; existing != nil && !SessionIsAeron(existing) {
		// The user gave the remembered session another root role. Keep that
		// session and create a separate Aeron instead of rewriting it.
		id = ""
	}
	cfg, err := invocationConfig(w.sessionCfg, InvocationSelection{MainAgent: session.CategoryAeron})
	if err != nil {
		return nil, err
	}
	op, err := w.prepareSessionLifecycle(id, cfg, w.homeDir, true)
	if err != nil {
		return nil, err
	}
	op.createIfMissing = true
	op.rememberRole = session.CategoryAeron
	op.rememberAeron = true
	return &InvocationLifecycleOperation{op: op}, nil
}

// EnsureAeronSession returns the running or saved Aeron for this workspace,
// creating it once when the workspace has none. It does not submit input.
func (w *Workspace) EnsureAeronSession() *NewSessionResult {
	op, err := w.PrepareAeronSession()
	if err != nil {
		return &NewSessionResult{Error: err.Error(), NeedsAccount: errors.Is(err, session.ErrAccountRequired), NeedsModel: errors.Is(err, session.ErrModelRequired)}
	}
	return w.ApplyAgentSession(op.Run(context.Background()))
}
