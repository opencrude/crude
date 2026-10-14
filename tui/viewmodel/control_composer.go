package viewmodel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	"crude/coreprotocol"
	"crude/provider"
	"crude/shellport"
	"crude/types/control"
	"crude/workspace"
)

func (m *App) productComposerOwner(r control.Request) error {
	if m.ActiveSession == nil || m.composerHydratedFor != m.ActiveSession || m.AgentView == nil {
		return errors.New("own composer not bound")
	}
	id := m.AgentView.ActiveID()
	n := m.ws.AgentMgr().Get(id)
	if n == nil || !n.IsOrchestrator() || id != m.activeAgentTabID() || id != r.Target.ResourceID || m.ActiveSession.SessionID != r.Target.SessionID || m.ws.SessionForAgent(id) != m.ActiveSession {
		return errors.New("exact selected root session composer required")
	}
	if r.Caller.Kind == control.AgentCaller && r.Caller.AgentID != id {
		return errors.New("another agent's private composer is denied")
	}
	if owner := m.draftOwner(m.ActiveSession); owner.loading {
		return errors.New("composer draft is loading")
	}
	return nil
}

func (m *App) handleComposerProduct(msg shellport.ControlRequestMsg) (tea.Cmd, bool) {
	r := msg.Envelope.Request
	result := control.Result{State: control.Applied}
	fail := func(code control.FaultCode, text string) (tea.Cmd, bool) {
		return m.failShellControl(msg, code, text), true
	}
	switch r.CommandID {
	case "composer.inspect":
		if err := decodeShellInput(r.Input, &struct{}{}); err != nil {
			return fail(control.Invalid, err.Error())
		}
		if len(m.Input.Value()) > control.MaxValueBytes/2 {
			return fail(control.Unavailable, "draft exceeds bounded inspection; retained unchanged")
		}
		result.Output, _ = json.Marshal(struct {
			Text       string   `json:"text"`
			References []string `json:"reference_ids"`
			Revision   uint64   `json:"revision"`
			Epoch      uint64   `json:"open_epoch"`
		}{m.Input.Value(), m.Input.SnapshotReferenceIDs(), m.Input.DraftRevision(), m.Input.DraftEpoch()})
	case "composer.set":
		var in struct {
			Text *string `json:"text"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return fail(control.Invalid, err.Error())
		}
		if in.Text == nil || len(*in.Text) > 32768 {
			return fail(control.Invalid, "text required within 32768 bytes")
		}
		m.Input.SetValuePreserveReferences(*in.Text)
		m.persistComposerDraftIfChanged()
	case "composer.reference":
		var in struct {
			ID string `json:"id"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return fail(control.Invalid, err.Error())
		}
		store := m.RefStore()
		if store == nil {
			return fail(control.Unavailable, "session reference owner unavailable")
		}
		ref, ok := store.GetReference(in.ID)
		if !ok {
			return fail(control.Unavailable, "reference not owned by this session")
		}
		m.Input.InsertReference(ref)
		m.persistComposerDraftIfChanged()
	case "capability.picker":
		if err := decodeShellInput(r.Input, &struct{}{}); err != nil {
			return fail(control.Invalid, err.Error())
		}
		return m.awaitShellControl(msg, m.openCapabilityPicker(), nil), true
	case "composer.send":
		if err := decodeShellInput(r.Input, &struct{}{}); err != nil {
			return fail(control.Invalid, err.Error())
		}
		if m.Input.HasPendingPastes() {
			return fail(control.Unavailable, "attachment persistence pending; draft retained")
		}
		if strings.TrimSpace(m.Input.Value()) == "" {
			return fail(control.Invalid, "empty draft")
		}
		cmd := m.submitControlComposer(r)
		if cmd == nil {
			return fail(control.Unavailable, "input admission unavailable; draft retained")
		}
		return m.awaitShellControl(msg, cmd, nil), true
	default:
		return nil, false
	}
	m.forceFrame()
	return m.replyShellControl(msg, result), true
}

