package coreclient

import (
	"context"
	"crude/core/coreprotocol"
)

func (v *WorkspaceView) SaveApplicationPreferences(ctx context.Context, input coreprotocol.ApplicationPreferencesRequest) (coreprotocol.ApplicationPreferencesResult, error) {
	var out coreprotocol.ApplicationPreferencesResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.ApplicationPreferencesSave, &input, &out)
	return out, err
}
