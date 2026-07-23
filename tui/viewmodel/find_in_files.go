package viewmodel

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/tui/rendering"
	findinfiles "crude/tui/screens/findinfiles"
	"crude/tui/ui"
	"crude/tui/wm"
)

const findInFilesWindowID = "find-in-files"

func (m *App) toggleFindInFiles() (tea.Model, tea.Cmd) {
	if m.overlayHost == nil {
		return m, nil
	}
	if m.overlayHost.IsOpen(ui.OverlayFindInFiles) {
		m.closeFindInFiles()
		return m, nil
	}

	root := m.RootPath
	if root == "" {
		root = "."
	}
	search := findinfiles.New(m.Theme)
	search.Open(root)
	w := max(68, min(m.width*84/100, 150))
	h := max(20, min(m.height*78/100, 46))
	if w > m.width-4 {
		w = max(30, m.width-4)
	}
	if h > m.height-4 {
		h = max(12, m.height-4)
	}
	frame := ui.NewWindowFrameWidget(
		m.Theme,
		ui.NewID(ui.RootID, "find-in-files-frame"),
		ui.FrameWindow,
		"Find in Files",
		search,
		nil,
		true,
		w,
		h,
	)
	fw := wm.NewFloatingWindow(string(ui.OverlayFindInFiles), "Find in Files", frame, w, h,
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
	m.overlayHost.Open(ui.OverlayFindInFiles, fw, ui.DefaultOverlayPolicy(), func() {
		m.CancelProjectSearch()
		m.findInFilesRequest = 0
		m.findInFilesGen = 0
		if searchCopy != nil {
			searchCopy.Close()
		}
		m.findInFiles = nil
	})
	m.findInFiles = search
	return m, nil
}

func (m *App) closeFindInFiles() {
	m.overlayHost.Close(ui.OverlayFindInFiles)
}

func (m *App) openFindInFilesResult(match fileio.ProjectSearchMatch) tea.Cmd {
	m.closeFindInFiles()
	path := filepath.Clean(match.Path)
	cmd, err := m.openEditorTab(path)
	if err != nil {
		m.StatusBar.SetError("Cannot open " + match.RelativePath)
		return nil
	}
	if m.EditorState.Editor == nil {
		return cmd
	}
	line := match.Line - 1
	if line < 0 {
		line = 0
	}
	column := rendering.RuneIdxToCol([]rune(match.LineText), match.Column)
	m.EditorState.Editor.SetCursorPosition(line, column)
	m.EditorState.Editor.ScrollToCursor()
	m.contentDirty = true
	return cmd
}
