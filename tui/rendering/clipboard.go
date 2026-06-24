package rendering

import (
	"crude/clipboard"
	tea "charm.land/bubbletea/v2"
)

// ClipboardCopiedMsg is sent after a successful clipboard write.
type ClipboardCopiedMsg struct{}

// ClipboardErrorMsg carries a clipboard-related error.
type ClipboardErrorMsg string

// ClipboardPasteMsg carries text read from the system clipboard.
// Sent only on success; failure is reported via ClipboardErrorMsg.
type ClipboardPasteMsg string

// CopyToClipboardCmd returns a tea.Cmd that writes the given text to
// the system clipboard. On success, returns ClipboardCopiedMsg; on
// failure, returns ClipboardErrorMsg.
func CopyToClipboardCmd(text string) tea.Cmd {
	return func() tea.Msg {
		if err := clipboard.WriteText(text); err != nil {
			return ClipboardErrorMsg("failed to copy: " + err.Error())
		}
		return ClipboardCopiedMsg{}
	}
}

// ReadFromClipboardCmd returns a tea.Cmd that reads text from the
// system clipboard. On success, returns ClipboardPasteMsg(text); on
// failure or empty clipboard, returns ClipboardErrorMsg.
func ReadFromClipboardCmd() tea.Cmd {
	return func() tea.Msg {
		data := clipboard.Read(clipboard.FmtText)
		if len(data) == 0 {
			return ClipboardErrorMsg("clipboard is empty or unavailable")
		}
		return ClipboardPasteMsg(string(data))
	}
}
