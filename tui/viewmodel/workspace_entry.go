package viewmodel

import (
	"context"
	"fmt"
	"path"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"crude/core/shellport"
	"crude/types/statuscenter"
)

// SetLaunchFile selects the named file when this client restores its view.
func (m *App) SetLaunchFile(path string, missing bool) {
	m.launchFile, m.launchFileMissing = path, missing
}

const entryNoticeSource = "workspace-entry"

const openEntryFolderAction = "workspace:open-entry-folder"

func (m *App) fileOnly() bool {
	entry, ok := m.ws.(shellport.FolderEntry)
	return ok && entry.FileOnly()
}

func (m *App) entryName(value string) string {
	if files, ok := m.ws.(shellport.ClientFiles); ok {
		return path.Base(files.CanonicalClientPath(value))
	}
	return filepath.Base(value)
}

type entryFolderOpenedMsg struct {
	owner shellport.Workspace
	err   error
}

func (m *App) openEntryFolder() tea.Cmd {
	port, ok := m.ws.(shellport.FolderEntry)
	if !ok || !port.FileOnly() || m.workspaceAttachPending || m.openingEntry == m.ws {
		return nil
	}
	owner := m.ws
	m.openingEntry = owner
	return m.clientIOCommand(context.Background(), func(ctx context.Context) tea.Msg {
		return entryFolderOpenedMsg{owner, port.OpenFolder(ctx)}
	})
}

func (m *App) postEntryNotice(summary, detail string, file bool) {
	root := m.ws.RootPath()
	actions := []statuscenter.Action{{ID: "settings:workspace", Label: "Open Workspace Settings", Enabled: true}}
	if file {
		actions = append([]statuscenter.Action{{ID: openEntryFolderAction, Label: "Open folder as workspace", Enabled: true}}, actions...)
	}
	m.ws.Status().Notify(statuscenter.NotificationRecord{Owner: statuscenter.ResourceRef{Kind: statuscenter.ResourceWorkspace, ID: root},
		DedupKey: "workspace-entry:" + root, Source: entryNoticeSource, Severity: statuscenter.SeverityInfo, Summary: summary, Detail: detail, Actions: actions})
}

func (m *App) showEntryFolderNotice() {
	if m.launchFile != "" || m.ws.Status() == nil {
		return
	}
	s := m.ws.LoadAppSettings()
	folder := s.Entry.FolderSummary()
	if folder == "" {
		return
	}
	summary := ""
	switch {
	case !s.LocalHistory.EnabledValue() && !s.LSP.Enabled:
		summary = "Local History and language servers are off."
	case !s.LocalHistory.EnabledValue():
		summary = "Local History is off."
	case !s.LSP.Enabled:
		summary = "Language servers are off."
	default:
		return
	}
	m.postEntryNotice(summary, folder+" Checking this folder can read many files. Change these settings in Workspace Settings.", false)
}

func (m *App) showFileEntryNotice(file string) {
	s := m.ws.LoadAppSettings()
	detail := "Language servers are off for this file."
	if m.Tree != nil && !m.Tree.Visible {
		detail += " Press Ctrl+B to show the folder tree."
	}
	if folder := s.Entry.FolderSummary(); folder != "" {
		detail += " " + folder
	}
	if !s.LocalHistory.EnabledValue() {
		detail += " Local History is off here."
	}
	m.postEntryNotice(fmt.Sprintf("Opened %s in %s.", m.entryName(file), m.entryName(m.ws.RootPath())), detail, true)
}
