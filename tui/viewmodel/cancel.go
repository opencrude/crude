package viewmodel

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m *App) handleCancel(msg tea.KeyPressMsg) tea.Cmd {
	if m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" {
		return nil
	}

	if m.OverlayState.Is(ModeSettings) {
		if settings := m.sessionSettings(); settings != nil {
			settings.RestoreSnapshot()
		}
		m.closeOverlayTab("Session Settings")
		return nil
	}

	if m.OverlayState.Is(ModeYolo) {
		if dialog := m.sessionYoloDialog(); dialog != nil {
			dialog.Dismiss()
		}
		m.StatusBar.SetConfirming(false)
		m.closeOverlayTab("YOLO")
		return nil
	}

	if m.OverlayState.Is(ModeConfirm) {
		if confirm := m.sessionConfirm(); confirm != nil {
			confirm.Dismiss()
		}
		m.OverlayState.SetBlocking(false)
		m.StatusBar.SetConfirming(false)
		m.closeOverlayTab("Confirm")
		m.ResolveConfirmation(m.AgentMgr.ActiveID(), "deny")
		m.StatusBar.SetInfo("denied")
		return nil
	}

	if m.OverlayState.Is(ModeQuestion) {
		if questionForm := m.sessionQuestionForm(); questionForm != nil {
			questionForm.Dismiss()
		}
		m.OverlayState.SetBlocking(false)
		m.StatusBar.SetConfirming(false)
		m.Input.ResetPrompt()
		m.Input.ClearInput()
		m.closeAllQuestionOverlayTabs()
		m.ResolveQuestion(m.AgentMgr.ActiveID(), nil, nil, true)
		return nil
	}

	if n := m.AgentMgr.ActiveNode(); n != nil && n.State != nil && !n.State.Completed && n.State.IsActive() {
		go m.CancelAgent(n.ID)
		n.State.Streaming = false
		n.State.Thinking = false
		m.StatusBar.SetInfo("cancelling agent...")
		return nil
	}

	if m.anyActive() {
		if n := m.AgentMgr.ActiveNode(); n != nil {
			select {
			case n.CancelChan <- struct{}{}:
			default:
			}
		}
		m.HardCancel()
		m.StatusBar.SetInfo("cancelling...")
		return nil
	}

	inputText := strings.TrimSpace(m.Input.Value())
	if inputText != "" {
		m.Input.ClearInput()
		m.Input.DismissHint()
		return nil
	}

	return nil
}
