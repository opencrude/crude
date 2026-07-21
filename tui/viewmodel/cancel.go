package viewmodel

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m *App) handleCancel(msg tea.KeyPressMsg) tea.Cmd {
	if m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" {
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
