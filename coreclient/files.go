package coreclient

import (
	"context"
	"crude/coreprotocol"
	"crude/fileio"
	"crude/localhistory"
	"crude/workspace"
	"errors"
)

func (v *WorkspaceView) BrowseDirectory(ctx context.Context, path string) (workspace.DirectoryListing, error) {
	var out workspace.DirectoryListing
	err := v.Client.Call(ctx, v.ID, coreprotocol.FilesBrowse, coreprotocol.FilePathRequest{Path: path}, &out)
	return out, err
}
func (v *WorkspaceView) ScanBrowserPaths(ctx context.Context, directoriesOnly, includeHidden bool) (workspace.BrowserCatalog, error) {
	var out workspace.BrowserCatalog
	err := v.Client.Call(ctx, v.ID, coreprotocol.FilesCatalog, coreprotocol.FileCatalogRequest{DirectoriesOnly: directoriesOnly, IncludeHidden: includeHidden}, &out)
	return out, err
}
func (v *WorkspaceView) ReadFileChunk(ctx context.Context, in coreprotocol.FileReadRequest) (coreprotocol.FileChunk, error) {
	var out coreprotocol.FileChunk
	err := v.Client.Call(ctx, v.ID, coreprotocol.FilesRead, in, &out)
	return out, err
}
func (v *WorkspaceView) fileOperation(ctx context.Context, method string, in any) workspace.FileOperationResultMsg {
	var out coreprotocol.FileOperationResult
	err := v.Client.Call(ctx, v.ID, method, in, &out)
	if err == nil && out.Error != "" {
		err = errors.New(out.Error)
	}
	return workspace.FileOperationResultMsg{Op: out.Op, Path: out.Path, OldPath: out.OldPath, NewPath: out.NewPath, IsDir: out.IsDir, Err: err, Detail: out.Detail}
}
func (v *WorkspaceView) CreateFile(ctx context.Context, path string) workspace.FileOperationResultMsg {
	return v.fileOperation(ctx, coreprotocol.FilesCreate, coreprotocol.FilePathRequest{Path: path})
}
func (v *WorkspaceView) CreateFolder(ctx context.Context, path string) workspace.FileOperationResultMsg {
	return v.fileOperation(ctx, coreprotocol.FilesCreateFolder, coreprotocol.FilePathRequest{Path: path})
}
func (v *WorkspaceView) RenamePath(ctx context.Context, path, target string) workspace.FileOperationResultMsg {
	return v.fileOperation(ctx, coreprotocol.FilesRename, coreprotocol.FilePathRequest{Path: path, NewPath: target})
}
func (v *WorkspaceView) DeletePath(ctx context.Context, path string) workspace.FileOperationResultMsg {
	return v.fileOperation(ctx, coreprotocol.FilesDelete, coreprotocol.FilePathRequest{Path: path})
}
func (v *WorkspaceView) ApplyWorkspaceEdit(ctx context.Context, edits []workspace.FileEdit) workspace.FileOperationResultMsg {
	return v.fileOperation(ctx, coreprotocol.FilesEdit, coreprotocol.FileEditsRequest{Edits: edits})
}
func (v *WorkspaceView) SearchProject(ctx context.Context, query string, opts fileio.ProjectSearchOptions) ([]fileio.ProjectSearchMatch, error) {
	var out coreprotocol.FileSearchResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.FilesSearch, coreprotocol.FileSearchRequest{Query: query, Options: opts}, &out)
	return out.Matches, err
}
func (v *WorkspaceView) ListSavedWorkspaces(ctx context.Context) ([]workspace.WorkspaceInfo, error) {
	var out []workspace.WorkspaceInfo
	err := v.Client.Call(ctx, v.ID, coreprotocol.SavedWorkspaces, struct{}{}, &out)
	return out, err
}

// HistoryService is a transport facade, never a storage owner.
type HistoryService struct{ view *WorkspaceView }

