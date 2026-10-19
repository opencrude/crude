package core

import (
	"context"
	"errors"
	"sync"

	"crude/coreprotocol"
	"crude/session"
	"crude/types"
	"crude/workspace"
)

// prepareSessionSettings captures the exact backend on the actor. Only the
// worker commits persistence; only the actor publishes its private receipt.
func (a *actor) prepareSessionSettings(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, func(coreprotocol.Response), bool) {
	switch request.Method {
	case coreprotocol.SessionSettingsSave, coreprotocol.SessionSettingsReset, coreprotocol.SessionMultiAgentToggle, coreprotocol.SessionCapabilityToggle:
	default:
		return nil, nil, false
	}
	bad := func(code, message string) (func(context.Context) coreprotocol.Response, func(coreprotocol.Response), bool) {
		return func(context.Context) coreprotocol.Response { return fault(request, code, message) }, nil, true
	}
	var in coreprotocol.SessionSettingsMutationRequest
	if err := decode(request.Payload, &in); err != nil {
		return bad("invalid_request", err.Error())
	}
	if err := types.ValidateSettingEdits(types.SessionPreferences{}, in.Edits); err != nil {
		return bad("invalid_request", err.Error())
	}
	var op *workspace.SessionSettingsOperation
	var err error
	switch request.Method {
	case coreprotocol.SessionSettingsSave:
		value := types.Settings{SessionEdits: in.Edits}
		if value.SessionEdits == nil {
			value.SessionEdits = []types.SettingEdit{}
		}
		op, err = a.ws.PrepareSessionSettingsSave(in.SessionID, value)
	case coreprotocol.SessionSettingsReset:
		op, err = a.ws.PrepareSessionSettingsReset(in.SessionID)
	case coreprotocol.SessionMultiAgentToggle:
		op, err = a.ws.PrepareMultiAgentToggle(in.SessionID)
	case coreprotocol.SessionCapabilityToggle:
		if in.Capability == nil || in.Capability.ID == "" {
			return bad("invalid_request", "capability id is required")
		}
		op, err = a.ws.PrepareSessionCapabilityToggle(in.SessionID, in.Capability.ID, in.Capability.Enabled)
	}
	if err != nil {
		return bad("session_missing", "exact session is not open")
	}
	var retained workspace.SessionSettingsResult
	var completed bool
	var receiptMu sync.Mutex
	run := func(ctx context.Context) coreprotocol.Response {
		receiptMu.Lock()
		defer receiptMu.Unlock()
		if ctx.Err() != nil {
			return fault(request, "cancelled", "session settings mutation cancelled")
		}
		retained = op.Run()
		completed = true
		if errors.Is(retained.Err, session.ErrSettingsSessionClosing) {
			return fault(request, "session_closing", "session is closing")
		}
		if retained.Err != nil {
			return fault(request, "settings_failed", retained.Err.Error())
		}
		return response(request, publicSessionSet(retained.Snapshot))
	}
	apply := func(out coreprotocol.Response) {
		receiptMu.Lock()
		defer receiptMu.Unlock()
		if completed && out.Error == nil {
			a.ws.ApplySessionSettingsResult(retained)
		}
	}
	return run, apply, true
}
