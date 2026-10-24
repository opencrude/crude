package remote

import (
	"context"
	"errors"
	"strings"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/fileio"
	"crude/workspace"
)

func (w *Workspace) PreviewWorkspaceDeletion(ctx context.Context, id, projectDir string) (workspace.WorkspaceDeletion, error) {
	var out workspace.WorkspaceDeletion
	err := w.call(ctx, coreprotocol.WorkspaceDeletePreview, coreprotocol.WorkspaceDeletePreviewRequest{ID: id, ProjectDir: projectDir}, &out)
	return out, err
}

func (w *Workspace) DeleteSavedWorkspace(ctx context.Context, plan workspace.WorkspaceDeletion) (workspace.DeletedWorkspace, error) {
	var out coreprotocol.WorkspaceDeletionResult
	err := w.workspaceDeletionCall(ctx, coreprotocol.WorkspaceDelete, coreprotocol.WorkspaceDeleteRequest{Token: plan.Token}, &out)
	if err == nil && out.Warning != "" {
		err = fileio.CommittedWarning(out.Warning)
	}
	return out.Deleted, err
}

func (w *Workspace) RestoreDeletedWorkspace(ctx context.Context, deleted workspace.DeletedWorkspace) error {
	var out coreprotocol.WorkspaceDeletionResult
	if err := w.workspaceDeletionCall(ctx, coreprotocol.WorkspaceRestore, &deleted, &out); err != nil {
		return err
	}
	if out.Warning != "" {
		return fileio.CommittedWarning(out.Warning)
	}
	return nil
}

func (w *Workspace) workspaceDeletionCall(ctx context.Context, method string, input coreprotocol.Value, output coreprotocol.Target) error {
	client, id := w.conn()
	if client == nil {
		return errDisconnected
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(w.ctx, cancel)
	defer stop()
	requestID := randomID()
	_, err := client.CallWith(ctx, id, method, input, output, coreclient.CallOptions{ID: requestID})
	var fault *coreprotocol.Fault
	if errors.As(err, &fault) {
		if strings.HasPrefix(fault.Code, "outcome_") {
			return &coreclient.UncertainError{RequestID: requestID, Cause: fault}
		}
		return fault
	}
	if err != nil {
		return &coreclient.UncertainError{RequestID: requestID, Cause: err}
	}
	return nil
}

func (w *Workspace) WorkspaceDeletionReceipt(ctx context.Context, id string) (coreprotocol.Receipt, error) {
	var out coreprotocol.Receipt
	err := w.call(ctx, coreprotocol.GetReceipt, coreprotocol.ReceiptRequest{RequestID: id}, &out)
	return out, err
}
