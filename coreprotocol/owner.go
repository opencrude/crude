package coreprotocol

import (
	"crude/fileio"
	"crude/localhistory"
	"crude/types"
	"crude/workspace"
)

// Workspace-owner operations a connected shell requests. Each names its exact
// agent/session target; none depends on which tab a shell has selected.
const (
	AgentYolo             = "agent.yolo"
	AgentPlanMode         = "agent.plan_mode"
	AgentGoal             = "agent.goal"
	AgentAppendMessages   = "agent.append_messages"
	AgentRecover          = "agent.recover"
	AgentRecoverCancel    = "agent.recover_cancel"
	AgentRecoverReject    = "agent.recover_reject"
	AgentStopCommand      = "agent.stop_command"
	AgentCompact          = "agent.compact"
	WorkspaceStartup      = "workspace.startup"
	WorkspaceOverrides    = "workspace.overrides"
	WorkspaceOverridesSet = "workspace.overrides_save"
	ToolLayoutLoad        = "workspace.tool_layout"
	ToolLayoutSave        = "workspace.tool_layout_save"
	HistoryUndoScopes     = "history.undo_scopes"
	GitStagePaths         = "git.stage_paths"
	GitUnstagePaths       = "git.unstage_paths"
	GitCommitMessage      = "git.commit_message"
	GitCommitModel        = "git.commit_model"
	// CancelRequest cancels the requester's own in-flight job.
	CancelRequest = "request.cancel"
)

type CancelRequestPayload struct {
	RequestID string `json:"request_id"`
}

type AgentYoloRequest struct {
	AgentID string `json:"agent_id"`
	Enabled bool   `json:"enabled"`
}

// AgentPlanModeRequest sets Plan mode for the session that owns AgentID.
type AgentPlanModeRequest struct {
	AgentID string `json:"agent_id"`
	Enabled bool   `json:"enabled"`
}

// AgentGoalRequest applies one user action (workspace.GoalSet, …) to the
// goal of the session whose main agent is AgentID.
type AgentGoalRequest struct {
	AgentID   string `json:"agent_id"`
	Action    string `json:"action"`
	Objective string `json:"objective,omitempty"`
}

// AgentGoalResult is the goal after the action; Goal is nil once cleared.
type AgentGoalResult struct {
	Goal  *types.Goal `json:"goal,omitempty"`
	Error string      `json:"error,omitempty"`
}

type AppendMessagesRequest struct {
	AgentID  string              `json:"agent_id"`
	Messages []workspace.Message `json:"messages"`
	Stopped  bool                `json:"stopped,omitempty"`
}

// RecoveryRequest fences an exact recovery incident and runtime generation.
type RecoveryRequest struct {
	AgentID    string `json:"agent_id"`
	IncidentID string `json:"incident_id"`
	Generation uint64 `json:"generation"`
}

type StopCommandRequest struct {
	AgentID string             `json:"agent_id"`
	Ref     fileio.AsyncCmdRef `json:"ref"`
}

type AgentIDRequest struct {
	AgentID string `json:"agent_id"`
}

// BoolResult reports whether the exact target accepted the operation.
type BoolResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// StartupResult transfers pending startup diagnostics exactly once.
type StartupResult struct {
	Issues        []types.OperationIssue `json:"issues,omitempty"`
	RegistryError string                 `json:"registry_error,omitempty"`
}

type OverridesResult struct {
	Overrides     types.WorkspaceOverrides `json:"overrides"`
	AgentDefaults types.AgentsSettings     `json:"agent_defaults"`
}

type ToolLayoutRequest struct {
	Layout workspace.ToolLayout `json:"layout"`
}

type UndoScopesRequest struct {
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"`
}

type UndoScopesResult struct {
	Scopes []localhistory.MessageScope `json:"scopes"`
}

type GitPathsRequest struct {
	Paths []string `json:"paths"`
}

type CommitMessageRequest struct {
	StageAll bool `json:"stage_all"`
}

type CommitMessageResult struct {
	Message string `json:"message"`
}

type CommitModelResult struct {
	Model string `json:"model"`
}
