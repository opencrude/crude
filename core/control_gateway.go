package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"crude/collaboration"
	"crude/coreprotocol"
	"crude/types/control"
	"crude/workspace"
)

type actorControlRuntime struct{ actor *actor }

type actorControlCommand struct {
	caller   control.Caller
	target   control.Target
	query    string
	request  *control.Request
	result   chan control.Result
	describe chan controlDescribeResult
	resolve  chan controlResolveResult
	observe  chan controlObserveResult
	ctx      context.Context
}

type controlDescribeResult struct {
	descriptors []control.Descriptor
	err         error
}
type controlResolveResult struct {
	descriptor control.Descriptor
	err        error
}
type controlObserveResult struct {
	found  bool
	result control.Result
}

func (a *actor) bindControlSessions() {
	for _, frontend := range a.ws.Sessions {
		if frontend != nil {
			if backend := a.ws.BackendSession(frontend.SessionID); backend != nil {
				backend.SetControlRuntime(&actorControlRuntime{actor: a})
			}
		}
	}
}

func (r *actorControlRuntime) enqueue(ctx context.Context, command *actorControlCommand) error {
	select {
	case r.actor.commands <- actorCommand{control: command}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-r.actor.done:
		return errors.New("control workspace closed")
	}
}

func (r *actorControlRuntime) Describe(ctx context.Context, caller control.Caller, target control.Target, query string) ([]control.Descriptor, error) {
	if target.WorkspaceID == "" {
		target.WorkspaceID = r.actor.id
	}
	ready := make(chan controlDescribeResult, 1)
	if err := r.enqueue(ctx, &actorControlCommand{caller: caller, target: target, query: query, describe: ready, ctx: ctx}); err != nil {
		return nil, err
	}
	select {
	case result := <-ready:
		return result.descriptors, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.actor.done:
		return nil, errors.New("control workspace closed")
	}
}

func (r *actorControlRuntime) Resolve(ctx context.Context, request control.Request) (control.Descriptor, error) {
	if request.Target.WorkspaceID == "" {
		request.Target.WorkspaceID = r.actor.id
	}
	request = request.Clone()
	ready := make(chan controlResolveResult, 1)
	if err := r.enqueue(ctx, &actorControlCommand{request: &request, caller: request.Caller, target: request.Target, resolve: ready, ctx: ctx}); err != nil {
		return control.Descriptor{}, err
	}
	select {
	case result := <-ready:
		return result.descriptor, result.err
	case <-ctx.Done():
		return control.Descriptor{}, ctx.Err()
	case <-r.actor.done:
		return control.Descriptor{}, errors.New("control workspace closed")
	}
}

func (r *actorControlRuntime) Execute(ctx context.Context, request control.Request) (control.Result, error) {
	if request.Target.WorkspaceID == "" {
		request.Target.WorkspaceID = r.actor.id
	}
	request = request.Clone()
	// Observe an original immutable receipt before resolving a control whose
	// component may have legitimately disappeared after Save/Close. This is not
	// fresh operation admission and does not grant a changed payload authority.
	observed := make(chan controlObserveResult, 1)
	if err := r.enqueue(ctx, &actorControlCommand{request: &request, caller: request.Caller, target: request.Target, observe: observed, ctx: ctx}); err != nil {
		return control.Result{}, err
	}
	select {
	case prior := <-observed:
		if prior.found {
			if prior.result.Fault != nil {
				return prior.result, prior.result.Fault
			}
			return prior.result, nil
		}
	case <-ctx.Done():
		return control.Result{}, ctx.Err()
	case <-r.actor.done:
		return control.Result{}, errors.New("control workspace closed")
	}
	// Resolution may need an exact shell inspection. Never wait for a shell on
	// the actor thread, and authorize its actual effect before admitting mutation.
	descriptor, err := r.Resolve(ctx, request)
	if err != nil {
		return control.Result{}, err
	}
	if descriptor.HumanOnly || descriptor.Effect == control.Authority {
		return control.Result{}, errors.New("control operation requires a human-owned authority path")
	}
	approval := control.NewApproval(request, descriptor)
	if descriptor.Effect != control.Read && request.Caller.Kind == control.AgentCaller {
		if _, authorized := approval.Authorized(ctx); !authorized {
			return control.Result{}, approval
		}
	}
	ready := make(chan control.Result, 1)
	if err := r.enqueue(ctx, &actorControlCommand{request: &request, caller: request.Caller, target: request.Target, result: ready, ctx: ctx}); err != nil {
		return control.Result{}, err
	}
	select {
	case result := <-ready:
		if result.Fault != nil {
			return result, result.Fault
		}
		return result, nil
	case <-ctx.Done():
		return uncertainControl(request.ID), ctx.Err()
	case <-r.actor.done:
		return uncertainControl(request.ID), errors.New("control workspace closed during operation")
	}
}

