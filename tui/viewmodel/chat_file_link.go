package viewmodel

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"crude/internal/filelink"
	"crude/shellport"
	"crude/tui/editor"
)

func (m *App) openChatFile(target string) tea.Cmd {
	ref, ok := filelink.Parse(target)
	if !ok {
		return nil
	}
	return m.openSourceLocation(ref.Path, ref.Line, ref.Column)
}

// openSourceLocation opens a file at a one-based line and column (zero means
// none) in the pane that asked, through the shared pane-file owner.
func (m *App) openSourceLocation(path string, line, column int) tea.Cmd {
	if m.ws == nil || m.Win == nil || path == "" {
		return nil
	}
	if files, ok := m.ws.(shellport.ClientFiles); ok {
		path = files.CanonicalClientPath(path)
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(m.ws.RootPath(), filepath.FromSlash(path))
	}
	command := m.openPaneFiles([]string{path}, m.Win.ActiveGroup())
	if command == nil {
		return nil
	}
	return func() tea.Msg {
		msg := command().(paneFilesOpenedMsg)
		if line > 0 {
			msg.filePosition = &editor.Position{Line: line - 1, Col: max(0, column-1)}
		}
		return msg
	}
}
