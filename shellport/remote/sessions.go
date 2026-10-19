package remote

import (
	"context"
	"errors"
	"slices"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/session"
	"crude/sessiondb"
	"crude/types"
	"crude/workspace"
)

func (w *Workspace) Sessions() []*workspace.Session { return w.view.Sessions }
func (w *Workspace) SessionsByID() map[string]*workspace.Session {
	return w.view.SessionsByID
}
func (w *Workspace) SessionsByAgent() map[string]*workspace.Session {
	return w.view.SessionsByAgent
}
func (w *Workspace) SessionForAgent(agentID string) *workspace.Session {
	if owner := w.view.SessionForAgent(agentID); owner != nil {
		return owner
	}
	if root := w.view.AgentMgr.OrchestratorFor(agentID); root != nil {
		return w.view.SessionForAgent(root.ID)
	}
	return nil
}

func (w *Workspace) ListSessions() []sessiondb.SessionMeta {
	w.registryMu.RLock()
	defer w.registryMu.RUnlock()
	return slices.Clone(w.registry)
}

// lifecycle creates or opens a session in the core. The new root reaches this
// shell's replica through the ordered pump; the shell activates it then.
func (w *Workspace) lifecycle(method string, payload any) *workspace.NewSessionResult {
	ctx, cancel := context.WithTimeout(context.Background(), 2*callTimeout)
	defer cancel()
	var result workspace.NewSessionResult
	if err := w.call(ctx, method, payload, &result); err != nil {
		var fault *coreprotocol.Fault
		if errors.As(err, &fault) {
			result.Error = fault.Message
		} else {
			result.Error = err.Error()
		}
		return &result
	}
	w.pending.add(coreprotocol.Change{Sessions: true, Agents: []string{result.OrchID}})
	return &result
}

func (w *Workspace) NewAgentSession() *workspace.NewSessionResult {
	return w.lifecycle(coreprotocol.NewSession, coreprotocol.InvocationSessionRequest{Selection: w.invocation})
}

func (w *Workspace) OpenAgentSession(sessionID string) *workspace.NewSessionResult {
	return w.lifecycle(coreprotocol.OpenSession, coreprotocol.InvocationSessionRequest{SessionID: sessionID, Selection: w.invocation})
}

// BackendSession is never available to a connected shell; the core owns it.
func (w *Workspace) BackendSession(string) *session.Session { return nil }

func (w *Workspace) SessionConnectionIssue(sessionID string) error {
	if issue := w.view.SessionConnectionIssue(sessionID); issue != "" {
		return errors.New(issue)
	}
	return nil
}

func (w *Workspace) settingsOperation(sessionID string, run func(ctx context.Context, rpc *coreclient.WorkspaceView) (coreprotocol.SessionSettingsResult, error)) (*workspace.SessionSettingsOperation, error) {
	owner := w.view.SessionsByID[sessionID]
	if owner == nil {
		return nil, errors.New("session is not open")
	}
	rpc := w.rpc()
	return workspace.NewReplicaSessionSettingsOperation(owner, func() (session.SettingsSnapshotSet, error) {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		result, err := run(ctx, rpc)
		if err != nil {
			return session.SettingsSnapshotSet{}, err
		}
		return coreclient.SettingsSnapshot(result), nil
	}), nil
}

func (w *Workspace) PrepareMultiAgentToggle(sessionID string) (*workspace.SessionSettingsOperation, error) {
	return w.settingsOperation(sessionID, func(ctx context.Context, rpc *coreclient.WorkspaceView) (coreprotocol.SessionSettingsResult, error) {
		return rpc.ToggleSessionMultiAgent(ctx, coreprotocol.SessionSettingsMutationRequest{SessionID: sessionID})
	})
}

func (w *Workspace) PrepareSessionCapabilityToggle(sessionID, id string, enabled bool) (*workspace.SessionSettingsOperation, error) {
	return w.settingsOperation(sessionID, func(ctx context.Context, rpc *coreclient.WorkspaceView) (coreprotocol.SessionSettingsResult, error) {
		return rpc.ToggleSessionCapability(ctx, coreprotocol.SessionSettingsMutationRequest{SessionID: sessionID, Capability: &coreprotocol.SessionCapabilityChange{ID: id, Enabled: enabled}})
	})
}

func (w *Workspace) PrepareSessionSettingsSave(sessionID string, settings types.Settings) (*workspace.SessionSettingsOperation, error) {
	return w.settingsOperation(sessionID, func(ctx context.Context, rpc *coreclient.WorkspaceView) (coreprotocol.SessionSettingsResult, error) {
		return rpc.SaveSessionSettings(ctx, coreprotocol.SessionSettingsMutationRequest{SessionID: sessionID, Edits: settings.SessionEdits})
	})
}

func (w *Workspace) PrepareSessionSettingsReset(sessionID string) (*workspace.SessionSettingsOperation, error) {
	return w.settingsOperation(sessionID, func(ctx context.Context, rpc *coreclient.WorkspaceView) (coreprotocol.SessionSettingsResult, error) {
		return rpc.ResetSessionSettings(ctx, coreprotocol.SessionSettingsMutationRequest{SessionID: sessionID})
	})
}

func (w *Workspace) ApplySessionSettingsResult(result workspace.SessionSettingsResult) bool {
	owner := result.ResultOwner()
	if result.Err != nil || owner == nil || w.view.SessionsByID[owner.SessionID] != owner {
		return false
	}
	if !owner.InstallSettingsSnapshot(result.Snapshot) {
		return false
	}
	for _, node := range w.view.AgentMgr.Orchestrators() {
		if w.view.SessionForAgent(node.ID) == owner {
			node.Model = result.Snapshot.Effective.Model
		}
	}
	return true
}
