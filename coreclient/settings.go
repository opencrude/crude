package coreclient

import (
	"context"
	"crude/coreprotocol"
)

func (v *WorkspaceView) FetchSettings(ctx context.Context) (coreprotocol.SettingsFetchResult, error) {
	var out coreprotocol.SettingsFetchResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.SettingsFetch, struct{}{}, &out)
	return out, err
}
func (v *WorkspaceView) ReadSessionSettings(ctx context.Context, id string) (coreprotocol.SessionSettingsResult, error) {
	var out coreprotocol.SessionSettingsResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.SessionSettingsRead, coreprotocol.SessionSettingsRequest{SessionID: id}, &out)
	return out, err
}
func (v *WorkspaceView) SaveSessionSettings(ctx context.Context, input coreprotocol.SessionSettingsMutationRequest) (coreprotocol.SessionSettingsResult, error) {
	var out coreprotocol.SessionSettingsResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.SessionSettingsSave, input, &out)
	return out, err
}
func (v *WorkspaceView) ResetSessionSettings(ctx context.Context, input coreprotocol.SessionSettingsMutationRequest) (coreprotocol.SessionSettingsResult, error) {
	var out coreprotocol.SessionSettingsResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.SessionSettingsReset, input, &out)
	return out, err
}
func (v *WorkspaceView) ToggleSessionMultiAgent(ctx context.Context, input coreprotocol.SessionSettingsMutationRequest) (coreprotocol.SessionSettingsResult, error) {
	var out coreprotocol.SessionSettingsResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.SessionMultiAgentToggle, input, &out)
	return out, err
}
func (v *WorkspaceView) ChangeAccount(ctx context.Context, input coreprotocol.AccountChangeRequest) (coreprotocol.AccountView, error) {
	var out coreprotocol.AccountView
	err := v.Client.Call(ctx, v.ID, coreprotocol.AccountChange, input, &out)
	return out, err
}
func (v *WorkspaceView) QueryAccountModels(ctx context.Context, input coreprotocol.AccountModelsRequest) (coreprotocol.AccountModelsResult, error) {
	var out coreprotocol.AccountModelsResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.AccountModels, input, &out)
	return out, err
}
func (v *WorkspaceView) CapabilityRuntimeSnapshot(ctx context.Context) (coreprotocol.CapabilityResult, error) {
	var out coreprotocol.CapabilityResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.CapabilityRuntime, struct{}{}, &out)
	return out, err
}
func (v *WorkspaceView) CapabilitySnapshot(ctx context.Context) (coreprotocol.CapabilityResult, error) {
	var out coreprotocol.CapabilityResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.CapabilitySnapshot, struct{}{}, &out)
	return out, err
}
func (v *WorkspaceView) CapabilityAction(ctx context.Context, input coreprotocol.CapabilityActionRequest) (coreprotocol.CapabilityResult, error) {
	var out coreprotocol.CapabilityResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.CapabilityAction, input, &out)
	return out, err
}
func (v *WorkspaceView) PreviewCapabilityAcquisition(ctx context.Context, input coreprotocol.CapabilityActionRequest) (coreprotocol.CapabilityPreviewResult, error) {
	var out coreprotocol.CapabilityPreviewResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.CapabilityAcquisitionPreview, input, &out)
	return out, err
}
func (v *WorkspaceView) PrepareSkillInput(ctx context.Context, input coreprotocol.SkillInputRequest) (coreprotocol.SkillInputResult, error) {
	var out coreprotocol.SkillInputResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.SkillInput, input, &out)
	return out, err
}
