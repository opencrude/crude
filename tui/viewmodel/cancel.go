package viewmodel

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	ttypes "crude/tui/types"
)

func (m *App) handleCancel(msg tea.KeyPressMsg) tea.Cmd {
	if m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" {
		return nil
	}

	if m.OverlayState.Is(ModeSettings) {
		m.SessionSettings.RestoreSnapshot()
		m.closeOverlayTab("Session Settings")
		return nil
	}

	if m.OverlayState.Is(ModeYolo) {
		m.ActiveSession.YoloDlg.Dismiss()
		m.StatusBar.SetConfirming(false)
		m.closeOverlayTab("YOLO")
		return nil
	}

	if m.OverlayState.Is(ModeConfirm) {
		m.ActiveSession.Confirm.Dismiss()
		m.OverlayState.SetBlocking(false)
		m.StatusBar.SetConfirming(false)
		m.closeOverlayTab("Confirm")
		m.ActiveSession.OnConfirmResult(ttypes.ConfirmResult{Choice: ttypes.ConfirmDeny})
		if n := m.AgentMgr.ActiveNode(); n != nil {
			select {
			case n.CancelChan <- struct{}{}:
			default:
			}
		}
		m.StatusBar.SetInfo("cancelling...")
		return nil
	}

	if m.OverlayState.Is(ModeQuestion) {
		m.ActiveSession.QuestionForm.Dismiss()
		m.OverlayState.SetBlocking(false)
		m.StatusBar.SetConfirming(false)
		m.Input.ResetPrompt()
		m.Input.ClearInput()
		m.closeAllQuestionOverlayTabs()
		m.ActiveSession.OnQuestionResult(ttypes.QuestionResult{Cancelled: true})
		return nil
	}

	if n := m.AgentMgr.ActiveNode(); n != nil && n.State != nil && !n.State.Completed && n.State.IsActive() {
		go m.CancelSubAgent(n.ID)
		n.State.Streaming = false
		n.State.Thinking = false
		m.StatusBar.SetInfo("cancelling sub-agent...")
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
