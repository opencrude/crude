package remote

import (
	"context"

	"crude/core/coreprotocol"
	"crude/core/shellport"
	"crude/types/insight"
)

var _ shellport.CodeTools = (*Workspace)(nil)

// ReadCodeToolInventory returns a page of the code tools that the workspace has found.
func (w *Workspace) ReadCodeToolInventory(ctx context.Context, offset, limit int) (insight.CodeToolInventory, error) {
	var out coreprotocol.ReadCodeToolInventoryResult
	err := w.callCancellable(ctx, coreprotocol.ReadCodeToolInventory, &coreprotocol.ReadCodeToolInventoryRequest{Offset: offset, Limit: limit}, &out)
	return out.Inventory, err
}

// PreviewCodeToolRemoval reports what removing code tools would delete.
func (w *Workspace) PreviewCodeToolRemoval(ctx context.Context, ids []string, unused bool) (insight.CodeToolRemovalPreview, error) {
	var out coreprotocol.PreviewCodeToolRemovalResult
	err := w.callCancellable(ctx, coreprotocol.PreviewCodeToolRemoval, &coreprotocol.PreviewCodeToolRemovalRequest{IDs: ids, Unused: unused}, &out)
	return out.Preview, err
}

// RemoveCodeTools removes the code tools of a reviewed preview.
func (w *Workspace) RemoveCodeTools(ctx context.Context, preview insight.CodeToolRemovalPreview) error {
	var out coreprotocol.RemoveCodeToolsResult
	return w.callCancellable(ctx, coreprotocol.RemoveCodeTools, &coreprotocol.RemoveCodeToolsRequest{Preview: preview}, &out)
}