func (a *actor) controlCaller(caller control.Caller, target control.Target) (coreprotocol.Owner, error) {
	if fault := caller.Validate(); fault != nil {
		return coreprotocol.Owner{}, fault
	}
	if target.WorkspaceID != a.id {
		return coreprotocol.Owner{}, errors.New("foreign logical control workspace")
	}
	if caller.Kind == control.AgentCaller {
		store := a.ws.Collaboration
		if store == nil {
			return coreprotocol.Owner{}, errors.New("control caller ownership unavailable")
		}
		participant, err := store.Agent(a.ctx, caller.AgentID)
		if err != nil || participant.CurrentRunID != caller.RunID || participant.Presence != collaboration.RuntimeAttached {
			return coreprotocol.Owner{}, errors.New("control caller run is no longer current")
		}
		run, err := store.Run(a.ctx, caller.RunID)
		if err != nil || run.State.Terminal() || run.AgentID != caller.AgentID {
			return coreprotocol.Owner{}, errors.New("control caller run settled or changed")
		}
		frontend := a.ws.SessionForAgent(caller.AgentID)
		if frontend == nil {
			return coreprotocol.Owner{}, errors.New("control caller session unavailable")
		}
		if target.SessionID != "" && target.SessionID != frontend.SessionID {
			return coreprotocol.Owner{}, errors.New("control cannot reach a foreign session")
		}
		return coreprotocol.Owner{ClientID: "agent:" + caller.AgentID, Epoch: participant.RuntimeEpoch}, nil
	}
	a.runtime.mu.Lock()
	client := a.runtime.clients[caller.ClientID]
	a.runtime.mu.Unlock()
	if client == nil {
		return coreprotocol.Owner{}, errors.New("external control client detached")
	}
	client.mu.Lock()
	attached, admitted := client.attached[a.id] == a, client.controlExternal[a.id] != nil
	client.mu.Unlock()
	if !attached || !admitted {
		return coreprotocol.Owner{}, errors.New("external control client has no admitted control scope")
	}
	return client.Owner, nil
}

func (a *actor) staticControlDescriptor(request control.Request) (control.Descriptor, error) {
	descriptor, ok := control.Lookup(request.CommandID)
	if !ok {
		return control.Descriptor{}, errors.New("crude command is not offered")
	}
	if request.CommandID == "settings.set" || request.CommandID == "settings.reset" {
		var input controlSettingsInput
		if err := controlDecode(request.Input, &input); err != nil {
			return control.Descriptor{}, err
		}
		if ControlSettingsAuthority(input.Path) {
			descriptor.Effect, descriptor.HumanOnly = control.Authority, true
		}
	}
	return descriptor, nil
}

