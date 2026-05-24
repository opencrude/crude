package viewmodel

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	ttypes "crude/tui/types"
)

func (m *App) handleCancel(msg tea.KeyPressMsg) tea.Cmd {
	now := time.Now()

	if m.Mode == ModeSettings {
		m.settings.RestoreSnapshot()
		m.closeOverlayTab("Settings")
		return nil
	}

	if m.Mode == ModeYolo {
		m.yoloDlg.Dismiss()
		m.statusbar.SetConfirming(false)
		m.closeOverlayTab("YOLO")
		return nil
	}

	if m.Mode == ModeConfirm {
		m.confirm.Dismiss()
		m.blocking = false
		m.statusbar.SetConfirming(false)
		m.closeOverlayTab("Confirm")
		if m.OnConfirmResult != nil {
			m.OnConfirmResult(ttypes.ConfirmResult{Choice: ttypes.ConfirmDeny})
		}
		select {
		case m.cancelCh <- struct{}{}:
		default:
		}
		m.statusbar.SetInfo("cancelling...")
		return nil
	}

	if m.Mode == ModeQuestion {
		m.questionForm.Dismiss()
		m.blocking = false
		m.statusbar.SetConfirming(false)
		m.input.ResetPrompt()
		m.input.ClearInput()
		m.closeAllQuestionOverlayTabs()
		if m.OnQuestionResult != nil {
			m.OnQuestionResult(ttypes.QuestionResult{Cancelled: true})
		}
		return nil
	}

	if m.activeAgentView != "" && m.activeAgentView != MainAgentID {
		if agent, ok := m.agents[m.activeAgentView]; ok && !agent.Completed && agent.IsActive() {
			if m.OnCancelSubAgent != nil {
				go m.OnCancelSubAgent(m.activeAgentView)
			}
			agent.Streaming = false
			agent.Thinking = false
			m.statusbar.SetInfo("cancelling sub-agent...")
			return nil
		}
	}

	if m.anyActive() {
		select {
		case m.cancelCh <- struct{}{}:
		default:
		}
		if m.OnHardCancel != nil {
			m.OnHardCancel()
		}
		m.statusbar.SetInfo("cancelling...")
		return nil
	}

	inputText := strings.TrimSpace(m.input.Value())
	if inputText != "" {
		m.input.ClearInput()
		m.input.DismissHint()
		return nil
	}

	if now.Sub(m.lastCancelTime) < 2*time.Second {
		m.Quit = true
		return tea.Quit
	}
	m.lastCancelTime = now
	m.statusbar.SetError("press ctrl+c or esc again to exit")
	return nil
}
