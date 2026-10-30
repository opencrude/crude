package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"crude/buildinfo"
	"crude/collaboration"
	"crude/coreprotocol"
	"crude/retry"
	"crude/types/control"
	"crude/types/handover"
)

func coreRestartMethod(method string) bool {
	switch method {
	case coreprotocol.CoreRestartReserve, coreprotocol.CoreRestartInspect, coreprotocol.CoreRestartCancel, coreprotocol.CoreRestartContinue:
		return true
	}
	return false
}

// This is an application-owned handoff on a live local shell connection, not a
// headless MCP goal-resume endpoint. Admission rechecks the shell registration
// and actor attachment; neither a supplied owner nor a private-file path is a
// substitute for an authenticated connection.
func (a *actor) coreRestartClient(request coreprotocol.Request) (*Connection, error) {
	a.runtime.mu.Lock()
	client := a.runtime.clients[request.Owner.ClientID]
	a.runtime.mu.Unlock()
	if client == nil || client.Owner != request.Owner || !a.validClient(client) {
		return nil, errors.New("the exact restart shell is detached or replaced")
	}
	client.mu.Lock()
	valid := client.localHost && client.controlRegistered[a.id] && len(client.attached) == 1
	external := client.controlExternal[a.id] != nil
	client.mu.Unlock()
	if !valid || external {
		return nil, errors.New("restart requires the registered original local application shell")
	}
	return client, nil
}

