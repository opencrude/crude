package remote

import (
	"context"
	"errors"
	"time"

	"crude/coreprotocol"
	"crude/fileio"
	"crude/provider"
	"crude/session"
	"crude/types"
	"crude/workspace"
)

func (w *Workspace) AgentMgr() *workspace.AgentManager { return w.view.AgentMgr }
func (w *Workspace) PaneMgr() *workspace.PaneManager   { return w.view.PaneMgr }

// AdvanceAgentState runs on the core owner loop; replicas receive its results.
func (w *Workspace) AdvanceAgentState(time.Time) workspace.AgentReduction {
	return workspace.AgentReduction{}
}

func (w *Workspace) AgentActivity(agentID string) types.AgentActivityView {
	return w.view.AgentActivity(agentID)
}

func (w *Workspace) AgentModelIdentity(agentID string) types.TurnIdentity {
	return w.view.AgentModelIdentity(agentID)
}

func (w *Workspace) AgentTurnActive(agentID string) bool { return w.view.AgentTurnActive(agentID) }

func (w *Workspace) UsageSummary(agentID string, scope types.UsageScope) (types.UsageSummary, bool) {
	return w.view.UsageSummary(agentID, scope)
}

func (w *Workspace) YoloForAgent(agentID string) bool { return w.view.YoloForAgent(agentID) }

func (w *Workspace) SetYoloForAgent(agentID string, enabled bool) bool {
	if w.view.AgentMgr.OrchestratorFor(agentID) == nil {
		return false
	}
	w.view.SetYolo(agentID, enabled)
	w.commands.push(func(ctx context.Context) {
		var out coreprotocol.BoolResult
		if err := w.call(ctx, coreprotocol.AgentYolo, coreprotocol.AgentYoloRequest{AgentID: agentID, Enabled: enabled}, &out); err != nil || !out.OK {
			w.requestResync()
		}
	})
	return true
}

// ApplyAgentEvent: the core is the only semantic reducer. A connected shell
// never feeds events into its replica; reductions arrive from the core.
func (w *Workspace) ApplyAgentEvent(workspace.AgentEvent) workspace.AgentReduction {
	return workspace.AgentReduction{}
}

func (w *Workspace) ApplyAgentStatusEvent(workspace.AgentEvent) {}

// AppendAgentMessages publishes shell-authored evidence (status rows) through
// the core so every shell and the persisted transcript agree.
func (w *Workspace) AppendAgentMessages(id string, messages []workspace.Message, stopped bool) bool {
	if w.view.AgentMgr.Get(id) == nil || len(messages) == 0 {
		return false
	}
	payload := coreprotocol.AppendMessagesRequest{AgentID: id, Messages: append([]workspace.Message(nil), messages...), Stopped: stopped}
	w.commands.push(func(ctx context.Context) {
		if err := w.call(ctx, coreprotocol.AgentAppendMessages, payload, &coreprotocol.BoolResult{}); err != nil {
			w.logger.Warn("REMOTE", "append_messages_failed", "agent", id, "error", err.Error())
		}
	})
	// The canonical rows arrive with the next conversation delta.
	return false
}

// RegisterAgentRoot returns replicated inventory; the core registered it.
func (w *Workspace) RegisterAgentRoot(id string) *workspace.AgentNode { return w.view.AgentMgr.Get(id) }

// ForgetAgentInventory is performed by the core when it terminates the subtree.
func (w *Workspace) ForgetAgentInventory(string) {}

func (w *Workspace) SyncAgentStatusInventory() {}

func (w *Workspace) generation(agentID string) uint64 {
	if node := w.view.AgentMgr.Get(agentID); node != nil && node.State != nil {
		return node.State.TurnIdentity.Generation
	}
	return 0
}

func (w *Workspace) CancelAgent(agentID string) {
	request := coreprotocol.AgentRequest{AgentID: agentID, Generation: w.generation(agentID)}
	w.commands.push(func(ctx context.Context) {
		if err := w.call(ctx, coreprotocol.CancelAgent, request, &struct{}{}); err != nil {
			w.logger.Warn("REMOTE", "cancel_failed", "agent", agentID, "error", err.Error())
		}
	})
}

// BeginAgentCancellation shows the stopping boundary immediately; the core's
// cancellation reduction then becomes authoritative for every shell.
func (w *Workspace) BeginAgentCancellation(id string) bool {
	node := w.view.AgentMgr.Get(id)
	if node == nil || node.State == nil {
		return false
	}
	node.State.BeginCancellation()
	return true
}

func (w *Workspace) CloseAgent(agentID string) {
	request := coreprotocol.AgentRequest{AgentID: agentID, Generation: w.generation(agentID)}
	w.commands.push(func(ctx context.Context) {
		if err := w.call(ctx, coreprotocol.TerminateAgent, request, &struct{}{}); err != nil {
			w.logger.Warn("REMOTE", "terminate_failed", "agent", agentID, "error", err.Error())
		}
	})
}

