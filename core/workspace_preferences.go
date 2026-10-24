package core

import (
	"context"

	"crude/coreprotocol"
	"crude/types"
	"crude/workspace"
)

// Capture on actor, persist on worker, publish only back on actor. A save must
// not mutate session/LSP/config projections concurrently with agent ingress.
func (a *actor) prepareWorkspacePreferences(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, func(coreprotocol.Response), bool) {
	if request.Method != coreprotocol.WorkspaceOverridesSet {
		return nil, nil, false
	}
	var input coreprotocol.WorkspacePreferencesRequest
	if err := decodeRequest(request, &input); err != nil {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, nil, true
	}
	if err := types.ValidateSettingEdits(types.AppSettings{}, input.Edits); err != nil {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, nil, true
	}
	for _, edit := range input.Edits {
		if len(edit.Path) == 0 || edit.Path[0] == "onboarding" || edit.Path[0] == "accounts" || edit.Path[0] == "network" || edit.Path[0] == "capabilities" && (len(edit.Path) < 2 || edit.Path[1] != "acquisition") {
			return func(context.Context) coreprotocol.Response {
				return fault(request, "invalid_request", "field is not a workspace preference")
			}, nil, true
		}
	}
	run := a.ws.PreparePreferencesSave(types.SettingsScopeWorkspace, input.Edits)
	var retained workspace.PreferencesSaveResult
	work := func(ctx context.Context) coreprotocol.Response {
		if err := ctx.Err(); err != nil {
			return fault(request, "cancelled", err.Error())
		}
		retained = run()
		if retained.Err != nil {
			return fault(request, "save_failed", retained.Err.Error())
		}
		return response(request, coreprotocol.WorkspacePreferencesResult{Effective: publicApp(retained.Effective), Overrides: retained.Overrides})
	}
	apply := func(result coreprotocol.Response) {
		if result.Error == nil {
			a.ws.ApplyPreferencesSave(retained)
		}
	}
	return work, apply, true
}