func (a *actor) resolveControlAsync(command *actorControlCommand, descriptor control.Descriptor) {
	request := command.request.Clone()
	if request.Target.Shell == nil {
		command.resolve <- controlResolveResult{err: errors.New("exact submitting shell is unavailable")}
		return
	}
	var input struct {
		ComponentID    string          `json:"component_id"`
		Operation      json.RawMessage `json:"operation"`
		ExpectedAction string          `json:"expected_action"`
	}
	if err := controlDecode(request.Input, &input); err != nil {
		command.resolve <- controlResolveResult{err: err}
		return
	}
	read, _ := control.Lookup("shell.describe_control")
	// Execute resolves twice (authorization, then effect-time admission). Each
	// contextual read needs its own reply lifetime: the first read's waiter can
	// finish before its shell acknowledgement has released client accounting.
	// Reusing <mutation>-resolve can race that cleanup and disconnect the shell.
	// This never changes the original mutation or its durable receipt identity.
	request.ID = "resolve-" + NewClientID()
	request.CommandID = read.ID
	request.Preconditions = control.Preconditions{}
	request.Target.ResourceID = ""
	request.Input, _ = json.Marshal(map[string]any{"component_id": input.ComponentID, "operation": input.Operation})
	go func() {
		result, err := a.runtime.RequestShellControl(command.ctx, a.id, *request.Target.Shell, coreprotocol.ControlEnvelope{Request: request, Descriptor: read})
		if err == nil && result.Fault != nil {
			err = result.Fault
		}
		var resolved struct {
			Descriptor control.Descriptor `json:"descriptor"`
			Node       control.Node       `json:"node"`
		}
		if err == nil {
			err = json.Unmarshal(result.Output, &resolved)
		}
		if err == nil {
			if input.ExpectedAction == "" || resolved.Descriptor.ID != input.ExpectedAction {
				err = errors.New("exact expected_action from control description is required")
			}
			descriptor.Effect, descriptor.HumanOnly, descriptor.Requires = resolved.Descriptor.Effect, resolved.Descriptor.HumanOnly, resolved.Descriptor.Requires
		}
		command.resolve <- controlResolveResult{descriptor: descriptor, err: err}
	}()
}

