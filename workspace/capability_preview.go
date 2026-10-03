package workspace

import (
	"context"
	"errors"

	captypes "crude/types/capability"
)

func (workspace *Workspace) PreviewCapabilityAcquisition(ctx context.Context, action captypes.Action) (captypes.AcquisitionPreview, error) {
	if action.Operation != "install" && action.Operation != "update" {
		return captypes.AcquisitionPreview{}, errors.New("preview requires install or update")
	}
	if err := workspace.ensureCapabilities(ctx); err != nil {
		return captypes.AcquisitionPreview{}, err
	}
	workspace.capabilityMu.RLock()
	defer workspace.capabilityMu.RUnlock()
	if workspace.Capabilities == nil || workspace.WorkspaceDB == nil {
		return captypes.AcquisitionPreview{}, errors.New("capability workspace owner is closed")
	}
	if err := workspace.Capabilities.Configure(ctx, workspace.LoadAppSettings().Capabilities); err != nil {
		return captypes.AcquisitionPreview{}, err
	}
	return workspace.Capabilities.PreviewAcquisition(ctx, action.ID, action.Scope, action.Operation)
}
