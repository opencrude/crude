package workspace

import (
	"fmt"
	"sync"

	"crude/session"
	"crude/types"
)

// SessionSettingsOperation captures an exact backend while the workspace owner
// is serialized. Run performs I/O without reading workspace maps or mutating
// frontend state. A repeated Run returns the same result, never a second toggle.
type SessionSettingsOperation struct {
	owner   *Session
	backend *session.Session
	form    *types.Settings
	reset   bool
	// capability turns one skill/MCP server on or off for the session.
	capability *sessionCapabilityChange
	// remote performs the operation in a core when the owner is a replica.
	remote func() (session.SettingsSnapshotSet, error)
	once   sync.Once
	result SessionSettingsResult
}

type SessionSettingsResult struct {
	Snapshot session.SettingsSnapshotSet
	Err      error
	owner    *Session
}

func (w *Workspace) PrepareSessionSettingsSave(sessionID string, settings types.Settings) (*SessionSettingsOperation, error) {
	operation, err := w.prepareSessionSettings(sessionID)
	if err != nil {
		return nil, err
	}
	copy := cloneSettings(settings)
	operation.form = &copy
	return operation, nil
}

func (w *Workspace) PrepareMultiAgentToggle(sessionID string) (*SessionSettingsOperation, error) {
	return w.prepareSessionSettings(sessionID)
}

type sessionCapabilityChange struct {
	id      string
	enabled bool
}

// PrepareSessionCapabilityToggle turns one installed skill or MCP server on or
// off for the session and all its agents. It is an idempotent set, not a flip,
// so it needs no revision fence.
func (w *Workspace) PrepareSessionCapabilityToggle(sessionID, id string, enabled bool) (*SessionSettingsOperation, error) {
	op, err := w.prepareSessionSettings(sessionID)
	if err == nil {
		op.capability = &sessionCapabilityChange{id: id, enabled: enabled}
	}
	return op, err
}

func (w *Workspace) PrepareSessionSettingsReset(sessionID string) (*SessionSettingsOperation, error) {
	op, err := w.prepareSessionSettings(sessionID)
	if err == nil {
		op.reset = true
	}
	return op, err
}

func (w *Workspace) prepareSessionSettings(sessionID string) (*SessionSettingsOperation, error) {
	if w == nil || sessionID == "" {
		return nil, fmt.Errorf("session settings owner is unavailable")
	}
	owner := w.SessionsByID[sessionID]
	if owner == nil || owner.backend == nil {
		return nil, fmt.Errorf("session %q is not open", sessionID)
	}
	return &SessionSettingsOperation{owner: owner, backend: owner.backend}, nil
}

func (operation *SessionSettingsOperation) Run() SessionSettingsResult {
	if operation == nil {
		return SessionSettingsResult{Err: fmt.Errorf("session settings operation is unavailable")}
	}
	operation.once.Do(func() {
		operation.result.owner = operation.owner
		if operation.remote != nil {
			operation.result.Snapshot, operation.result.Err = operation.remote()
		} else if operation.capability != nil {
			operation.result.Snapshot, operation.result.Err = operation.backend.SetCapabilityEnabled(operation.capability.id, operation.capability.enabled)
		} else if operation.reset {
			operation.result.Snapshot, operation.result.Err = operation.backend.ResetSettingsSnapshot()
		} else if operation.form == nil {
			operation.result.Snapshot, operation.result.Err = operation.backend.ToggleMultiAgent()
		} else {
			operation.result.Snapshot, operation.result.Err = operation.backend.SaveSettingsSnapshot(*operation.form)
		}
	})
	result := operation.result
	result.Snapshot.Effective = cloneSettings(result.Snapshot.Effective)
	result.Snapshot.Base = cloneSettings(result.Snapshot.Base)
	result.Snapshot.Overrides = copyCategoryMap(result.Snapshot.Overrides)
	return result
}

// ApplySessionSettingsResult runs only on the workspace owner, after its caller
// checks the attachment generation. Replacement owners and old revisions are
// rejected; the background operation never writes this projection.
func (w *Workspace) ApplySessionSettingsResult(result SessionSettingsResult) bool {
	if w == nil || result.Err != nil || result.owner == nil ||
		w.SessionsByID[result.owner.SessionID] != result.owner {
		return false
	}
	if !result.owner.installSettings(result.Snapshot) {
		return false
	}
	if w.AgentMgr != nil {
		for _, node := range w.AgentMgr.Orchestrators() {
			if w.SessionForAgent(node.ID) == result.owner {
				node.Model = result.Snapshot.Effective.Model
			}
		}
	}
	w.agentRevision++
	return true
}

func (owner *Session) installSettings(snapshot session.SettingsSnapshotSet) bool {
	if owner == nil || snapshot.Revision < owner.settingsRevision {
		return false
	}
	owner.Settings = NewSessionSettingsState(snapshot.Effective, snapshot.Base, snapshot.Overrides)
	owner.settingsRevision = snapshot.Revision
	return true
}

// NewReplicaSessionSettingsOperation wraps a core request for a connected
// shell's replica session. Its result is applied with ApplyReplicaSettings.
func NewReplicaSessionSettingsOperation(owner *Session, run func() (session.SettingsSnapshotSet, error)) *SessionSettingsOperation {
	return &SessionSettingsOperation{owner: owner, remote: run}
}

// ResultOwner is the replica session an operation result belongs to.
func (result SessionSettingsResult) ResultOwner() *Session { return result.owner }

// InstallSettingsSnapshot installs a newer redacted settings set on a replica
// session; older revisions are rejected.
func (owner *Session) InstallSettingsSnapshot(snapshot session.SettingsSnapshotSet) bool {
	return owner.installSettings(snapshot)
}

// SettingsRevision is an observation generation for replica publication.
func (owner *Session) SettingsRevision() uint64 {
	if owner == nil {
		return 0
	}
	return owner.settingsRevision
}