func (m *App) submitControlComposer(r control.Request) tea.Cmd {
	service, ok := m.ws.(shellport.ControlInput)
	if !ok || m.shellControlPending >= 16 {
		return nil
	}
	owner, frontend := m.ws, m.ActiveSession
	epoch, revision, generation := m.Input.DraftEpoch(), m.Input.DraftRevision(), m.workspaceSwitchGen.Load()
	request := coreprotocol.ControlInputRequest{ControlRequestID: r.ID, SessionID: frontend.SessionID, AgentID: r.Target.ResourceID,
		DraftEpoch: epoch, DraftRevision: revision, Text: m.Input.Value(), ReferenceIDs: m.Input.SnapshotReferenceIDs()}
	intent := &workspace.InputAdmissionIntent{SessionID: frontend.SessionID, AgentID: request.AgentID, DraftRevision: revision}
	if m.pendingInputAdmissions == nil {
		m.pendingInputAdmissions = make(map[*workspace.InputAdmissionIntent]struct{})
	}
	m.pendingInputAdmissions[intent] = struct{}{}
	value := provider.InputFromControl(request.Text, request.ReferenceIDs, provider.ControlInputProvenance{ControlRequestID: r.ID, Caller: r.Caller})
	return func() tea.Msg {
		out, err := service.SubmitControlInput(context.Background(), request)
		result := workspace.InputAdmissionResult{Status: out.Status, Receipt: out.Receipt, Err: err}
		if err != nil {
			result.Status = workspace.InputAdmissionRejected
		}
		if out.Error != "" {
			result.Err = errors.New(out.Error)
		}
		intent.InputID, intent.OwnerEpoch, intent.TurnGeneration = out.Receipt.InputID, out.Receipt.OwnerEpoch, out.Receipt.TurnGeneration
		return inputAdmissionMsg{workspace: owner, workspaceGen: generation, session: frontend, intent: intent,
			agentID: request.AgentID, draftRevision: revision, draftEpoch: epoch, input: value, result: result}
	}
}

func (m *App) finishComposerProduct(done shellControlDoneMsg, admission inputAdmissionMsg) tea.Cmd {
	if admission.workspace != m.ws || admission.workspaceGen != m.workspaceSwitchGen.Load() || admission.session == nil || m.ws.SessionsByAgent()[admission.agentID] != admission.session {
		delete(m.pendingInputAdmissions, admission.intent)
		return m.failShellControl(done.request, control.Stale, "input receipt owner changed; original outcome must be inspected, never replayed")
	}
	r := admission.result.Receipt
	queued := admission.result.Status == workspace.InputAdmissionQueued
	if admission.intent == nil || r.SessionID != admission.session.SessionID || r.AgentID != admission.agentID || r.InputID != admission.intent.InputID || r.OwnerEpoch != admission.intent.OwnerEpoch || r.DraftRevision != admission.draftRevision || r.AcceptedAt.IsZero() || (!queued && (r.ContextRevision == 0 || r.TurnGeneration != admission.intent.TurnGeneration)) {
		delete(m.pendingInputAdmissions, admission.intent)
		return m.failShellControl(done.request, control.OperationFailed, "input not acknowledged; draft retained; inspect original control receipt before any new effect, never replay")
	}
	switch admission.result.Status {
	case workspace.InputAdmissionAccepted, workspace.InputAdmissionQueued, workspace.InputAdmissionPreservationPending:
		// Apply normal draft receipt fences, but never record this nonhuman
		// control submission in the human prompt-history catalog.
		_ = m.applyInputAdmission(admission)
		result := control.Result{State: control.Committed}
		if queued {
			result.State = control.Admitted
		}
		result.Output, _ = json.Marshal(r)
		m.forceFrame()
		return m.replyShellControl(done.request, result)
	default:
		delete(m.pendingInputAdmissions, admission.intent)
		return m.failShellControl(done.request, control.OperationFailed, "input rejected; draft retained")
	}
}
