package coreprotocol

import (
	"errors"

	"crude/types/control"
)

const (
	ControlShellRegister    = "control.shell.register"
	ControlShellReply       = "control.shell.reply"
	ControlRequest          = "control.request"
	ControlMaxEnvelopeBytes = 1 << 20
	ControlMaxQueuedBytes   = 8 << 20
	ControlMaxOutstanding   = 16
)

// ControlEnvelope is attribution and an immutable descriptor preimage, not
// authorization. The gateway and the effect owner remain responsible for grants
// and current resource/revision/open-epoch checks.
type ControlEnvelope struct {
	Request    control.Request    `json:"request"`
	Descriptor control.Descriptor `json:"descriptor"`
}

func (e ControlEnvelope) Clone() ControlEnvelope {
	return ControlEnvelope{Request: e.Request.Clone(), Descriptor: e.Descriptor.Clone()}
}

func controlID(id string, optional bool) bool {
	if id == "" {
		return optional
	}
	if len(id) > 256 {
		return false
	}
	for _, r := range id {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}

// ValidateTransport narrows neutral IDs to the core envelope contract.
func (e ControlEnvelope) ValidateTransport(workspaceID string, owner control.ShellOwner) error {
	if f := e.Request.Validate(e.Descriptor); f != nil {
		return f
	}
	if e.Descriptor.Scope != control.Shell || e.Request.Target.WorkspaceID != workspaceID || e.Request.Target.Shell == nil || *e.Request.Target.Shell != owner {
		return errors.New("control request target binding mismatch")
	}
	for _, id := range []string{workspaceID, owner.ClientID, e.Request.ID, e.Request.CommandID, e.Descriptor.Input.ID, e.Descriptor.Output.ID} {
		if !controlID(id, false) {
			return errors.New("invalid control transport identity")
		}
	}
	// Component/resource identities follow the neutral bounded UTF-8 contract;
	// they are data, not frame routing IDs. Retained ActionBar controls can include
	// spaces or Unicode in their logical identity, unlike core actor/run IDs.
	for _, id := range []string{e.Request.Target.ExecutionID, e.Request.Target.SessionID, e.Request.Caller.AgentID, e.Request.Caller.RunID, e.Request.Caller.ClientID} {
		if !controlID(id, true) {
			return errors.New("invalid control transport identity")
		}
	}
	b, err := Marshal(&e)
	if err != nil {
		return err
	}
	if len(b) > ControlMaxEnvelopeBytes {
		return errors.New("control envelope exceeds limit")
	}
	return nil
}

func ValidateControlResult(r control.Result) error {
	if f := r.Validate(); f != nil {
		return f
	}
	if !controlID(r.RequestID, false) || !controlID(r.OperationID, false) {
		return errors.New("invalid control result identity")
	}
	for _, id := range r.ResourceIDs {
		if !controlID(id, false) {
			return errors.New("invalid control resource identity")
		}
	}
	b, err := Marshal(&r)
	if err != nil {
		return err
	}
	if len(b) > ControlMaxEnvelopeBytes {
		return errors.New("control result exceeds limit")
	}
	return nil
}

// BoundControlResult enforces semantic value and transport limits before a final
// reply or durable receipt is published. Valid committed identities survive a
// malformed owner result; invalid presentation data cannot cause a reply timeout.
func BoundControlResult(request control.Request, result control.Result, mutation bool) control.Result {
	result = control.BoundResult(request, result, mutation)
	if ValidateControlResult(result) == nil {
		return result
	}
	out := control.Result{RequestID: request.ID, OperationID: request.ID, State: control.Failed,
		Fault: &control.Fault{Code: control.OperationFailed, Message: "control result exceeds transport bounds; inspect the original operation receipt without repeating a mutation"}}
	if mutation && result.State != control.Failed {
		out.State = control.Partial
		if result.State == control.Uncertain {
			out.State = control.Uncertain
		}
	}
	if controlID(result.OperationID, false) {
		out.OperationID = result.OperationID
	}
	for _, id := range result.ResourceIDs {
		if controlID(id, false) {
			out.ResourceIDs = append(out.ResourceIDs, id)
		}
	}
	out.Output, out.Revision = result.Output, result.Revision
	return out
}
