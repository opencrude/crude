package rendering

import (
	"crude/clipboard"
	tea "github.com/charmbracelet/bubbletea"
)

type ClipboardCopiedMsg struct{}
type ClipboardErrorMsg string

func CopyToClipboardCmd(text string) tea.Cmd {
	return func() tea.Msg {
		if err := clipboard.WriteText(text); err != nil {
			return ClipboardErrorMsg("failed to copy: " + err.Error())
		}
		return ClipboardCopiedMsg{}
	}
}
