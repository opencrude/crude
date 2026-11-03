package remote

import (
	"context"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
)

var _ shellport.CodeTools = (*Workspace)(nil)

func (w *Workspace) ReadCodeToolInventory(ctx context.Context, offset, limit int) (types.CodeToolInventory, error) {
	var out coreprotocol.ReadCodeToolInventoryResult
	err := w.callCancellable(ctx, coreprotocol.ReadCodeToolInventory, &coreprotocol.ReadCodeToolInventoryRequest{Offset: offset, Limit: limit}, &out)
	return out.Inventory, err
}

func (w *Workspace) PreviewCodeToolRemoval(ctx context.Context, ids []string, unused bool) (types.CodeToolRemovalPreview, error) {
	var out coreprotocol.PreviewCodeToolRemovalResult
	err := w.callCancellable(ctx, coreprotocol.PreviewCodeToolRemoval, &coreprotocol.PreviewCodeToolRemovalRequest{IDs: ids, Unused: unused}, &out)
	return out.Preview, err
}

func (w *Workspace) RemoveCodeTools(ctx context.Context, preview types.CodeToolRemovalPreview) error {
	var out coreprotocol.RemoveCodeToolsResult
	return w.callCancellable(ctx, coreprotocol.RemoveCodeTools, &coreprotocol.RemoveCodeToolsRequest{Preview: preview}, &out)
}
