package viewmodel

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/screens/findinfiles"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/tui/wm"
	"crude/workspace"
	"crude/workspace/fileio"
)

const findInFilesWindowID = "find-in-files"
const replaceInFilesWindowID = "replace-in-files"

func overlayForProjectSearch(mode findinfiles.Mode) ui.OverlayID {
	if mode == findinfiles.ModeReplace {
		return ui.OverlayReplaceInFiles
	}
	return ui.OverlayFindInFiles
}

// toggleGoToFile is the Go to File command.
func (m *App) toggleGoToFile() (tea.Model, tea.Cmd) {
	model, cmd := m.toggleProjectSearch(findinfiles.ModeFiles)
	m.goToFileLanding = goToFileLanding{}
	return model, cmd
}

func (m *App) toggleFindInFiles() (tea.Model, tea.Cmd) {
	return m.toggleProjectSearch(findinfiles.ModeFind)
}

func (m *App) toggleReplaceInFiles() (tea.Model, tea.Cmd) {
	return m.toggleProjectSearch(findinfiles.ModeReplace)
}

// openGoToFile opens Go to File seeded with query. A landing line (one-based,
// zero for none) places every opened result at the cited line and column.
func (m *App) openGoToFile(query string, line, column int) tea.Cmd {
	_, cmd := m.toggleProjectSearch(findinfiles.ModeFiles)
	m.goToFileLanding = goToFileLanding{line: line, column: column, group: m.Win.ActiveGroup()}
	if m.findInFiles == nil {
		return cmd
	}
	m.findInFiles.SetOptions(false, false, false)
	m.findInFiles.SetQuery(query)
	m.findInFiles.SelectQuery()
	return tea.Batch(cmd, m.findInFiles.ScheduleSearch())
}

// goToFileLanding is the cited position Go to File results open at.
type goToFileLanding struct {
	catalog      *workspace.BrowserCatalog
	external     bool
	line, column int
	group        window.GroupID
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
	if search != nil && (search.Mode() == findinfiles.ModeFiles) != (mode == findinfiles.ModeFiles) {
		// Go to File and Find in Files keep separate queries: close the other
		// one through its normal lifecycle, which keeps the Find draft.
		m.closeFindInFiles()
		search = m.findInFiles
	}
	if search == nil {
		search = findinfiles.New(m.Theme)
		search.Open(root)
		search.SetQuery(m.projectSearchDraft.query)
		if mode == findinfiles.ModeFiles {
			search.SetQuery("")
		}
		search.SetReplacement(m.projectSearchDraft.replacement)
		opts := m.projectSearchDraft.options
		search.SetOptions(opts.CaseSensitive, opts.WholeWord, opts.Regex)
	}
	seeded := false
	if mode != findinfiles.ModeFiles && !inside && m.EditorState != nil && m.EditorState.Editor != nil && m.EditorState.Editor.HasSelection() {
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
		if searchCopy.Mode() != findinfiles.ModeFiles {
			m.projectSearchDraft = projectSearchDraft{query: searchCopy.Query(), replacement: searchCopy.Replacement(), options: searchCopy.Options()}
		}
		m.goToFileLanding = goToFileLanding{}
		m.retireFileSearchPreview()
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

func (m *App) openSearchLocation(match fileio.ProjectSearchMatch, position *editor.Position) tea.Cmd {
	// Closing the window ends its landing; a chat reference's cited place
	// applies to the file name results it offered.
	landing := m.goToFileLanding
	m.closeFindInFiles()
	match.Path = m.chatFilePath(match.Path)
	group := m.Win.ActiveGroup()
	if landing.group != 0 {
		group = landing.group
	}
	if !m.Win.HasGroup(group) {
		return nil
	}
	m.unfocus()
	cmd := m.openPaneFilesChecked(context.Background(), []string{match.Path}, group, true)
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		message := cmd().(paneFilesOpenedMsg)
		// The worker resolves the core host's actual document identity. On a
		// Windows core it can have backslashes even if search returned slashes.
		if len(message.files) == 1 && message.files[0].err == nil {
			match.Path = message.files[0].path
		}
		message.landing = &match
		if position != nil {
			message.clickedSource = true
			message.filePosition = position
		} else if match.NameOnly && landing.line > 0 {
			message.filePosition = &editor.Position{Line: landing.line - 1, Col: max(0, landing.column-1)}
		}
		return message
	}
}

type catalogSearchRows struct {
	request findinfiles.SearchRequestMsg
	rows    []fileio.ProjectSearchMatch
	err     error
}

func (m *App) rankGoToFiles(request findinfiles.SearchRequestMsg, catalog *workspace.BrowserCatalog) tea.Cmd {
	return m.clientIOCommand(context.Background(), func(ctx context.Context) tea.Msg {
		files := make([]fileio.ProjectFile, 0, len(catalog.Entries))
		for _, entry := range catalog.Entries {
			if !entry.IsDir {
				files = append(files, fileio.ProjectFile{Path: entry.Path, Rel: entry.Relative})
			}
		}
		rows, err := fileio.SearchProjectFiles(ctx, files, request.Query, request.Options)
		return catalogSearchRows{request, rows, err}
	})
}
