package core

import (
	"context"
	"strings"

	"crude/coreprotocol"
	"crude/types/control"
)

type controlLSPRestartResult struct {
	Server           string `json:"server"`
	RestartRequested bool   `json:"restart_requested"`
	Next             string `json:"next"`
}

// prepareControlLSP uses the same manager intent as Settings and Problems. The
// normal gateway journals this exact request; its receipt confirms the restart
// request, never successful server initialization or a finished project check.
func (a *actor) prepareControlLSP(in control.Request, owner coreprotocol.Owner) (func(context.Context) control.Result, func(control.Result) control.Result, bool) {
	var input struct {
		Server string `json:"server"`
	}
	if err := controlDecode(in.Input, &input); err != nil {
		return controlFailure(in, control.Invalid, err.Error())
	}
	if strings.TrimSpace(input.Server) == "" {
		return controlFailure(in, control.Invalid, "Use an exact server ID from diagnostics.workspace, or all.")
	}
	if owner.ClientID == "" || owner.Epoch == 0 {
		return controlFailure(in, control.Invalid, "Trusted caller ownership is required.")
	}
	if in.Target.ResourceID != "" && in.Target.ResourceID != input.Server {
		return controlFailure(in, control.Invalid, "The resource target differs from the server ID.")
	}
	if in.Preconditions.Revision != nil || in.Preconditions.OpenEpoch != nil {
		return controlFailure(in, control.Invalid, "Restart uses the server ID, not document or view preconditions.")
	}
	if a.ws == nil || a.ws.LSPMgr == nil {
		return controlFailure(in, control.Unavailable, "The workspace language service is unavailable.")
	}
	manager, root := a.ws.LSPMgr, a.ws.RootPath
	request := coreprotocol.Request{ID: in.ID, Owner: owner, WorkspaceID: in.Target.WorkspaceID}
	return wrapControlOperation(in, func(ctx context.Context) coreprotocol.Response {
		actual, err := a.controlDomainRoot(ctx, in)
		if err != nil || actual != root {
			return fault(request, "ownership_blocked", "The language service belongs to the attached workspace, not a different execution.")
		}
		if input.Server == "all" {
			manager.Refresh(root)
		} else if !manager.RestartServer(input.Server) {
			return fault(request, "unavailable", "The server cannot restart. Read diagnostics.workspace for its current ID and state.")
		}
		// This local typed result is encoded by wrapControlOperation into the
		// existing semantic JSON receipt, not transported as a new RPC DTO.
		return coreprotocol.Response{ID: in.ID, Value: controlLSPRestartResult{Server: input.Server, RestartRequested: true,
			Next: "Read diagnostics.workspace to check the server and closed-file check results."}}
	}, nil)
}
