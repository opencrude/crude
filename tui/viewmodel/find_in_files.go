package viewmodel

import (
	"fmt"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	findinfiles "crude/tui/screens/findinfiles"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/workspace"
)

const findInFilesWindowID = "find-in-files"
const replaceInFilesWindowID = "replace-in-files"

func overlayForProjectSearch(mode findinfiles.Mode) ui.OverlayID {
	if mode == findinfiles.ModeReplace {
		return ui.OverlayReplaceInFiles
	}
	return ui.OverlayFindInFiles
}

func (m *App) toggleFindInFiles() (tea.Model, tea.Cmd) {
	return m.toggleProjectSearch(findinfiles.ModeFind)
}

func (m *App) toggleReplaceInFiles() (tea.Model, tea.Cmd) {
	return m.toggleProjectSearch(findinfiles.ModeReplace)
}

func (m *App) toggleProjectSearch(mode findinfiles.Mode) (tea.Model, tea.Cmd) {
	if m.overlayHost == nil {
		return m, nil
	}
	overlay := overlayForProjectSearch(mode)
	if m.overlayHost.IsOpen(overlay) {
		m.closeFindInFiles()
		return m, nil
	}
	query, replacement, caseSensitive, regex := m.captureProjectSearch()
	m.closeFindInFiles()

	root := m.RootPath
	if root == "" {
		root = "."
	}
	search := findinfiles.New(m.Theme)
	if mode == findinfiles.ModeReplace {
		search.OpenReplace(root)
	} else {
		search.Open(root)
	}
	if query != "" {
		search.SetQuery(query)
	}
	if replacement != "" {
		search.SetReplacement(replacement)
	}
	search.SetOptions(caseSensitive, regex)

	w := max(68, min(m.width*84/100, 150))
	h := max(20, min(m.height*78/100, 46))
	if w > m.width-4 {
		w = max(30, m.width-4)
	}
	if h > m.height-4 {
		h = max(12, m.height-4)
	}
	title := mode.Title()
	frame := ui.NewWindowFrameWidget(
		m.Theme,
		ui.NewID(ui.RootID, string(overlay)+"-frame"),
		ui.FrameWindow,
		title,
		search,
		nil,
		true,
		w,
		h,
	)
	fw := wm.NewFloatingWindow(string(overlay), title, frame, w, h,
		wm.WithDecoration(true),
		wm.WithMovable(true),
		wm.WithResizable(true),
		wm.WithClosable(true),
		wm.WithFocusable(true),
		wm.WithMinSize(min(60, w), min(16, h)),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	search.SetFocusHandler(frame.Focus)
	search.FocusQuery()
	searchCopy := search
	m.overlayHost.Open(overlay, fw, ui.DefaultOverlayPolicy(), func() {
		m.CancelProjectSearch()
		m.findInFilesRequest = 0
		m.findInFilesGen = 0
		if searchCopy != nil {
			searchCopy.Close()
		}
		m.findInFiles = nil
	})
	m.findInFiles = search
	var cmd tea.Cmd
	if query != "" {
		cmd = search.ScheduleSearch()
	}
	return m, cmd
}

func (m *App) captureProjectSearch() (query, replacement string, caseSensitive, regex bool) {
	if m.findInFiles == nil {
		return "", "", false, false
	}
	return m.findInFiles.Query(), m.findInFiles.Replacement(), m.findInFiles.CaseSensitive(), m.findInFiles.Regex()
}

func (m *App) closeFindInFiles() {
	if m.overlayHost == nil {
		return
	}
	m.overlayHost.Close(ui.OverlayFindInFiles)
	m.overlayHost.Close(ui.OverlayReplaceInFiles)
}

func (m *App) markProjectSearchDirty() {
	if m.windowManager == nil {
		return
	}
	for _, id := range []string{findInFilesWindowID, replaceInFilesWindowID} {
		if fw := m.windowManager.Window(id); fw != nil {
			fw.MarkContentDirty()
		}
	}
}

func (m *App) openFindInFilesResult(match fileio.ProjectSearchMatch) tea.Cmd {
	m.closeFindInFiles()
	path, err := filepath.Abs(match.Path)
	if err != nil {
		m.Feedback.NotifyError("Cannot open " + match.RelativePath)
		return nil
	}
	match.Path = filepath.Clean(path)
	group := m.Win.ActiveGroup()
	if tab := m.Win.TabByTitle(m.editorTabTitle(match.Path)); tab != nil {
		group = tab.Group
	}
	m.unfocus()
	cmd := m.openPaneFiles([]string{match.Path}, group)
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		message := cmd().(paneFilesOpenedMsg)
		message.landing = &match
		return message
	}
}

func (m *App) applyProjectReplace(msg findinfiles.ReplaceMsg) tea.Cmd {
	if m.findInFiles == nil || msg.Screen != m.findInFiles {
		return nil
	}
	matches := m.findInFiles.Results()
	if !msg.All {
		matches = []fileio.ProjectSearchMatch{msg.Match}
	}
	edits, skipped, err := fileio.PlanProjectReplace(matches, msg.Query, msg.Replacement, msg.Options)
	if err != nil {
		m.findInFiles.SetNotice("Replace failed")
		if m.Feedback != nil {
			m.Feedback.NotifyError(err.Error())
		}
		m.markProjectSearchDirty()
		return nil
	}
	replaced, files := 0, 0
	disk := make([]workspace.FileEdit, 0)
	for _, edit := range edits {
		if m.applyReplacementToOpenBuffer(edit) {
			replaced += edit.Count
			files++
			continue
		}
		disk = append(disk, workspace.FileEdit{
			Path:       edit.Path,
			OldContent: edit.OldContent,
			NewContent: edit.NewContent,
		})
		replaced += edit.Count
		files++
	}
	if len(disk) > 0 {
		result := m.ApplyWorkspaceEdit(disk)
		if !result.Succeeded() && m.Feedback != nil {
			m.Feedback.NotifyError(result.Error())
		}
	}
	notice := fmt.Sprintf("Replaced %d in %d files", replaced, files)
	if skipped > 0 {
		notice += fmt.Sprintf(" · skipped %d", skipped)
	}
	if replaced == 0 && skipped == 0 {
		notice = "Nothing to replace"
	}
	m.findInFiles.SetNotice(notice)
	if m.Feedback != nil {
		m.Feedback.NotifyInfo(notice)
	}
	m.markProjectSearchDirty()
	return m.findInFiles.ScheduleSearch()
}

func (m *App) applyReplacementToOpenBuffer(edit fileio.ProjectFileReplacement) bool {
	path := filepath.Clean(edit.Path)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if m.EditorState != nil {
		if tab, ok := m.EditorState.Tabs[path]; ok && tab != nil && tab.Editor != nil {
			tab.Editor.ReplaceDocument(edit.NewContent)
			return true
		}
	}
	if m.BufferMgr != nil {
		if buf := m.BufferMgr.Get(path); buf != nil {
			buf.ReplaceContents(edit.NewContent)
			return true
		}
	}
	return false
}