func (a *actor) admitControl(command *actorControlCommand) {
	owner, err := a.controlCaller(command.caller, command.target)
	if err != nil {
		switch {
		case command.describe != nil:
			command.describe <- controlDescribeResult{err: err}
		case command.resolve != nil:
			command.resolve <- controlResolveResult{err: err}
		case command.observe != nil:
			command.observe <- controlObserveResult{found: true, result: failedControl(*command.request, control.Denied, err.Error())}
		default:
			command.result <- failedControl(*command.request, control.Denied, err.Error())
		}
		return
	}
	if command.describe != nil {
		command.describe <- controlDescribeResult{descriptors: control.Catalog(command.query)}
		return
	}
	if command.observe != nil {
		prior, lookupErr := a.receipts.lookup(command.ctx, owner.ClientID, command.request.ID)
		if lookupErr != nil {
			if errors.Is(lookupErr, sql.ErrNoRows) {
				command.observe <- controlObserveResult{}
			} else {
				command.observe <- controlObserveResult{found: true, result: failedControl(*command.request, control.OperationFailed, "original receipt cannot be inspected")}
			}
			return
		}
		journal := coreprotocol.Request{ID: command.request.ID, Owner: owner, WorkspaceID: a.id, Method: coreprotocol.ControlExecute, Payload: mustPayload(command.request)}
		if _, fresh, beginErr := a.receipts.begin(command.ctx, journal); beginErr != nil || fresh {
			command.observe <- controlObserveResult{found: true, result: failedControl(*command.request, control.Invalid, "original receipt preimage changed")}
			return
		}
		outcome := uncertainControl(command.request.ID)
		if prior.State == "completed" {
			var completed control.Result
			if coreprotocol.Decode(prior.Response.Payload, &completed) == nil && completed.RequestID == command.request.ID {
				outcome = completed
			}
		}
		command.observe <- controlObserveResult{found: true, result: outcome}
		return
	}
	if command.ctx.Err() != nil {
		if command.resolve != nil {
			command.resolve <- controlResolveResult{err: command.ctx.Err()}
		} else {
			command.result <- failedControl(*command.request, control.Unavailable, "operation cancelled before admission")
		}
		return
	}
	descriptor, err := a.staticControlDescriptor(*command.request)
	if command.resolve != nil {
		if err == nil && command.request.CommandID == "shell.control" {
			a.resolveControlAsync(command, descriptor)
			return
		}
		command.resolve <- controlResolveResult{descriptor: descriptor, err: err}
		return
	}
	request := command.request.Clone()
	// Recovery observes the immutable caller-owned receipt before another UI
	// resolution. A component may have legitimately closed after its effect.
	if prior, lookupErr := a.receipts.lookup(command.ctx, owner.ClientID, request.ID); lookupErr == nil {
		journal := coreprotocol.Request{ID: request.ID, Owner: owner, WorkspaceID: a.id, Method: coreprotocol.ControlExecute, Payload: mustPayload(&request)}
		if _, fresh, beginErr := a.receipts.begin(command.ctx, journal); beginErr != nil || fresh {
			command.result <- failedControl(request, control.Invalid, "original receipt preimage changed")
			return
		}
		var outcome control.Result
		if prior.State == "completed" && coreprotocol.Decode(prior.Response.Payload, &outcome) == nil {
			command.result <- outcome
		} else {
			command.result <- uncertainControl(request.ID)
		}
		return
	}
	if err == nil && request.CommandID == "shell.control" {
		// The actual descriptor is part of the exact authorization preimage.
		// Resolve again off-owner, then return through the admitted internal route.
		go func() {
			d, e := (&actorControlRuntime{actor: a}).Resolve(command.ctx, request)
			if e != nil {
				command.result <- failedControl(request, control.Denied, e.Error())
				return
			}
			approval := control.NewApproval(request, d)
			if d.HumanOnly || d.Effect == control.Authority {
				command.result <- failedControl(request, control.Denied, "human-only control")
				return
			}
			if request.Caller.Kind == control.AgentCaller && d.Effect != control.Read {
				if _, ok := approval.Authorized(command.ctx); !ok {
					command.result <- failedControl(request, control.Denied, "control effect changed after authorization")
					return
				}
			} else if request.Caller.Kind == control.ExternalCaller {
				a.runtime.mu.Lock()
				client := a.runtime.clients[owner.ClientID]
				a.runtime.mu.Unlock()
				if client == nil {
					command.result <- failedControl(request, control.Denied, "external client disconnected")
					return
				}
				client.mu.Lock()
				allowed := client.controlExternal[a.id][d.Effect]
				client.mu.Unlock()
				if !allowed {
					command.result <- failedControl(request, control.Denied, "resolved effect is outside external grant")
					return
				}
			}
			result := a.executeShellControl(command.ctx, owner, request, d)
			command.result <- result
		}()
		return
	}
	if err == nil {
		if fault := request.Validate(descriptor); fault != nil {
			err = fault
		}
	}
	if err != nil {
		command.result <- failedControl(request, control.Invalid, err.Error())
		return
	}
	if descriptor.HumanOnly || descriptor.Effect == control.Authority {
		command.result <- failedControl(request, control.Denied, "human-owned operation is not offered through agent control")
		return
	}
	if descriptor.Effect != control.Read && request.Caller.Kind == control.AgentCaller {
		if _, ok := control.NewApproval(request, descriptor).Authorized(command.ctx); !ok {
			command.result <- failedControl(request, control.Denied, "exact control authorization changed before admission")
			return
		}
	}
	if request.Caller.Kind == control.ExternalCaller {
		a.runtime.mu.Lock()
		client := a.runtime.clients[owner.ClientID]
		a.runtime.mu.Unlock()
		client.mu.Lock()
		allowed := client.controlExternal[a.id][descriptor.Effect]
		client.mu.Unlock()
		if !allowed {
			command.result <- failedControl(request, control.Denied, "external control effect is outside the configured grant")
			return
		}
	}
	if descriptor.Scope == control.Shell {
		go func() {
			result := a.executeShellControl(command.ctx, owner, request, descriptor)
			command.result <- result
		}()
		return
	}
	if !a.startControlDomainJob(command, owner, descriptor) {
		command.result <- failedControl(request, control.Unavailable, "domain control command unavailable")
	}
}

