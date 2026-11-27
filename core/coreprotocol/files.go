package coreprotocol

import (
	"crude/workspace"
	"crude/workspace/fileio"
	"crude/workspace/localhistory"
)

const (
	FilesBrowse            = "files.browse"
	FilesCatalog           = "files.catalog"
	FilesRead              = "files.read"
	FilesCreate            = "files.create"
	FilesCreateFolder      = "files.create_folder"
	FilesRename            = "files.rename"
	FilesCopy              = "files.copy"
	FilesDelete            = "files.delete"
	FilesEdit              = "files.edit"
	FilesSearch            = "files.search"
	HistoryAgentReceipts   = "history.agent_receipts"
	HistoryTimeline        = "history.timeline"
	HistoryFile            = "history.file"
	HistoryStats           = "history.stats"
	HistoryPlan            = "history.plan"
	HistoryPlanPaths       = "history.plan_paths"
	HistoryPlanMessage     = "history.plan_message"
	HistoryPlanAgent       = "history.plan_agent"
	HistoryApply           = "history.apply"
	HistoryVerify          = "history.verify"
	HistoryPrune           = "history.prune"
	HistoryCapture         = "history.capture"
	SavedWorkspaces        = "files.saved_workspaces"
	SavedWorkspaceActivity = "files.saved_workspace_activity"
)

// WorkspaceActivities is a retained-only read keyed by the catalog's stable
// workspace IDs. Absent IDs were not observed, not measured at zero.
type WorkspaceActivities map[string]workspace.Activity

const MaxFileChunk = 256 << 10
const MaxFileRead = 64 << 20

type FilePathRequest struct {
	Path    string `json:"path"`
	NewPath string `json:"new_path,omitempty"`
}
type FileCatalogRequest struct {
	ExecutionID     string `json:"execution_id,omitempty"`
	DirectoriesOnly bool   `json:"directories_only"`
	IncludeHidden   bool   `json:"include_hidden"`
}

// Continuation must echo the digest and offset of the same workspace path.
type FileReadRequest struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
	Limit  int    `json:"limit"`
	Digest string `json:"digest,omitempty"`
}
type FileChunk struct {
	Path       string `json:"path"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
	Total      int64  `json:"total"`
	Digest     string `json:"digest"`
	Data       []byte `json:"data"`
	EOF        bool   `json:"eof"`
}
type FileEditsRequest struct {
	Edits []workspace.FileEdit `json:"edits"`
}

// Errors are strings on the wire, reconstructed as FileOperationResultMsg by the client.
type FileOperationResult struct {
	Op      workspace.FileOperation `json:"op"`
	Path    string                  `json:"path"`
	OldPath string                  `json:"old_path"`
	NewPath string                  `json:"new_path"`
	IsDir   bool                    `json:"is_dir"`
	Error   string                  `json:"error,omitempty"`
	Detail  string                  `json:"detail"`
}
type FileSearchRequest struct {
	Query   string                      `json:"query"`
	Options fileio.ProjectSearchOptions `json:"options"`
}
type FileSearchResult struct {
	Matches []fileio.ProjectSearchMatch `json:"matches"`
}

// AgentReceiptRequest selects an agent in this session and checkout.
type AgentReceiptRequest struct {
	Query localhistory.AgentReceiptQuery
}

// AgentReceiptResult carries one page or one exact retained diff.
type AgentReceiptResult struct{ Page localhistory.RunChangePage }

type HistoryListRequest struct {
	Path  string `json:"path,omitempty"`
	Limit int    `json:"limit"`
}
type HistoryPlanRequest struct {
	SnapshotID string                       `json:"snapshot_id,omitempty"`
	SessionID  string                       `json:"session_id,omitempty"`
	MessageID  string                       `json:"message_id,omitempty"`
	AgentID    string                       `json:"agent_id,omitempty"`
	Paths      []string                     `json:"paths,omitempty"`
	Options    localhistory.SnapshotOptions `json:"options"`
}
type HistoryApplyRequest struct {
	PlanID  string                       `json:"plan_id"`
	Choices []localhistory.RestoreChoice `json:"choices"`
}
type HistoryVerifyRequest struct {
	SnapshotID string `json:"snapshot_id,omitempty"`
}
type HistoryPruneRequest struct {
	Policy localhistory.RetentionPolicy `json:"policy"`
}
type HistoryCaptureRequest struct {
	Options localhistory.SnapshotOptions `json:"options"`
}
