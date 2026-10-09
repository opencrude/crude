package core

import (
	"context"
	"errors"
	"sync"

	"crude/coreprotocol"
	"crude/session"
	"crude/workspace"
)

// prepareSessionSettings captures the exact backend on the actor. Only the
// worker commits persistence; only the actor publishes its private receipt.
func (a *actor) prepareSessionSettings(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, func(coreprotocol.Response), bool) {
	switch request.Method {
	case coreprotocol.SessionSettingsSave, coreprotocol.SessionSettingsReset, coreprotocol.SessionMultiAgentToggle:
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
	var op *workspace.SessionSettingsOperation
	var err error
	switch request.Method {
	case coreprotocol.SessionSettingsSave:
		value := in.Settings.Value
		value.SessionMainAgent, value.SessionOverrides, value.SessionMultiAgent = in.Settings.MainAgent, in.Settings.Overrides, in.Settings.MultiAgent
		op, err = a.ws.PrepareSessionSettingsSaveAtRevision(in.SessionID, value, in.ExpectedRevision)
	case coreprotocol.SessionSettingsReset:
		op, err = a.ws.PrepareSessionSettingsResetAtRevision(in.SessionID, in.ExpectedRevision)
	case coreprotocol.SessionMultiAgentToggle:
		op, err = a.ws.PrepareSessionSettingsToggleAtRevision(in.SessionID, in.ExpectedRevision)
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
		if errors.Is(retained.Err, session.ErrSettingsRevisionConflict) {
			return fault(request, "stale_preimage", "session settings changed; fetch the current revision")
		}
		if errors.Is(retained.Err, session.ErrSettingsSessionClosing) {
			return fault(request, "session_closing", "session is closing")
		}
		if retained.Err != nil {
			return fault(request, "settings_failed", "session settings could not be saved; repair the selected configuration")
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
