package remote

import (
	"context"
	"errors"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/workspace"
)

var _ shellport.HistorySafety = (*Workspace)(nil)

func (w *Workspace) ListHistorySafety(ctx context.Context, in workspace.HistorySafetyListRequest) (workspace.HistorySafetyPage, error) {
	var out coreprotocol.HistorySafetyReply
	err := w.callCancellable(ctx, coreprotocol.HistorySafetyList, &coreprotocol.HistorySafetyListRequest{
		Cursor: in.Cursor, Limit: in.Limit,
	}, &out)
	return out.Page, err
}

func (w *Workspace) InspectHistorySafety(ctx context.Context, reference string) (workspace.HistorySafetyDetail, error) {
	var out coreprotocol.HistorySafetyReply
	err := w.callCancellable(ctx, coreprotocol.HistorySafetyInspect, &coreprotocol.HistorySafetyInspectRequest{Reference: reference}, &out)
	return out.Detail, err
}

func (w *Workspace) ChangeHistorySafetyProtection(ctx context.Context, in workspace.HistorySafetyChangeRequest) (workspace.HistorySafetyChangeResult, error) {
	var out coreprotocol.HistorySafetyReply
	err := w.callCancellable(ctx, coreprotocol.HistorySafetyChange, &coreprotocol.HistorySafetyChangeRequest{Consent: in}, &out)
	return out.Result, err
}

func (w *Workspace) HistorySafetyReceipt(ctx context.Context, id string) (coreprotocol.Receipt, error) {
	var out coreprotocol.Receipt
	err := w.callCancellable(ctx, coreprotocol.GetReceipt, &coreprotocol.ReceiptRequest{RequestID: id}, &out)
	if err == nil && (out.RequestID != id || out.WorkspaceID != w.id || out.Method != coreprotocol.HistorySafetyChange) {
		err = errors.New("original safety disposition receipt identity does not match this workspace")
	}
	return out, err
}
