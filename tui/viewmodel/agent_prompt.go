package viewmodel

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/confirmation"
	"crude/tui/screens/question"
	"crude/tui/ui"
	"crude/tui/wm"
)

type agentPromptKind uint8

const (
	agentPromptConfirmation agentPromptKind = iota
	agentPromptQuestions
)

type agentPromptRequest struct {
	kind          agentPromptKind
	agentID       string
	blockID       string
	question      string
	operation     string
	tool          string
	target        string
	risk          string
	reason        string
	suggestion    string
	questionsJSON string
}

type agentPromptState struct {
	request      agentPromptRequest
	choice       int
	question     *question.Screen
	confirmation *confirmation.Screen
	inline       bool
	projection   *sessionProjection
}

func (s *agentPromptState) panelID() string {
	return fmt.Sprintf("agent-prompt/%d/%q/%q", s.request.kind, s.request.agentID, s.request.blockID)
}

func (m *App) enqueueAgentConfirmation(req agentPromptRequest) {
	if m.agentYoloEnabled(req.agentID) {
		return
	}
	m.enqueueAgentPrompt(req)
}

func (m *App) enqueueAgentQuestions(req agentPromptRequest) {
	m.enqueueAgentPrompt(req)
}

func (m *App) enqueueAgentPrompt(req agentPromptRequest) {
	if m.AgentMgr == nil || m.AgentMgr.Get(req.agentID) == nil || req.blockID == "" {
		return
	}
	for _, state := range m.agentPrompts {
		if state.request.agentID == req.agentID && state.request.blockID == req.blockID {
			return
		}
	}
	state := &agentPromptState{request: req, choice: 2}
	_, state.projection = m.sessionProjectionForAgent(req.agentID)
	if req.kind == agentPromptQuestions {
		state.question = question.New(m.Theme)
		count, err := state.question.ResetWithError(req.questionsJSON)
		if err != nil || count == 0 {
			detail := "question payload could not be rendered"
			if err != nil {
				detail = err.Error()
			}
			m.RejectQuestion(req.agentID, req.blockID, detail)
			return
		}
	} else {
		state.confirmation = confirmation.New(m.Theme)
		state.confirmation.Reset(confirmation.Details{
			Question: req.question, Operation: req.operation, Tool: req.tool, Target: req.target,
			Risk: req.risk, Reason: req.reason, Suggestion: req.suggestion,
		})
	}
	m.agentPrompts = append(m.agentPrompts, state)
	m.openNextAgentPrompt()
}

func (m *App) agentYoloEnabled(agentID string) bool {
	if m.YoloForAgent(agentID) {
		return true
	}
	return m.AgentMgr != nil && m.AgentMgr.ActiveID() == agentID && m.OverlayState != nil && m.OverlayState.IsYolo()
}

func (m *App) openNextAgentPrompt() {
	// Registry order is arrival order. Docked requests stay retained in their
	// family when the user leaves; only never-docked requests enter the FIFO modal.
	for _, state := range append([]*agentPromptState(nil), m.agentPrompts...) {
		if m.AgentMgr.Get(state.request.agentID) == nil {
			m.removeAgentPrompt(state)
			continue
		}
		if state.request.kind == agentPromptConfirmation && m.agentYoloEnabled(state.request.agentID) {
			m.ResolveConfirmation(state.request.agentID, state.request.blockID, "allow")
			m.removeAgentPrompt(state)
			continue
		}
		if !state.inline && state.projection != nil && m.agentPromptInlineEligible(state.request.agentID) {
			if m.agentPrompt == state {
				m.overlayHost.Close(ui.OverlayAgentPrompt)
				m.agentPrompt = nil
			}
			m.openInlineAgentPrompt(state)
		}
	}
	if m.agentPrompt == nil && m.overlayHost != nil && !m.overlayHost.IsOpen(ui.OverlayDirtyClose) {
		for _, state := range m.agentPrompts {
			if state.inline {
				continue
			}
			m.agentPrompt = state
			if state.request.kind == agentPromptQuestions {
				m.openAgentQuestionPrompt(state)
			} else {
				m.openAgentConfirmationPrompt(state)
			}
			break
		}
	}
	m.syncAgentPromptBlocking()
}

func (m *App) agentPromptInlineEligible(agentID string) bool {
	owner := m.AgentMgr.OrchestratorFor(agentID)
	if owner == nil || m.Win == nil {
		return false
	}
	for _, group := range m.Win.GroupIDs() {
		if m.mainPanes != nil && m.mainPanes.compact && group != m.Win.ActiveGroup() {
			continue
		}
		tab := m.Win.GroupActiveTab(group)
		if tab == nil || !m.isAgentTabTitle(tab.Title) {
			continue
		}
		visible := m.AgentMgr.OrchestratorFor(m.agentIDFromTabTitle(tab.Title))
		if visible != nil && owner.ID == visible.ID {
			return true
		}
	}
	return false
}