// A shell epoch owns presentation, while the requesting caller owns the durable
// receipt. These are distinct identities: external/native receipt observation
// must not accidentally look in the target shell's private map.
func (a *actor) executeShellControl(ctx context.Context, owner coreprotocol.Owner, in control.Request, descriptor control.Descriptor) control.Result {
	journal := descriptor.Effect != control.Read
	request := coreprotocol.Request{ID: in.ID, Owner: owner, WorkspaceID: a.id, Method: coreprotocol.ControlExecute, Payload: mustPayload(&in)}
	if journal {
		prior, fresh, err := a.receipts.begin(ctx, request)
		if err != nil {
			return failedControl(in, control.OperationFailed, err.Error())
		}
		if !fresh {
			var result control.Result
			if prior.State == "completed" && coreprotocol.Decode(prior.Response.Payload, &result) == nil {
				return result
			}
			return uncertainControl(in.ID)
		}
	}
	result, err := a.runtime.RequestShellControl(ctx, a.id, *in.Target.Shell, coreprotocol.ControlEnvelope{Request: in, Descriptor: descriptor})
	if err != nil && result.RequestID == "" {
		result = failedControl(in, control.OperationFailed, err.Error())
	}
	if journal {
		if err := a.receipts.complete(context.WithoutCancel(ctx), request, coreprotocol.Response{ID: in.ID, Payload: mustPayload(&result)}); err != nil {
			return uncertainControl(in.ID)
		}
	}
	return result
}

func failedControl(request control.Request, code control.FaultCode, message string) control.Result {
	return control.Result{RequestID: request.ID, OperationID: request.ID, State: control.Failed, Fault: &control.Fault{Code: code, Message: message}}
}

