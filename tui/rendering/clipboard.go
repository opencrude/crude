package rendering

import (
	"os"
	"runtime"

	tea "charm.land/bubbletea/v2"
)

// ClipboardWriteMsg asks the shell to publish text through its clipboard owner.
type ClipboardWriteMsg string

// CopyToClipboardCmd emits a copy intent. Controls with a shell copy callback
// use it to admit the intent before Bubble Tea schedules any command.
func CopyToClipboardCmd(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	return func() tea.Msg { return ClipboardWriteMsg(text) }
}

// RemoteTerminalSession reports a Unix session reached over SSH, whose
// system clipboard is not the user's.
func RemoteTerminalSession() bool {
	if runtime.GOOS == "windows" {
		return false
	}
	return os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != ""
}