func (v *WorkspaceView) History() *HistoryService { return &HistoryService{view: v} }
func (h *HistoryService) Timeline(ctx context.Context, limit int) ([]localhistory.SnapshotRecord, error) {
	var out []localhistory.SnapshotRecord
	err := h.view.Client.Call(ctx, h.view.ID, coreprotocol.HistoryTimeline, coreprotocol.HistoryListRequest{Limit: limit}, &out)
	return out, err
}
func (h *HistoryService) FileHistory(ctx context.Context, path string, limit int) ([]localhistory.SnapshotRecord, error) {
	var out []localhistory.SnapshotRecord
	err := h.view.Client.Call(ctx, h.view.ID, coreprotocol.HistoryFile, coreprotocol.HistoryListRequest{Path: path, Limit: limit}, &out)
	return out, err
}
func (h *HistoryService) Stats(ctx context.Context) (localhistory.Stats, error) {
	var out localhistory.Stats
	err := h.view.Client.Call(ctx, h.view.ID, coreprotocol.HistoryStats, struct{}{}, &out)
	return out, err
}
func (h *HistoryService) plan(ctx context.Context, method string, in coreprotocol.HistoryPlanRequest) (localhistory.RestorePlan, error) {
	var out localhistory.RestorePlan
	err := h.view.Client.Call(ctx, h.view.ID, method, in, &out)
	return out, err
}
func (h *HistoryService) PlanRestore(ctx context.Context, id string, opts localhistory.SnapshotOptions) (localhistory.RestorePlan, error) {
	return h.plan(ctx, coreprotocol.HistoryPlan, coreprotocol.HistoryPlanRequest{SnapshotID: id, Options: opts})
}
func (h *HistoryService) PlanRestorePaths(ctx context.Context, id string, opts localhistory.SnapshotOptions, paths []string) (localhistory.RestorePlan, error) {
	return h.plan(ctx, coreprotocol.HistoryPlanPaths, coreprotocol.HistoryPlanRequest{SnapshotID: id, Options: opts, Paths: paths})
}
func (h *HistoryService) PlanMessageRestore(ctx context.Context, sessionID, messageID string) (localhistory.RestorePlan, error) {
	return h.plan(ctx, coreprotocol.HistoryPlanMessage, coreprotocol.HistoryPlanRequest{SessionID: sessionID, MessageID: messageID})
}
func (h *HistoryService) PlanAgentRestore(ctx context.Context, sessionID, messageID, agentID string) (localhistory.RestorePlan, error) {
	return h.plan(ctx, coreprotocol.HistoryPlanAgent, coreprotocol.HistoryPlanRequest{SessionID: sessionID, MessageID: messageID, AgentID: agentID})
}
func (h *HistoryService) ApplyRestoreChoices(ctx context.Context, id string, choices []localhistory.RestoreChoice) (localhistory.RestoreResult, error) {
	var out localhistory.RestoreResult
	err := h.view.Client.Call(ctx, h.view.ID, coreprotocol.HistoryApply, coreprotocol.HistoryApplyRequest{PlanID: id, Choices: choices}, &out)
	return out, err
}
func (h *HistoryService) ApplyRestore(ctx context.Context, id string, paths []string) (localhistory.RestoreResult, error) {
	choices := make([]localhistory.RestoreChoice, len(paths))
	for i, path := range paths {
		choices[i].Path = path
	}
	return h.ApplyRestoreChoices(ctx, id, choices)
}
func (h *HistoryService) Verify(ctx context.Context, id string) error {
	return h.view.Client.Call(ctx, h.view.ID, coreprotocol.HistoryVerify, coreprotocol.HistoryVerifyRequest{SnapshotID: id}, nil)
}
func (h *HistoryService) VerifyAll(ctx context.Context) error { return h.Verify(ctx, "") }
func (h *HistoryService) Prune(ctx context.Context, policy localhistory.RetentionPolicy) (localhistory.PruneResult, error) {
	var out localhistory.PruneResult
	err := h.view.Client.Call(ctx, h.view.ID, coreprotocol.HistoryPrune, coreprotocol.HistoryPruneRequest{Policy: policy}, &out)
	return out, err
}
func (h *HistoryService) Capture(ctx context.Context, opts localhistory.SnapshotOptions) (localhistory.Manifest, error) {
	var out localhistory.Manifest
	err := h.view.Client.Call(ctx, h.view.ID, coreprotocol.HistoryCapture, coreprotocol.HistoryCaptureRequest{Options: opts}, &out)
	return out, err
}
