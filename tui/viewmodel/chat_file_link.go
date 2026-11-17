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
	if !ok || m.ws == nil || m.Win == nil {
		return nil
	}
	path := ref.Path
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
		if ref.Line > 0 {
			msg.filePosition = &editor.Position{Line: ref.Line - 1, Col: max(0, ref.Column-1)}
		}
		return msg
	}
}
