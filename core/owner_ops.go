package core

import (
	"context"
	"errors"
	"fmt"

	"crude/types"

	"crude/coreprotocol"
)

// executeOwner serves cheap, in-memory operations on the actor goroutine, the
// only owner of workspace state. It reports false for every other method.
func (a *actor) executeOwner(request coreprotocol.Request) (coreprotocol.Response, bool) {
	switch request.Method {
	case coreprotocol.StatusSnapshotMethod:
		status := a.ws.Status
		return response(request, coreprotocol.StatusSnapshot{
			Version: status.Version(), Agents: status.Agents(), Issues: status.Issues(), Notifications: status.Notifications(true),
		}), true
	case coreprotocol.StatusMutateMethod:
		var in coreprotocol.StatusMutation
		if err := decodeRequest(request, &in); err != nil {
			return fault(request, "invalid_request", err.Error()), true
		}
		out, err := a.mutateStatus(in)
		if err != nil {
			return fault(request, "invalid_request", err.Error()), true
		}
		a.publishStatusIfChanged()
		return response(request, out), true
	case coreprotocol.CancelRequest:
		var in coreprotocol.CancelRequestPayload
		if err := decodeRequest(request, &in); err != nil {
			return fault(request, "invalid_request", err.Error()), true
		}
		// Only the requester's own job is reachable: the key includes its client.
		key := jobKey(coreprotocol.Request{Owner: request.Owner, ID: in.RequestID})
		cancel, ok := a.jobCancels[key]
		if ok {
			cancel()
		}
		return response(request, coreprotocol.BoolResult{OK: ok}), true
	case coreprotocol.AgentYolo:
		var in coreprotocol.AgentYoloRequest
		if err := decodeRequest(request, &in); err != nil {
			return fault(request, "invalid_request", err.Error()), true
		}
		ok := a.ws.SetYoloForAgent(in.AgentID, in.Enabled)
		if ok {
			a.changed([]string{in.AgentID})
		}
		return response(request, coreprotocol.BoolResult{OK: ok}), true
	case coreprotocol.AgentAppendMessages:
		var in coreprotocol.AppendMessagesRequest
		if err := decodeRequest(request, &in); err != nil {
			return fault(request, "invalid_request", err.Error()), true
		}
		ok := a.ws.AppendAgentMessages(in.AgentID, in.Messages, in.Stopped)
		if ok {
			a.changed([]string{in.AgentID})
		}
		return response(request, coreprotocol.BoolResult{OK: ok}), true
	case coreprotocol.AgentRecoverReject:
		var in coreprotocol.RecoveryRequest
		if err := decodeRequest(request, &in); err != nil {
			return fault(request, "invalid_request", err.Error()), true
		}
		a.ws.RejectAgentRecoveryCancellation(in.AgentID, in.IncidentID)
		a.changed([]string{in.AgentID})
		return response(request, coreprotocol.BoolResult{OK: true}), true
	case coreprotocol.WorkspaceStartup:
		out := coreprotocol.StartupResult{Issues: a.ws.TakeStartupIssues()}
		if err := a.ws.LastRegistryError(); err != nil {
			out.RegistryError = err.Error()
		}
		return response(request, out), true
	case coreprotocol.WorkspaceOverrides:
		return response(request, coreprotocol.OverridesResult{Overrides: a.ws.LoadWorkspaceOverrides(), AgentDefaults: a.ws.WorkspaceAgentDefaults()}), true
	}
	return coreprotocol.Response{}, false
}

func (a *actor) mutateStatus(in coreprotocol.StatusMutation) (coreprotocol.StatusMutationResult, error) {
	status := a.ws.Status
	switch in.Op {
	case coreprotocol.StatusNotify:
		if in.Notification == nil {
			return coreprotocol.StatusMutationResult{}, fmt.Errorf("notify requires a notification")
		}
		id := status.Notify(*in.Notification)
		return coreprotocol.StatusMutationResult{ID: id, Applied: id != ""}, nil
	case coreprotocol.StatusMarkRead:
		return coreprotocol.StatusMutationResult{ID: in.ID, Applied: status.MarkRead(in.ID, in.Read)}, nil
	case coreprotocol.StatusDismiss:
		return coreprotocol.StatusMutationResult{ID: in.ID, Applied: status.Dismiss(in.ID)}, nil
	case coreprotocol.StatusSetIssue:
		if in.Issue == nil {
			return coreprotocol.StatusMutationResult{}, fmt.Errorf("set_issue requires an issue")
		}
		status.SetIssue(*in.Issue)
		return coreprotocol.StatusMutationResult{ID: in.Issue.ID, Applied: true}, nil
	case coreprotocol.StatusResolveIssue:
		status.ResolveIssue(in.ID)
		return coreprotocol.StatusMutationResult{ID: in.ID, Applied: true}, nil
	case coreprotocol.StatusSetOperation:
		if in.Operation == nil {
			return coreprotocol.StatusMutationResult{}, fmt.Errorf("set_operation requires an operation")
		}
		status.SetOperation(*in.Operation)
		return coreprotocol.StatusMutationResult{ID: in.Operation.ID, Applied: true}, nil
	case coreprotocol.StatusFinishOperation:
		status.FinishOperation(in.ID, in.State, in.Error)
		return coreprotocol.StatusMutationResult{ID: in.ID, Applied: true}, nil
	}
	return coreprotocol.StatusMutationResult{}, fmt.Errorf("unknown status operation %q", in.Op)
}

// publishStatusIfChanged tells shells to refresh their status replica after the
// center's version moved, whoever changed it.
func (a *actor) publishStatusIfChanged() {
	if a.ws.Status == nil {
		return
	}
	if version := a.ws.Status.Version(); version != a.statusVersion {
		a.statusVersion = version
		a.broadcast(coreprotocol.Change{Status: true})
	}
}

// announceSettings tells shells which replica a completed mutation changed.
func (a *actor) announceSettings(method string) {
	switch method {
	case coreprotocol.SessionSettingsSave, coreprotocol.SessionSettingsReset, coreprotocol.SessionMultiAgentToggle, coreprotocol.SessionCapabilityToggle,
		coreprotocol.ReferenceCreate, coreprotocol.ReferenceImport, coreprotocol.ReferenceSaveContent, coreprotocol.ReferenceRename,
		coreprotocol.ReferenceDelete, coreprotocol.ReferenceUploadCommit, coreprotocol.HistoryRestore, coreprotocol.HistoryImportAttachment:
		a.broadcast(coreprotocol.Change{Sessions: true})
	case coreprotocol.AccountChange, coreprotocol.ApplicationPreferencesSave, coreprotocol.CapabilityAction, coreprotocol.WorkspaceOverridesSet:
		a.broadcast(coreprotocol.Change{Settings: true, Sessions: true})
	}
}

// ApplyAppDefaults republishes committed application defaults on every
// retained workspace's actor and tells its shells to refresh settings.
func (r *Runtime) ApplyAppDefaults(ctx context.Context, settings types.AppSettings) error {
	var errs []error
	for _, a := range r.actors() {
		if err := a.inspect(ctx, func() {
			a.ws.SetAppDefaults(settings)
			a.broadcast(coreprotocol.Change{Settings: true, Sessions: true})
		}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func jobKey(request coreprotocol.Request) string {
	return request.Owner.ClientID + "\x00" + request.ID
}