func (m *App) openInlineAgentPrompt(state *agentPromptState) {
	projection := state.projection
	if projection == nil {
		return
	}
	state.inline = true
	var content ui.Component = state.confirmation
	focus := confirmation.ActionsID
	label := "Confirmation"
	if state.question != nil {
		content, focus, label = state.question, question.OptionsID, "Questions"
	}
	name := state.request.agentID
	if node := m.AgentMgr.Get(name); node != nil && node.Label != "" {
		name = node.Label
	}
	projection.deck.Open(ui.PanelEntry{
		ID: state.panelID(), Title: name + ": " + label, OwnerID: state.request.agentID,
		Content: content, Closable: true, Policy: ui.PanelBlocking, BlockingGroup: "agent-prompts",
		CloseAction: "agent-prompt.close", PreferredHeight: 12,
		FocusID: focus,
	})
	if projection.deck.Active().ID == state.panelID() {
		projection.deckFocused, projection.deckFocus = true, focus
	}
	projection.deckHost.MarkLayout()
}

func (m *App) syncAgentPromptBlocking() {
	if m.OverlayState != nil {
		m.OverlayState.SetBlocking(m.agentPrompt != nil || (m.agentPromptInlineEligible(m.AgentMgr.ActiveID()) && m.sessionDeckBlocking()))
	}
}

func (m *App) promptForScreen(target any) *agentPromptState {
	for _, state := range m.agentPrompts {
		if state.confirmation != nil && target == state.confirmation || state.question != nil && target == state.question {
			return state
		}
	}
	return nil
}