func (w *Workspace) SteerAgent(agentID string, input provider.UserInput) bool {
	if w.view.AgentMgr.Get(agentID) == nil || input.InternalID == "" {
		return false
	}
	request := coreprotocol.InputRequest{AgentID: agentID, Text: input.Text, ReferenceIDs: append([]string(nil), input.ReferenceIDs...),
		ActivePaths: append([]string(nil), input.ActivePaths...), DisplayText: input.DisplayText, ContextParts: append([]string(nil), input.ContextParts...)}
	w.commands.push(func(ctx context.Context) {
		var out coreprotocol.DecisionResult
		if err := w.call(ctx, coreprotocol.SteerInput, request, &out); err != nil {
			w.logger.Warn("REMOTE", "steer_failed", "agent", agentID, "error", err.Error())
		}
	})
	return true
}

func (w *Workspace) AdmitSteering(ctx context.Context, agentID string, input provider.UserInput) (bool, error) {
	request := coreprotocol.InputRequest{AgentID: agentID, Text: input.Text, ReferenceIDs: append([]string(nil), input.ReferenceIDs...), ActivePaths: append([]string(nil), input.ActivePaths...), DisplayText: input.DisplayText, ContextParts: append([]string(nil), input.ContextParts...)}
	var out coreprotocol.DecisionResult
	err := w.call(ctx, coreprotocol.SteerInput, request, &out)
	return out.Accepted, err
}

func (w *Workspace) decide(request coreprotocol.DecisionRequest) bool {
	node := w.view.AgentMgr.Get(request.AgentID)
	if node == nil || node.State == nil || request.BlockID == "" {
		return false
	}
	w.commands.push(func(ctx context.Context) {
		var out coreprotocol.DecisionResult
		if err := w.call(ctx, coreprotocol.Decide, request, &out); err != nil {
			w.logger.Warn("REMOTE", "decision_failed", "agent", request.AgentID, "error", err.Error())
			w.reportFailure("Respond to agent", err)
			w.requestResync()
		}
	})
	return true
}

func (w *Workspace) ResolveQuestion(agentID, blockID string, answers, questions map[string]string, cancelled bool) bool {
	return w.decide(coreprotocol.DecisionRequest{AgentID: agentID, BlockID: blockID, Answers: answers, Questions: questions, Cancelled: cancelled})
}

func (w *Workspace) RejectQuestion(agentID, blockID, detail string) bool {
	return w.decide(coreprotocol.DecisionRequest{AgentID: agentID, BlockID: blockID, Reject: detail})
}

func (w *Workspace) ResolveConfirmation(agentID, blockID, choice string) bool {
	return w.decide(coreprotocol.DecisionRequest{AgentID: agentID, BlockID: blockID, Choice: choice})
}

func (w *Workspace) recovery(method, agentID, incidentID string, generation uint64) func() bool {
	request := coreprotocol.RecoveryRequest{AgentID: agentID, IncidentID: incidentID, Generation: generation}
	return func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		var out coreprotocol.BoolResult
		return w.call(ctx, method, request, &out) == nil && out.OK
	}
}

func (w *Workspace) PrepareAgentRecovery(agentID, incidentID string, generation uint64) func() bool {
	return w.recovery(coreprotocol.AgentRecover, agentID, incidentID, generation)
}

func (w *Workspace) PrepareCancelAgentRecovery(agentID, incidentID string, generation uint64) func() bool {
	return w.recovery(coreprotocol.AgentRecoverCancel, agentID, incidentID, generation)
}

func (w *Workspace) RejectAgentRecoveryCancellation(id, incident string) {
	request := coreprotocol.RecoveryRequest{AgentID: id, IncidentID: incident}
	w.commands.push(func(ctx context.Context) {
		_ = w.call(ctx, coreprotocol.AgentRecoverReject, request, &coreprotocol.BoolResult{})
	})
}

func (w *Workspace) PrepareAsyncCommandTermination(agentID string, ref fileio.AsyncCmdRef) (func() error, error) {
	if w.view.AgentMgr.Get(agentID) == nil {
		return nil, errors.New("command owner is unavailable")
	}
	request := coreprotocol.StopCommandRequest{AgentID: agentID, Ref: ref}
	return func() error {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		var out coreprotocol.BoolResult
		if err := w.call(ctx, coreprotocol.AgentStopCommand, request, &out); err != nil {
			return err
		}
		if out.Error != "" {
			return errors.New(out.Error)
		}
		return nil
	}, nil
}

