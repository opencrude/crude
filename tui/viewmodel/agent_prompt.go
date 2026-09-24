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
}

func (m *App) enqueueAgentConfirmation(req agentPromptRequest) {
	if m.agentYoloEnabled(req.agentID) {
		return
	}
	m.agentPromptQueue = append(m.agentPromptQueue, req)
	m.openNextAgentPrompt()
}

func (m *App) enqueueAgentQuestions(req agentPromptRequest) {
	m.agentPromptQueue = append(m.agentPromptQueue, req)
	m.openNextAgentPrompt()
}

func (m *App) agentYoloEnabled(agentID string) bool {
	if m.YoloForAgent(agentID) {
		return true
	}
	return m.AgentMgr != nil && m.AgentMgr.ActiveID() == agentID && m.OverlayState != nil && m.OverlayState.IsYolo()
}

func (m *App) openNextAgentPrompt() {
	if m.agentPrompt != nil || len(m.agentPromptQueue) == 0 || m.overlayHost == nil {
		return
	}
	if m.overlayHost.IsOpen(ui.OverlayDirtyClose) {
		return
	}
	req := m.agentPromptQueue[0]
	m.agentPromptQueue = m.agentPromptQueue[1:]
	if req.kind == agentPromptConfirmation && m.agentYoloEnabled(req.agentID) {
		m.openNextAgentPrompt()
		return
	}
	state := &agentPromptState{request: req, choice: 2, inline: m.agentPromptInlineEligible(req.agentID)}
	m.agentPrompt = state
	if state.inline {
		m.openInlineAgentPrompt(state)
		return
	}
	if req.kind == agentPromptQuestions {
		m.openAgentQuestionPrompt(state)
		return
	}
	m.openAgentConfirmationPrompt(state)
}

func (m *App) agentPromptInlineEligible(agentID string) bool {
	return agentID != "" && m.activeAgentTabID() == agentID && m.chatHostVisible() &&
		(m.windowManager == nil || m.windowManager.Focused() == nil)
}

func (m *App) openInlineAgentPrompt(state *agentPromptState) {
	_, projection := m.sessionProjectionForAgent(state.request.agentID)
	if projection == nil {
		state.inline = false
		if state.request.kind == agentPromptQuestions {
			m.openAgentQuestionPrompt(state)
		} else {
			m.openAgentConfirmationPrompt(state)
		}
		return
	}
	if state.request.kind == agentPromptQuestions {
		n, err := projection.question.ResetWithError(state.request.questionsJSON)
		if err != nil || n == 0 {
			detail := "question payload could not be rendered"
			if err != nil {
				detail = err.Error()
			}
			m.RejectQuestion(state.request.agentID, state.request.blockID, detail)
			m.finishAgentPrompt()
			return
		}
		projection.questionAgentID = state.request.agentID
		projection.questionBlockID = state.request.blockID
		state.question = projection.question
		m.openSessionQuestionPanel(projection)
		return
	}
	req := state.request
	projection.confirmation.Reset(confirmation.Details{
		Question: req.question, Operation: req.operation, Tool: req.tool, Target: req.target,
		Risk: req.risk, Reason: req.reason, Suggestion: req.suggestion,
	})
	projection.confirmationAgentID = req.agentID
	projection.confirmationBlockID = req.blockID
	state.confirmation = projection.confirmation
	m.openSessionConfirmationPanel(projection)
}

func (m *App) openAgentConfirmationPrompt(state *agentPromptState) {
	req := state.request
	detail := strings.Join(nonemptyPromptParts(
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
	screen := question.New(m.Theme)
	count, err := screen.ResetWithError(state.request.questionsJSON)
	if err != nil || count == 0 {
		detail := "question payload could not be rendered"
		if err != nil {
			detail = err.Error()
		}
		m.RejectQuestion(state.request.agentID, state.request.blockID, detail)
		m.finishAgentPrompt()
		return
	}
	state.question = screen
	w, h := fitFloatingSize(m.width, m.height, 96, 24, 48, 12)
	frame := ui.NewWindowFrameWidget(m.Theme, ui.NewID(ui.RootID, "agent-question-frame"), ui.FrameWindow, "Questions", screen, nil, false, w, h)
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

func (m *App) resolveAgentPromptConfirmation(req agentPromptRequest, choice string) {
	m.OverlayState.SetBlocking(false)
	if m.ResolveConfirmation(req.agentID, req.blockID, choice) {
		if node := m.AgentMgr.Get(req.agentID); node != nil && node.State != nil {
			node.State.Running, node.State.Thinking = true, true
		}
	} else if m.Feedback != nil {
		m.Feedback.NotifyError("Permission response was not sent: the request is no longer active")
	}
}

func (m *App) resolveAgentQuestion(target *question.Screen, result question.Result, cancelled bool) {
	state := m.agentPrompt
	if state == nil || state.question != target {
		return
	}
	req := state.request
	m.OverlayState.SetBlocking(false)
	if !m.ResolveQuestion(req.agentID, req.blockID, result.Answers, result.Questions, cancelled) && m.Feedback != nil {
		m.Feedback.NotifyError("Question response was not sent: the request is no longer active")
	}
	m.finishAgentPrompt()
}

func (m *App) cancelAgentPrompt() {
	state := m.agentPrompt
	if state == nil {
		return
	}
	m.OverlayState.SetBlocking(false)
	if state.request.kind == agentPromptQuestions {
		m.ResolveQuestion(state.request.agentID, state.request.blockID, nil, nil, true)
	} else {
		m.cancelAgentTurn(state.request.agentID)
	}
	m.finishAgentPrompt()
}

func (m *App) finishAgentPrompt() {
	state := m.agentPrompt
	if state != nil && state.inline {
		_, projection := m.sessionProjectionForAgent(state.request.agentID)
		if projection != nil {
			if state.confirmation != nil {
				state.confirmation.Dismiss()
				m.closeProjectionPanel(projection, sessionConfirmationPanelID)
			}
			if state.question != nil {
				state.question.Dismiss()
				m.closeProjectionPanel(projection, sessionQuestionPanelID)
			}
		}
	} else {
		m.overlayHost.Close(ui.OverlayAgentPrompt)
	}
	m.agentPrompt = nil
	m.openNextAgentPrompt()
}

func (m *App) dismissAgentPromptFor(agentID string) {
	if state := m.agentPrompt; state != nil && state.request.agentID == agentID {
		if state.inline {
			_, projection := m.sessionProjectionForAgent(agentID)
			if projection != nil {
				if state.confirmation != nil {
					state.confirmation.Dismiss()
					m.closeProjectionPanel(projection, sessionConfirmationPanelID)
				}
				if state.question != nil {
					state.question.Dismiss()
					m.closeProjectionPanel(projection, sessionQuestionPanelID)
				}
			}
		} else {
			m.overlayHost.Close(ui.OverlayAgentPrompt)
		}
		m.agentPrompt = nil
	}
	filtered := m.agentPromptQueue[:0]
	for _, req := range m.agentPromptQueue {
		if req.agentID != agentID {
			filtered = append(filtered, req)
		}
	}
	m.agentPromptQueue = filtered
	m.openNextAgentPrompt()
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
