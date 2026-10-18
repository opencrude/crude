package viewmodel

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	findinfiles "crude/tui/screens/findinfiles"
	"crude/tui/ui"
	"crude/tui/wm"
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
	root := m.ws.RootPath()
	if root == "" {
		root = "."
	}
	search := m.findInFiles
	inside := false
	if fw := m.focusedPopup(); fw != nil {
		inside = fw.ID == findInFilesWindowID || fw.ID == replaceInFilesWindowID
	}
	if search == nil {
		search = findinfiles.New(m.Theme)
		search.Open(root)
		search.SetQuery(m.projectSearchDraft.query)
		search.SetReplacement(m.projectSearchDraft.replacement)
		opts := m.projectSearchDraft.options
		search.SetOptions(opts.CaseSensitive, opts.WholeWord, opts.Regex)
	}
	seeded := false
	if !inside && m.EditorState != nil && m.EditorState.Editor != nil && m.EditorState.Editor.HasSelection() {
		query, regex := selectedSearchQuery(m.EditorState.Editor.SelectedText(), search.Regex())
		search.SetQuery(query)
		search.SetOptions(search.CaseSensitive(), search.WholeWord(), regex)
		seeded = true
	}
	search.SelectQuery()
	if m.overlayHost.IsOpen(overlay) {
		m.windowManager.FocusWindow(string(overlay))
		m.markProjectSearchDirty()
		if seeded {
			return m, search.ScheduleSearch()
		}
		return m, nil
	}
	// A mode transition remounts the same state/edit owners, without running
	// their close lifecycle or cancelling an otherwise valid search.
	m.findInFiles = nil
	m.closeFindInFiles()
	search.SetMode(mode)

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
		if m.findInFiles != searchCopy {
			return
		}
		m.projectSearchDraft = projectSearchDraft{query: searchCopy.Query(), replacement: searchCopy.Replacement(), options: searchCopy.Options()}
		m.ws.CancelProjectSearch()
		m.findInFilesRequest = 0
		m.findInFilesGen = 0
		if searchCopy != nil {
			searchCopy.Close()
		}
		m.findInFiles = nil
	})
	m.findInFiles = search
	var cmd tea.Cmd
	if search.Query() != "" && (seeded || search.Generation() == 0) {
		cmd = search.ScheduleSearch()
	}
	return m, cmd
}

type projectSearchDraft struct {
	query, replacement string
	options            fileio.ProjectSearchOptions
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
