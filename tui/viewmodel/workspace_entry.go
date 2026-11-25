package viewmodel

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SetLaunchFile selects the named file when this client restores its view.
func (m *App) SetLaunchFile(path string, missing bool) {
	m.launchFile, m.launchFileMissing = path, missing
}

func (m *App) showEntryFolderNotice() {
	if m.launchFile != "" || m.Feedback == nil {
		return
	}
	s := m.ws.LoadAppSettings()
	folder := s.Entry.FolderSummary()
	if folder == "" {
		return
	}
	var off []string
	if !s.LocalHistory.EnabledValue() {
		off = append(off, "Local History")
	}
	if !s.LSP.Enabled {
		off = append(off, "language servers")
	}
	if len(off) > 0 {
		m.Feedback.NotifyInfo(folder + " Crude turned off " + strings.Join(off, " and ") + " here. Change this in Workspace Settings.")
	}
}
func (m *App) showFileEntryNotice(path string) {
	s := m.ws.LoadAppSettings()
	reason := "Language servers are off for this file."
	if s.Entry.FolderKind != "" && !s.LocalHistory.EnabledValue() {
		reason = "Local History and language servers are off here."
	}
	m.Feedback.NotifyInfo(fmt.Sprintf("Opened %s in %s. %s", filepath.Base(path), filepath.Base(m.ws.RootPath()), reason))
}
