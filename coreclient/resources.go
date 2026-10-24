package coreclient

import (
	"context"

	"crude/coreprotocol"
	"crude/document"
	"crude/terminalvt"
	"crude/workspace"
)

func (v *WorkspaceView) OpenDocument(ctx context.Context, path string) (document.Snapshot, error) {
	var out document.Snapshot
	err := v.Client.Call(ctx, v.ID, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}, &out)
	return out, err
}
func (v *WorkspaceView) ReadDocument(ctx context.Context, id string) (document.Snapshot, error) {
	var out document.Snapshot
	err := v.Client.Call(ctx, v.ID, coreprotocol.DocumentRead, coreprotocol.DocumentReadRequest{ID: id}, &out)
	return out, err
}
func (v *WorkspaceView) EditDocument(ctx context.Context, input coreprotocol.DocumentEditRequest) (document.Receipt, error) {
	var out document.Receipt
	err := v.Client.Call(ctx, v.ID, coreprotocol.DocumentEdit, &input, &out)
	return out, err
}
func (v *WorkspaceView) SaveDocument(ctx context.Context, id string, revision uint64, reason workspace.DocumentSaveReason) (document.Snapshot, error) {
	var out document.Snapshot
	err := v.Client.Call(ctx, v.ID, coreprotocol.DocumentSave, &coreprotocol.DocumentRevisionRequest{ID: id, Revision: revision, Reason: reason}, &out)
	return out, err
}
func (v *WorkspaceView) TerminalRows(ctx context.Context, id string, request terminalvt.RowRequest) (workspace.TerminalProjection, error) {
	var out workspace.TerminalProjection
	err := v.Client.Call(ctx, v.ID, coreprotocol.TerminalRows, &coreprotocol.TerminalRowsRequest{ID: id, Start: request.Start, Count: request.Count, ViewportStart: request.ViewportStart, HistoryEpoch: request.HistoryEpoch, HistoryBase: request.HistoryBase}, &out)
	return out, err
}
func (v *WorkspaceView) CreateTerminal(ctx context.Context, opts workspace.TerminalCreateOpts) (workspace.TerminalInfo, error) {
	var out workspace.TerminalInfo
	err := v.Client.Call(ctx, v.ID, coreprotocol.TerminalCreate, &opts, &out)
	return out, err
}
func (v *WorkspaceView) ControlTerminal(ctx context.Context, id string) (workspace.TerminalLease, error) {
	var out workspace.TerminalLease
	err := v.Client.Call(ctx, v.ID, coreprotocol.TerminalControl, coreprotocol.TerminalIDRequest{ID: id}, &out)
	return out, err
}
