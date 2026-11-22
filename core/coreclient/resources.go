package coreclient

import (
	"context"
	"errors"
	"fmt"

	"crude/core/coreprotocol"
	"crude/internal/terminalvt"
	"crude/workspace"
	"crude/workspace/document"
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

// ControlTerminal acquires input control as claim allows; refusal unwraps to
// workspace.ErrTerminalControl.
func (v *WorkspaceView) ControlTerminal(ctx context.Context, id string, claim workspace.TerminalClaim) (workspace.TerminalLease, error) {
	var out workspace.TerminalLease
	request := coreprotocol.TerminalIDRequest{ID: id, TakeOver: claim == workspace.ClaimTakeOver, FromAbsent: claim == workspace.ClaimFromAbsent}
	err := v.Client.Call(ctx, v.ID, coreprotocol.TerminalControl, request, &out)
	var fault *coreprotocol.Fault
	if errors.As(err, &fault) && fault.Code == coreprotocol.FaultTerminalControl {
		err = fmt.Errorf("%w: %s", workspace.ErrTerminalControl, fault.Message)
	}
	return out, err
}
