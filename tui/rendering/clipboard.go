package rendering

import (
	"os"
	"runtime"

	tea "charm.land/bubbletea/v2"

	"crude/clipboard"
)

// ClipboardCopiedMsg is sent after a clipboard write. Terminal reports that
// the text went out as an OSC 52 terminal clipboard write.
type ClipboardCopiedMsg struct {
	Terminal bool
}

// ClipboardErrorMsg carries a clipboard-related error.
type ClipboardErrorMsg string

// CopyToClipboardCmd returns a tea.Cmd that writes the given text to the
// clipboard. The system clipboard is written first. When it is unreachable
// (no wl-copy/xclip/pbcopy, a headless tmux) or the session is remote (SSH,
// where the system clipboard belongs to the server), the text also goes out
// as an OSC 52 terminal clipboard write, which local terminals and tmux
// (set-clipboard) deliver to the user's clipboard.
func CopyToClipboardCmd(text string) tea.Cmd {
	return func() tea.Msg {
		err := clipboard.WriteText(text)
		if err == nil && !RemoteTerminalSession() {
			return ClipboardCopiedMsg{}
		}
		return tea.BatchMsg{
			tea.SetClipboard(text),
			func() tea.Msg { return ClipboardCopiedMsg{Terminal: err != nil} },
		}
	}
}

// RemoteTerminalSession reports a Unix session reached over SSH, whose
// system clipboard is not the user's.
func RemoteTerminalSession() bool {
	if runtime.GOOS == "windows" {
		return false
	}
	return os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != ""
}
