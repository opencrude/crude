package remote

import (
	"context"

	"crude/coreprotocol"
	"crude/workspace"
)

// FormatProject runs the core-owned catalog formatters and transactional file
// owner. The shell never reads or writes project files for this command.
func (w *Workspace) FormatProject(ctx context.Context) (workspace.FormatProjectResult, error) {
	var out workspace.FormatProjectResult
	err := w.callCancellable(ctx, coreprotocol.ProjectFormat, &coreprotocol.ProjectFormatRequest{}, &out)
	return out, err
}