func (a *actor) startControlDomainJob(command *actorControlCommand, owner coreprotocol.Owner, descriptor control.Descriptor) bool {
	in := command.request.Clone()
	if a.historyBusy && (in.CommandID == "session.create" || in.CommandID == "session.open" || in.CommandID == "history.apply" || in.CommandID == "git.discard_reviewed" || in.CommandID == "execution.create") {
		command.result <- failedControl(in, control.Denied, "history restore owns admission")
		return true
	}
	if a.lifecycle && (in.CommandID == "session.create" || in.CommandID == "session.open") {
		command.result <- failedControl(in, control.Unavailable, "session creation owns admission")
		return true
	}
	if in.CommandID == "operation.receipt" {
		var input coreprotocol.ReceiptRequest
		if err := controlDecode(in.Input, &input); err != nil || input.RequestID == "" {
			command.result <- failedControl(in, control.Invalid, "exact original request_id required")
			return true
		}
		go func() {
			receipt, err := a.receipts.lookup(command.ctx, owner.ClientID, input.RequestID)
			if err != nil || receipt.WorkspaceID != a.id {
				command.result <- failedControl(in, control.Unavailable, "receipt unavailable for this exact caller/workspace")
				return
			}
			command.result <- control.Result{RequestID: in.ID, OperationID: in.ID, State: control.Committed, Output: controlJSON(receipt.View())}
		}()
		return true
	}
	if strings.HasPrefix(in.CommandID, "settings.") && a.settingsBusy {
		if len(a.controlQueue) >= 16 {
			command.result <- failedControl(in, control.Unavailable, "control settings queue is full")
		} else {
			a.controlQueue = append(a.controlQueue, command)
		}
		return true
	}
	if a.inFlight >= 16 {
		command.result <- failedControl(in, control.Unavailable, "control capacity occupied")
		return true
	}
	request := coreprotocol.Request{ID: in.ID, Owner: owner, WorkspaceID: a.id, Method: coreprotocol.ControlExecute, Payload: mustPayload(&in)}
	journal := descriptor.Effect != control.Read
	if journal {
		prior, fresh, err := a.receipts.begin(a.ctx, request)
		if err != nil {
			command.result <- failedControl(in, control.OperationFailed, err.Error())
			return true
		}
		if !fresh {
			var result control.Result
			if prior.State == "completed" && coreprotocol.Decode(prior.Response.Payload, &result) == nil {
				command.result <- result
			} else {
				command.result <- uncertainControl(in.ID)
			}
			return true
		}
	}
	run, apply, finish, handled := a.prepareControlHistory(in, owner)
	if !handled {
		run, apply, handled = a.prepareControlDomain(in, owner)
	}
	var lifecycle *workspace.SessionLifecycleOperation
	if in.CommandID == "session.create" || in.CommandID == "session.open" {
		var input struct {
			SessionID string `json:"session_id"`
		}
		if err := controlDecode(in.Input, &input); err != nil {
			run, apply, handled = controlFailure(in, control.Invalid, err.Error())
		} else {
			var err error
			lifecycle, err = a.ws.PrepareAgentSession(input.SessionID)
			if err != nil {
				run, apply, handled = controlFailure(in, control.OperationFailed, err.Error())
			} else {
				handled = true
			}
		}
	}
	if !handled {
		result := failedControl(in, control.Unavailable, "domain control command unavailable")
		if journal {
			_ = a.receipts.complete(a.ctx, request, coreprotocol.Response{ID: in.ID, Payload: mustPayload(&result)})
		}
		command.result <- result
		return true
	}
	a.inFlight++
	if lifecycle != nil {
		a.lifecycle = true
	}
	historyMutation := in.CommandID == "history.apply" || in.CommandID == "git.discard_reviewed"
	if historyMutation {
		a.historyBusy = true
		release := finish
		finish = func() {
			if release != nil {
				release()
			}
			a.historyBusy = false
		}
	}
	settings := strings.HasPrefix(in.CommandID, "settings.")
	if settings {
		a.settingsBusy = true
	}
	work := func() {
		var result control.Result
		var lifecycleResult workspace.SessionLifecycleResult
		panicked := false
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					panicked = true
					result = uncertainControl(in.ID)
					result.Fault.Message = fmt.Sprintf("operation panicked: %v; inspect original receipt before retrying", recovered)
				}
			}()
			if lifecycle != nil {
				lifecycleResult = lifecycle.Run(command.ctx)
				result = control.Result{RequestID: in.ID, OperationID: in.ID, State: control.Committed}
			} else {
				result = run(command.ctx)
			}
		}()
		completion := make(chan coreprotocol.Response, 1)
		a.jobs <- actorJobResult{command: actorCommand{result: completion}, admitted: true, lifecycle: lifecycle != nil, settings: settings, finish: finish, apply: func() coreprotocol.Response {
			defer func() {
				if recovered := recover(); recovered != nil {
					result = uncertainControl(in.ID)
					result.Fault.Message = fmt.Sprintf("publication panicked: %v; inspect original receipt before retrying", recovered)
				}
				if journal {
					if err := a.receipts.complete(context.Background(), request, coreprotocol.Response{ID: in.ID, Payload: mustPayload(&result)}); err != nil {
						result = uncertainControl(in.ID)
					}
				}
				command.result <- result
			}()
			if apply != nil && !panicked {
				result = apply(result)
			}
			if lifecycle != nil && !panicked {
				out := a.ws.ApplyAgentSession(lifecycleResult)
				if out.Error != "" {
					result = failedControl(in, control.OperationFailed, out.Error)
				} else {
					result.ResourceIDs = []string{out.SessionID, out.OrchID}
					result.Output = controlJSON(out)
					a.bindControlSessions()
					a.changed([]string{out.OrchID})
					a.broadcast(coreprotocol.Change{Sessions: true})
				}
			}
			if result.Fault == nil {
				a.changed(nil)
				if settings {
					a.broadcast(coreprotocol.Change{Settings: true, Sessions: true})
				}
			}
			return coreprotocol.Response{}
		}}
	}
	if strings.HasPrefix(in.CommandID, "terminal.") {
		if !a.terminalLane.submit(func(context.Context) { work() }) {
			command.result <- failedControl(in, control.Unavailable, "terminal owner closing")
			a.inFlight--
			if settings {
				a.settingsBusy = false
			}
		}
	} else if strings.HasPrefix(in.CommandID, "document.") || strings.HasPrefix(in.CommandID, "files.") {
		if !a.resourceLane.submit(func(context.Context) { work() }) {
			command.result <- failedControl(in, control.Unavailable, "resource owner closing")
			a.inFlight--
			if settings {
				a.settingsBusy = false
			}
		}
	} else {
		go work()
	}
	return true
}

// controlJSON renders a semantic control output document.
func controlJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }

// mustPayload encodes a control value for its receipt; control values always
// have generated codecs.
func mustPayload(value coreprotocol.Value) []byte { raw, _ := coreprotocol.Marshal(value); return raw }

var _ control.Runtime = (*actorControlRuntime)(nil)
