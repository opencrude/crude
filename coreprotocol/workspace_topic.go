package coreprotocol

import (
	"crude/types"
	"crude/workspace"
	"time"
)

// ConversationUpdate carries only transcript changes. Agent metadata is sent
// separately when it changes, not repeated for every text suffix.
type ConversationUpdate struct {
	AgentID       string
	LastTokenTime time.Time
	Delta         workspace.ConversationDelta
}

// WorkspaceUpdate is one atomic publication from the retained workspace. A
// snapshot is only a boundary/reset; ordinary publications contain changed data,
// never instructions to fetch it in a chain of round trips.
type WorkspaceUpdate struct {
	Snapshot        *WorkspaceSnapshot
	Changes         AgentChangesResult
	Conversations   []ConversationUpdate
	Sessions        *SessionSnapshotList
	Files           []FileChange
	Reductions      []workspace.AgentReduction
	Notices         []workspace.AgentNotice
	TerminalEffects []workspace.TerminalEffectsMsg
}

// WorkspaceState is a conflated state-topic value, independently sequenced
// from the workspace log. Exactly one projection is present for its topic.
type WorkspaceState struct {
	Registry      *SessionRegistry
	Settings      *SettingsFetchResult
	AgentDefaults *types.AgentsSettings
	Status        *StatusSnapshot
	LSP           *LSPSnapshot
	Terminals     *TerminalInfos
	Git           *GitSnapshotResult
	Board         *BoardSnapshotResult
	Fault         *Fault
}

const (
	TopicStatus            = "state.status"
	TopicSettings          = "state.settings"
	TopicLSP               = "state.lsp"
	TopicTerminalInventory = "state.terminals"
	TopicRegistry          = "state.registry"
	TopicGit               = "state.git"
	TopicBoard             = "state.board"
)
