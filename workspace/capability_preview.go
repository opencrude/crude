package workspace

import (
	"context"
	"errors"

	captypes "crude/types/capability"
)

// PreviewCapabilityAcquisition previews an install or update of one capability.
func (w *Workspace) PreviewCapabilityAcquisition(ctx context.Context, action captypes.Action) (captypes.AcquisitionPreview, error) {
	if action.Operation != "install" && action.Operation != "update" {
		return captypes.AcquisitionPreview{}, errors.New("preview requires install or update")
	}
	if err := w.ensureCapabilities(ctx); err != nil {
		return captypes.AcquisitionPreview{}, err
	}
	w.capabilityMu.RLock()
	defer w.capabilityMu.RUnlock()
	if w.Capabilities == nil || w.WorkspaceDB == nil {
		return captypes.AcquisitionPreview{}, errors.New("capability workspace owner is closed")
	}
	if err := w.Capabilities.Configure(ctx, w.LoadAppSettings().Capabilities); err != nil {
		return captypes.AcquisitionPreview{}, err
	}
	return w.Capabilities.PreviewAcquisition(ctx, action.ID, action.Scope, action.Operation)
}