// PrepareCompactForAgent hands compaction to the core, which reduces its
// boundary row once for every shell. The shell only awaits the result.
func (w *Workspace) PrepareCompactForAgent(agentID string) (session.PreparedCompaction, types.CompactResultMsg) {
	if w.view.AgentMgr.Get(agentID) == nil {
		return session.PreparedCompaction{}, types.CompactResultMsg{Error: "No active session"}
	}
	request := coreprotocol.AgentIDRequest{AgentID: agentID}
	return session.PreparedCompaction{AgentID: agentID, Run: func() types.CompactResultMsg {
		// Compaction runs model work; it is bounded by the core, not a UI timeout.
		var out types.CompactResultMsg
		if err := w.call(context.Background(), coreprotocol.AgentCompact, request, &out); err != nil {
			out.Error = err.Error()
		}
		return out
	}}, types.CompactResultMsg{}
}

// HydrateSubAgents: the core hydrated children; report the replicated subtree.
func (w *Workspace) HydrateSubAgents(parentID string) []string {
	parent := w.view.AgentMgr.Get(parentID)
	if parent == nil {
		return nil
	}
	ids := make([]string, 0, len(parent.Children))
	for _, child := range parent.Children {
		ids = append(ids, child.ID)
	}
	return ids
}

// PrepareAgentInputAdmission reserves nothing in the core; the identity of the
// accepted input is the core's receipt, filled in by Commit.
func (w *Workspace) PrepareAgentInputAdmission(agentID string, input provider.UserInput, draftRevision uint64) (*workspace.InputAdmissionIntent, error) {
	owner := w.view.SessionForAgent(agentID)
	if owner == nil || w.view.AgentMgr.Get(agentID) == nil {
		return nil, errors.New("input owner is unavailable")
	}
	intent := &workspace.InputAdmissionIntent{SessionID: owner.SessionID, AgentID: agentID, DraftRevision: draftRevision}
	w.admissionsMu.Lock()
	w.admissions[intent] = input
	w.admissionsMu.Unlock()
	return intent, nil
}

// CommitAgentInputAdmission submits once and binds the intent to the exact
// receipt; a lost response is reported, never replayed under a new identity.
func (w *Workspace) CommitAgentInputAdmission(ctx context.Context, intent *workspace.InputAdmissionIntent) workspace.InputAdmissionResult {
	w.admissionsMu.Lock()
	input, ok := w.admissions[intent]
	delete(w.admissions, intent)
	w.admissionsMu.Unlock()
	if !ok {
		return workspace.InputAdmissionResult{Status: workspace.InputAdmissionRejected, ErrorKind: session.InputAdmissionPreLocalRejection, Err: errors.New("input admission intent is unavailable")}
	}
	request := coreprotocol.InputRequest{AgentID: intent.AgentID, Text: input.Text, ReferenceIDs: append([]string(nil), input.ReferenceIDs...),
		ActivePaths: append([]string(nil), input.ActivePaths...), DraftRevision: intent.DraftRevision,
		DisplayText: input.DisplayText, ContextParts: append([]string(nil), input.ContextParts...)}
	var out coreprotocol.InputResult
	if err := w.call(ctx, coreprotocol.SubmitInput, request, &out); err != nil {
		return workspace.InputAdmissionResult{Status: workspace.InputAdmissionRejected, Err: err}
	}
	result := workspace.InputAdmissionResult{Status: out.Status, Receipt: out.Receipt}
	if out.Error != "" {
		result.Err = errors.New(out.Error)
	}
	intent.InputID, intent.OwnerEpoch, intent.TurnGeneration = out.Receipt.InputID, out.Receipt.OwnerEpoch, out.Receipt.TurnGeneration
	return result
}

// ApplyAgentInputAdmission: the accepted message reaches the replica in the
// conversation delta; this only confirms the exact receipt for draft release.
func (w *Workspace) ApplyAgentInputAdmission(intent *workspace.InputAdmissionIntent, result workspace.InputAdmissionResult, _ provider.UserInput, _ []string) bool {
	if intent == nil || (result.Status != workspace.InputAdmissionAccepted && result.Status != workspace.InputAdmissionPreservationPending) {
		return false
	}
	receipt := result.Receipt
	return receipt.InputID == intent.InputID && receipt.AgentID == intent.AgentID && receipt.SessionID == intent.SessionID &&
		receipt.DraftRevision == intent.DraftRevision && receipt.ContextRevision != 0 && !receipt.AcceptedAt.IsZero()
}

func (w *Workspace) ResetOrchestratorOverrides(orchID string) {
	w.commands.push(func(ctx context.Context) {
		_ = w.call(ctx, coreprotocol.AgentResetOverrides, coreprotocol.AgentIDRequest{AgentID: orchID}, &coreprotocol.BoolResult{})
	})
}

func (w *Workspace) PrepareSkillInput(ctx context.Context, agentID, id, arguments string) (provider.UserInput, error) {
	result, err := w.rpc().PrepareSkillInput(ctx, coreprotocol.SkillInputRequest{AgentID: agentID, ID: id, Arguments: arguments})
	return result.Input, err
}
