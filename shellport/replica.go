package shellport

import (
	"context"

	"crude/workspace"
)

// SettingsChangedMsg asks the shell to refresh presentation from installed
// settings; it carries no credentials or persistence authority.
type SettingsChangedMsg struct{}

// GitSnapshotChangedMsg publishes an installed Git topic to shell projections.
// Its attachment token prevents a queued old workspace from changing the UI.
type GitSnapshotChangedMsg struct {
	Owner    Workspace
	Snapshot workspace.GitSnapshot
}

// ReplicaFailureMsg reports a queued mutation failure on its exact attachment.
type ReplicaFailureMsg struct {
	Owner     Workspace
	Operation string
	Error     string
}

// Connection owns one client attachment, not the retained core workspace.
// A replacement is prepared off the UI thread before the old one is detached.
type Connection interface {
	WorkspaceID() string
	AttachWorkspace(context.Context, string) (Workspace, error)
	Close() error
}

// ReplicaChangesMsg is posted into the shell's FIFO by a connected workspace.
// Its batch is opaque to the shell: only the workspace that posted it can
// apply it, on the shell's Update goroutine, which is the replica's sole owner.
type ReplicaChangesMsg struct {
	Owner Workspace
	Batch any
}

// ReplicaApplied is what a shell presents after a batch was installed. The
// reductions were decided once by the core; the shell never re-runs them.
type ReplicaApplied struct {
	// Attached lists root agents to surface as tabs after an attach or resync,
	// in creation order.
	Attached []string
	// Initial permits the first attachment to select a root; subsequent roots
	// must not steal an independently selected editor or agent.
	Initial         bool
	SessionsChanged bool
	Reductions      []workspace.AgentReduction
	Notices         []workspace.AgentNotice
	// Follow are existing shell messages (terminal, status, history events)
	// to dispatch after the replica reflects the batch.
	Follow []any
}

// Replica is implemented by connected workspaces whose state is a replica of
// a core. Local workspaces reduce their own events and do not implement it.
type Replica interface {
	// StartReplica starts FIFO delivery after the shell has installed its sink.
	StartReplica()
	ApplyReplicaChanges(ReplicaChangesMsg) ReplicaApplied
}