func (a *actor) prepareCoreRestart(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	if !coreRestartMethod(request.Method) {
		return nil, false
	}
	fail := func(err error) (func(context.Context) coreprotocol.Response, bool) {
		return func(context.Context) coreprotocol.Response { return fault(request, "handover_refused", err.Error()) }, true
	}
	client, err := a.coreRestartClient(request)
	if err != nil {
		return fail(err)
	}
	var sessionID, operationID, token string
	var reservation handover.Reservation
	var original control.Request
	switch request.Method {
	case coreprotocol.CoreRestartReserve:
		var in coreprotocol.CoreRestartReservationRequest
		if err := decodeRequest(request, &in); err != nil {
			return fail(err)
		}
		sessionID, operationID, reservation = in.SessionID, in.Reservation.OperationID, in.Reservation
		admitted, ok := client.ControlAdmission(in.ControlRequestID, a.id)
		if !ok || admitted.CommandID != "core.restart" || admitted.ID != operationID ||
			admitted.Target.SessionID != sessionID || admitted.Target.WorkspaceID != a.id ||
			admitted.Target.Shell == nil || admitted.Target.Shell.ClientID != client.Owner.ClientID ||
			admitted.Target.Shell.Epoch != client.Owner.Epoch || admitted.Caller.Kind != control.AgentCaller {
			return fail(errors.New("the original pending native restart control is required"))
		}
		var input struct {
			Executable   string `json:"executable"`
			SHA256       string `json:"sha256"`
			GoalID       string `json:"goal_id"`
			DelaySeconds int    `json:"delay_seconds"`
		}
		if err := controlDecode(admitted.Input, &input); err != nil || input.GoalID != reservation.GoalID {
			return fail(errors.New("restart reservation differs from the admitted goal"))
		}
		if reservation.SourceCoreOwner != a.coreOwnerGeneration() || reservation.SourceShell != *admitted.Target.Shell ||
			reservation.SourceAgentID != admitted.Caller.AgentID || reservation.SourceRunID != admitted.Caller.RunID {
			return fail(errors.New("restart source identity differs from the admitted operation"))
		}
		if reservation.ExpiresAt.After(time.Now().Add(10 * time.Minute)) {
			return fail(errors.New("restart reservation deadline exceeds ten minutes"))
		}
		original = admitted
	case coreprotocol.CoreRestartInspect:
		var in coreprotocol.CoreRestartStateRequest
		if err := decodeRequest(request, &in); err != nil {
			return fail(err)
		}
		sessionID, operationID = in.SessionID, in.OperationID
	default:
		var in coreprotocol.CoreRestartPermitRequest
		if err := decodeRequest(request, &in); err != nil {
			return fail(err)
		}
		sessionID, operationID, token = in.SessionID, in.OperationID, in.Token
	}
	if !validID(sessionID) || !validID(operationID) || len(token) > 128 {
		return fail(errors.New("restart session, operation or permit is invalid"))
	}
	frontend, backend := a.ws.SessionsByID[sessionID], a.ws.BackendSession(sessionID)
	if frontend == nil || backend == nil {
		return fail(errors.New("restart session must be passively opened on this workspace"))
	}
	if request.Method == coreprotocol.CoreRestartReserve {
		if original.Caller.AgentID != frontend.AgentID {
			return fail(errors.New("only the session's root agent may reserve its goal for restart"))
		}
		// The shutdown lease remains authoritative. This preview rejects known
		// neighbors before suspending the goal; it never pauses their work.
		for _, peer := range a.ws.Sessions {
			if peer.SessionID == sessionID {
				continue
			}
			if owner := a.ws.BackendSession(peer.SessionID); owner != nil {
				goals, loops := owner.AutonomousWork()
				if owner.Activity().Any() || goals+loops != 0 {
					return fail(fmt.Errorf("session %s still owns work; restart was not scheduled", peer.SessionID))
				}
			}
		}
	}
	run := func(ctx context.Context) coreprotocol.Response {
		// Preparation runs on the actor. Capture only its session owner and
		// immutable connection identities for I/O; recheck mutable admission
		// facts on their authoritative owners, never read frontend maps here.
		var state handover.State
		var err error
		if !a.coreRestartConnectionCurrent(client) {
			return fault(request, "handover_refused", "restart shell detached before the effect")
		}
		switch request.Method {
		case coreprotocol.CoreRestartReserve:
			if _, pending := client.ControlAdmission(operationID, a.id); !pending {
				return fault(request, "handover_refused", "restart control settled before reservation")
			}
			if _, err := a.controlCallerContext(ctx, original.Caller, original.Target); err != nil {
				return fault(request, "handover_refused", err.Error())
			}
			member, memberErr := a.ws.Collaboration.Agent(ctx, original.Caller.AgentID)
			if memberErr != nil || member.Presence != collaboration.RuntimeAttached || member.CurrentRunID != original.Caller.RunID {
				return fault(request, "handover_refused", "restart root run is no longer current")
			}
			reservation.SourceRuntimeEpoch = member.RuntimeEpoch
			reservation.SourceTurnGeneration = 0 // the session fills its actual current turn
			status, statusErr := a.runtime.Status(ctx)
			if statusErr != nil || status.Partial || status.Opening != 0 || status.Draining || status.Clients != 1 {
				return fault(request, "handover_refused", "core ownership or peer activity is not exclusively available")
			}
			for _, other := range status.Workspaces {
				if other.ID != a.id && other.Busy() {
					return fault(request, "handover_refused", "another workspace still owns work: "+other.Root)
				}
			}
			state, err = backend.ReserveGoalHandover(reservation)
		case coreprotocol.CoreRestartInspect:
			state, err = backend.GoalHandover(operationID)
			if err == nil && state.SourceShell.ClientID != client.Owner.ClientID {
				err = errors.New("restart belongs to a different original profile")
			}
		case coreprotocol.CoreRestartCancel:
			state, err = backend.GoalHandover(operationID)
			if err == nil && state.SourceShell.ClientID != client.Owner.ClientID {
				err = errors.New("restart belongs to a different original profile")
			}
			if err == nil {
				err = backend.CancelGoalHandover(operationID, token)
			}
			if err == nil {
				state, err = backend.GoalHandover(operationID)
			}
		case coreprotocol.CoreRestartContinue:
			state, err = backend.GoalHandover(operationID)
			if err == nil && (state.SourceShell.ClientID != client.Owner.ClientID ||
				state.SourceCoreOwner == a.coreOwnerGeneration() || a.coreOwnerGeneration() == "" ||
				state.SuccessorCohort != buildinfo.ProtocolBuild || state.SuccessorSchema != coreprotocol.Schema) {
				err = errors.New("continuation does not match the intended successor core and original shell")
			}
			if err == nil {
				target := control.Target{WorkspaceID: a.id, SessionID: sessionID,
					Shell: &control.ShellOwner{ClientID: client.Owner.ClientID, Epoch: client.Owner.Epoch}}
				join, cancel := context.WithTimeout(ctx, 20*time.Second)
				defer cancel()
				for attempt := 1; ; attempt++ {
					if !a.coreRestartConnectionCurrent(client) {
						err = errors.New("restart successor shell detached before continuation")
						break
					}
					state, err = backend.ContinueGoalHandover(operationID, token, buildinfo.ProtocolBuild, coreprotocol.Schema, target)
					if !errors.Is(err, handover.ErrMaintenancePending) {
						break
					}
					if waitErr := retry.Wait(join, retry.Exponential(attempt, 25*time.Millisecond, 250*time.Millisecond)); waitErr != nil {
						err = errors.New("owned successor maintenance did not join before the continuation deadline")
						break
					}
				}
			}
		}
		if err != nil {
			return fault(request, "handover_refused", err.Error())
		}
		return response(request, coreprotocol.CoreRestartResult{State: state})
	}
	return run, true
}

// Worker-side recheck uses only synchronized owners. Actor frontend/subscriber
// maps are deliberately not read by the I/O worker.
func (a *actor) coreRestartConnectionCurrent(client *Connection) bool {
	a.runtime.mu.Lock()
	current := a.runtime.clients[client.Owner.ClientID] == client &&
		a.runtime.epochs[client.Owner.ClientID] == client.Owner.Epoch &&
		!a.runtime.closed && !a.runtime.draining && !a.runtime.stopping
	a.runtime.mu.Unlock()
	if !current || a.ctx.Err() != nil {
		return false
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.localHost && client.attached[a.id] == a && client.controlRegistered[a.id] &&
		client.controlExternal[a.id] == nil && len(client.attached) == 1
}

func (a *actor) coreOwnerGeneration() string {
	a.runtime.lifetime.mu.Lock()
	defer a.runtime.lifetime.mu.Unlock()
	return a.runtime.lifetime.ownerGeneration
}
