package core

import (
	"context"
	"crude/coreprotocol"
	"crude/provider"
	"crude/types/control"
	"fmt"
	"strings"
)

// ControlInputMethod accepts captured shell input only for the original admitted
// composer.send. Neither caller attribution nor a replacement run is supplied
// by the shell. Dispatch must route SubmitControlInput here before ordinary jobs.
func (c *Connection) ControlInputMethod(ctx context.Context, request coreprotocol.Request) <-chan coreprotocol.Response {
	ready := make(chan coreprotocol.Response, 1)
	fail := func(code, message string) <-chan coreprotocol.Response {
		ready <- fault(request, code, message)
		return ready
	}
	if request.Owner != c.Owner {
		return fail("invalid_owner", "control input connection owner changed")
	}
	var input coreprotocol.ControlInputRequest
	if err := decodeRequest(request, &input); err != nil {
		return fail("invalid_request", err.Error())
	}
	original, ok := c.ControlAdmission(input.ControlRequestID, request.WorkspaceID)
	if !ok || original.CommandID != "composer.send" || original.Target.SessionID != input.SessionID || original.Target.ResourceID != input.AgentID || original.Target.Shell == nil || original.Target.Shell.ClientID != c.Owner.ClientID || original.Target.Shell.Epoch != c.Owner.Epoch {
		return fail("denied", "exact admitted composer send required")
	}
	if original.Preconditions.Revision == nil || original.Preconditions.OpenEpoch == nil || *original.Preconditions.Revision != input.DraftRevision || *original.Preconditions.OpenEpoch != input.DraftEpoch || strings.TrimSpace(input.Text) == "" || len(input.Text) > control.MaxValueBytes {
		return fail("stale", "exact captured draft fences and bounded nonempty input required")
	}
	c.mu.Lock()
	a := c.attached[request.WorkspaceID]
	c.mu.Unlock()
	if a == nil {
		return fail("detached", "control input workspace detached")
	}
	command := actorCommand{inspect: func() {
		if !a.validClient(c) || ctx.Err() != nil {
			ready <- fault(request, "detached", "control input attachment no longer current")
			return
		}
		if _, pending := c.ControlAdmission(input.ControlRequestID, request.WorkspaceID); !pending {
			ready <- fault(request, "stale", "original control admission settled")
			return
		}
		callerOwner, err := a.controlCaller(original.Caller, original.Target)
		if err != nil {
			ready <- fault(request, "denied", err.Error())
			return
		}
		// The original gateway checked agent approval; external grants remain
		// immutable for the admitted connection and are rechecked here.
		if original.Caller.Kind == control.ExternalCaller {
			a.runtime.mu.Lock()
			client := a.runtime.clients[callerOwner.ClientID]
			a.runtime.mu.Unlock()
			if client == nil {
				ready <- fault(request, "denied", "external caller detached")
				return
			}
			client.mu.Lock()
			allowed := client.controlExternal[a.id][control.Communication]
			client.mu.Unlock()
			if !allowed {
				ready <- fault(request, "denied", "communication effect not granted")
				return
			}
		}
		if a.inFlight >= 16 {
			ready <- fault(request, "busy", "input capacity occupied; draft retained")
			return
		}
		// A separate domain intent uses the ORIGINAL caller/control identity.
		// Repeated shell RPCs cannot reserve or dispatch a second provider turn.
		domain := coreprotocol.Request{ID: original.ID + "/input", Owner: callerOwner, WorkspaceID: a.id, Method: coreprotocol.SubmitControlInput, Payload: request.Payload, JSON: request.JSON}
		prior, fresh, err := a.receipts.begin(a.ctx, domain)
		if err != nil {
			ready <- fault(request, "receipt_failed", err.Error())
			return
		}
		if !fresh {
			if prior.State == "completed" {
				out := prior.Response
				out.ID = request.ID
				ready <- out
			} else {
				ready <- fault(request, "outcome_uncertain", "original input intent cannot be replayed")
			}
			return
		}
		value := provider.InputFromControl(input.Text, input.ReferenceIDs, provider.ControlInputProvenance{ControlRequestID: original.ID, Caller: original.Caller})
		value.InternalID = fmt.Sprintf("control-%s-%s", callerOwner.ClientID, original.ID)
		target := original.Target.Clone()
		value.ControlTarget = &target
		if target.ExecutionID == "" {
			a.bindInputExecution(&value, input.AgentID)
		}
		intent, err := a.ws.PrepareControlInputAdmission(input.SessionID, input.AgentID, value, input.DraftRevision)
		if err != nil {
			out := fault(domain, "input_rejected", err.Error())
			if err := a.receipts.complete(a.ctx, domain, out); err != nil {
				out = fault(domain, "outcome_uncertain", "input rejection receipt failed")
			}
			out.ID = request.ID
			ready <- out
			return
		}
		backend := a.ws.BackendSession(input.SessionID)
		var labels []string
		if owner := a.ws.SessionForAgent(input.AgentID); owner.RefStore != nil {
			for _, id := range input.ReferenceIDs {
				if ref, exists := owner.RefStore.GetReference(id); exists {
					labels = append(labels, ref.DisplayName())
				}
			}
		}
		a.inFlight++
		go func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					a.jobs <- actorJobResult{command: actorCommand{result: ready}, admitted: true, apply: func() coreprotocol.Response {
						// Leave the durable domain intent pending/uncertain. A
						// panic is not evidence of no effect or permission to replay.
						return fault(request, "outcome_uncertain", fmt.Sprintf("control input panicked: %v; inspect original receipt, never replay", recovered))
					}}
				}
			}()
			result := backend.CommitInputAdmission(a.ctx, intent)
			a.jobs <- actorJobResult{command: actorCommand{result: ready}, admitted: true, apply: func() coreprotocol.Response {
				if a.ws.ApplyAgentInputAdmission(intent, result, value, labels) {
					a.changed([]string{input.AgentID})
				}
				out := coreprotocol.InputResult{Status: result.Status, Receipt: result.Receipt}
				if result.Err != nil {
					out.Error = result.Err.Error()
				}
				// Journal the domain response under its original identity, then
				// return this transport await's identity without changing evidence.
				durable := response(domain, out)
				if err := a.receipts.complete(context.Background(), domain, durable); err != nil {
					return fault(request, "outcome_uncertain", "input may be committed; domain receipt publication failed; never replay")
				}
				durable.ID = request.ID
				return durable
			}}
		}()
	}}
	select {
	case a.commands <- command:
	case <-ctx.Done():
		return fail("cancelled", ctx.Err().Error())
	case <-a.done:
		return fail("detached", "input actor closed")
	}
	return ready
}
