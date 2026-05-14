package app

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	ttypes "crude/tui/types"
)

func (m *App) handleCancel(msg tea.KeyMsg) tea.Cmd {
	now := time.Now()

	if m.Mode == ModeSettings {
		m.settings.RestoreSnapshot()
		m.setMode(ModeChat)
		return nil
	}

	if m.Mode == ModeYolo {
		m.yoloDlg.Dismiss()
		m.setMode(ModeChat)
		return nil
	}

	if m.Mode == ModeConfirm {
		m.confirm.Dismiss()
		m.blocking = false
		m.setMode(ModeChat)
		if m.OnConfirmResult != nil {
			m.OnConfirmResult(ttypes.ConfirmResult{Choice: ttypes.ConfirmDeny})
		}
		return nil
	}

	if m.anyActive() {
		select {
		case m.cancelCh <- struct{}{}:
		default:
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