func (m *App) openAgentConfirmationPrompt(state *agentPromptState) {
	req := state.request
	detail := strings.Join(nonemptyPromptParts(
		promptPart("Agent", m.promptOwnerLabel(state.request.agentID)),
		promptPart("Operation", req.operation), promptPart("Tool", req.tool), promptPart("Target", req.target),
		promptPart("Risk", req.risk), promptPart("Reason", req.reason), promptPart("Suggestion", req.suggestion),
	), "\n")
	severity := ui.ConfirmPromptWarning
	if req.risk == "destructive" || req.risk == "blocked" {
		severity = ui.ConfirmPromptDanger
	}
	frame := ui.BuildConfirmFrame(m.Theme, ui.ConfirmFrameSpec{
		Title: "Confirmation required", Question: req.question, Detail: detail, Severity: severity,
		Buttons:      []ui.ConfirmButtonSpec{{Label: "Allow", ActionID: ui.ConfirmActionYes}, {Label: "Always allow", ActionID: ui.ConfirmActionSave}, {Label: "Deny", ActionID: ui.ConfirmActionNo}},
		FocusedIndex: state.choice, Width: 70, DetailMaxLines: 10,
	})
	w, h := frame.Measure(80, 40)
	fw := wm.NewFloatingWindow(string(ui.OverlayAgentPrompt), "", frame, w, h,
		wm.WithDecoration(false), wm.WithMovable(true), wm.WithResizable(false), wm.WithClosable(false), wm.WithModal(true), wm.WithFocusable(true), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(ui.OverlayAgentPrompt, fw, ui.ModalDialogPolicy(), func() {})
	ui.SetConfirmFocus(frame, state.choice)
}

func (m *App) openAgentQuestionPrompt(state *agentPromptState) {
	screen := state.question
	w, h := fitFloatingSize(m.width, m.height, 96, 24, 48, 12)
	frame := ui.NewWindowFrameWidget(m.Theme, ui.NewID(ui.RootID, "agent-question-frame"), ui.FrameWindow, "Questions: "+m.promptOwnerLabel(state.request.agentID), screen, nil, false, w, h)
	if frame.Host() != nil {
		frame.Host().Focus(question.OptionsID)
	}
	fw := wm.NewFloatingWindow(string(ui.OverlayAgentPrompt), "Questions", frame, w, h,
		wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true), wm.WithClosable(false), wm.WithModal(true), wm.WithFocusable(true), wm.WithMinSize(minInt(48, w), minInt(12, h)), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(ui.OverlayAgentPrompt, fw, ui.ModalDialogPolicy(), func() {})
}

func (m *App) handleAgentPromptKey(msg tea.KeyPressMsg) tea.Cmd {
	state := m.agentPrompt
	if state == nil {
		return nil
	}
	if state.request.kind == agentPromptQuestions {
		if fw := m.overlayHost.Window(ui.OverlayAgentPrompt); fw != nil {
			return m.forwardEventToWindowContent(fw, msg)
		}
		return nil
	}
	fw := m.overlayHost.Window(ui.OverlayAgentPrompt)
	if fw == nil {
		return nil
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok {
		return nil
	}
	switch msg.Code {
	case tea.KeyLeft, tea.KeyUp:
		if n := ui.MoveConfirmFocus(cw, -1, 3); n >= 0 {
			state.choice = n
		}
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		delta := 1
		if msg.Code == tea.KeyTab && msg.Mod.Contains(tea.ModShift) {
			delta = -1
		}
		if n := ui.MoveConfirmFocus(cw, delta, 3); n >= 0 {
			state.choice = n
		}
	case tea.KeyEnter:
		m.resolveAgentConfirmationChoice(state.choice)
	case tea.KeyEsc:
		m.cancelAgentPrompt()
	}
	return nil
}

func (m *App) resolveAgentConfirmationChoice(choice int) {
	state := m.agentPrompt
	if state == nil || state.inline {
		return
	}
	choiceName := map[int]string{0: "allow", 1: "always", 2: "deny"}[choice]
	if choiceName == "" {
		choiceName = "deny"
	}
	m.resolveAgentPromptConfirmation(state.request, choiceName)
	m.finishAgentPrompt()
}

func (m *App) promptOwnerLabel(agentID string) string {
	if node := m.AgentMgr.Get(agentID); node != nil && node.Label != "" {
		return node.Label + " (" + agentID + ")"
	}
	return agentID
}

func (m *App) resolvePromptConfirmation(target *confirmation.Screen, choice string) {
	state := m.promptForScreen(target)
	if state == nil {
		return
	}
	m.resolveAgentPromptConfirmation(state.request, choice)
	m.removeAgentPrompt(state)
	m.openNextAgentPrompt()
}

func (m *App) cancelPromptConfirmation(target *confirmation.Screen) {
	state := m.promptForScreen(target)
	if state == nil {
		return
	}
	m.cancelAgentTurn(state.request.agentID)
	m.dismissAgentPromptFor(state.request.agentID)
}

func (m *App) resolveAgentPromptConfirmation(req agentPromptRequest, choice string) {
	if m.ResolveConfirmation(req.agentID, req.blockID, choice) {
		if node := m.AgentMgr.Get(req.agentID); node != nil && node.State != nil {
			// Delivery has accepted this exact decision. Release its local
			// attention state in the same frame as the prompt, not on a later
			// backend event; a newer request must never inherit this response.
			if node.State.PendingBlockID == req.blockID {
				node.State.PendingBlockID = ""
			}
			node.State.Running, node.State.Thinking = true, true
		}
		m.syncAgentStatusInventory()
	} else if m.Feedback != nil {
		m.Feedback.NotifyError("Permission response was not sent: the request is no longer active")
	}
}

func (m *App) resolveAgentQuestion(target *question.Screen, result question.Result, cancelled bool) {
	state := m.promptForScreen(target)
	if state == nil {
		return
	}
	req := state.request
	if !m.ResolveQuestion(req.agentID, req.blockID, result.Answers, result.Questions, cancelled) && m.Feedback != nil {
		m.Feedback.NotifyError("Question response was not sent: the request is no longer active")
	}
	m.removeAgentPrompt(state)
	m.openNextAgentPrompt()
}

func (m *App) cancelAgentPrompt() {
	state := m.agentPrompt
	if state == nil {
		return
	}
	if state.request.kind == agentPromptQuestions {
		m.ResolveQuestion(state.request.agentID, state.request.blockID, nil, nil, true)
	} else {
		m.cancelAgentTurn(state.request.agentID)
	}
	m.removeAgentPrompt(state)
	m.openNextAgentPrompt()
}

func (m *App) finishAgentPrompt() {
	m.removeAgentPrompt(m.agentPrompt)
	m.openNextAgentPrompt()
}

func (m *App) removeAgentPrompt(state *agentPromptState) {
	if state == nil {
		return
	}
	if m.agentPrompt == state {
		m.agentPrompt = nil
		m.overlayHost.Close(ui.OverlayAgentPrompt)
	}
	if state.inline && state.projection != nil {
		m.closeProjectionPanel(state.projection, state.panelID())
	}
	for i, candidate := range m.agentPrompts {
		if candidate == state {
			copy(m.agentPrompts[i:], m.agentPrompts[i+1:])
			m.agentPrompts[len(m.agentPrompts)-1] = nil
			m.agentPrompts = m.agentPrompts[:len(m.agentPrompts)-1]
			break
		}
	}
}

func (m *App) dismissAgentPromptFor(agentID string) {
	m.dismissAgentPromptBlock(agentID, "")
}

func (m *App) dismissAgentPromptBlock(agentID, blockID string) {
	for _, state := range append([]*agentPromptState(nil), m.agentPrompts...) {
		if state.request.agentID == agentID && (blockID == "" || state.request.blockID == blockID) {
			m.removeAgentPrompt(state)
		}
	}
	m.openNextAgentPrompt()
}

func (m *App) closeAgentPromptTab(id string) {
	for _, state := range m.agentPrompts {
		if state.panelID() != id {
			continue
		}
		if state.question != nil {
			m.resolveAgentQuestion(state.question, question.Result{}, true)
		} else {
			m.resolvePromptConfirmation(state.confirmation, "deny")
		}
		return
	}
}

func promptPart(label, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return fmt.Sprintf("%s: %s", label, value)
}

func nonemptyPromptParts(parts ...string) []string {
	out := parts[:0]
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
