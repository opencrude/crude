package viewmodel

import (
	"fmt"
	"path/filepath"

	"crude/tui/editor"
	"crude/tui/screens/editorchanges"
	"crude/tui/screens/shelleditor"
	"crude/tui/ui"
	"crude/tui/wm"
)

const editorChangesOverlay ui.OverlayID = "editor-changes"

type editorChangesView struct {
	source   *editor.Editor
	revision uint64
	index    int
	screen   *editorchanges.Screen
	hunks    []editor.GitHunk
}

func (m *App) inspectEditorChange(ed *editor.Editor, line int) {
	if ed == nil || m.overlayHost == nil {
		return
	}
	_, index, ok := ed.GitHunkAtLine(line)
	if !ok {
		if m.Feedback != nil {
			m.Feedback.NotifyInfo("No current change at this line")
		}
		return
	}
	m.openEditorChanges(ed, ed.GitHunks(), index, "HEAD")
}

func (m *App) openEditorChanges(ed *editor.Editor, hunks []editor.GitHunk, index int, baseline string) {
	hunk := hunks[index]
	m.overlayHost.Close(editorChangesOverlay)
	screen := editorchanges.New(m.Theme, hunk, baseline)
	m.editorChanges = &editorChangesView{source: ed, revision: ed.Revision(), index: index, screen: screen, hunks: append([]editor.GitHunk(nil), hunks...)}
	w, h := fitFloatingSize(m.width, m.height, 100, min(24, max(9, len(hunk.OldLines)+len(hunk.NewLines)+5)), 40, 9)
	title := fmt.Sprintf("Changes vs %s: %s", baseline, filepath.Base(ed.FilePath()))
	frame := ui.NewWindowFrameWidget(m.Theme, "editor-changes/frame", ui.FrameWindow, title, screen, nil, true, w, h)
	frame.Focus(shelleditor.ScreenID)
	win := wm.NewFloatingWindow(string(editorChangesOverlay), title, frame, w, h,
		wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true), wm.WithClosable(true), wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute))
	wm.CenterOnScreen(win, m.width, m.height)
	m.overlayHost.Open(editorChangesOverlay, win, ui.DefaultOverlayPolicy(), func() { screen.Close(); m.editorChanges = nil })
}

func (m *App) navigateEditorChanges(msg editorchanges.NavigateMsg) {
	view := m.editorChanges
	if view == nil || view.screen != msg.Target || !view.source.IsOpen() || view.source.Revision() != view.revision {
		return
	}
	hunks := view.hunks
	if len(hunks) == 0 {
		m.overlayHost.Close(editorChangesOverlay)
		return
	}
	view.index = (view.index + msg.Delta + len(hunks)) % len(hunks)
	view.screen.SetHunk(hunks[view.index])
	if win := m.overlayHost.Window(editorChangesOverlay); win != nil {
		win.MarkContentDirty()
	}
}

func (m *App) jumpEditorChange(direction int) {
	ed := m.EditorState.Editor
	if ed == nil {
		return
	}
	line, ok := ed.NextChange(ed.CursorLine(), direction)
	if !ok {
		if m.Feedback != nil {
			m.Feedback.NotifyInfo("No current changes")
		}
		return
	}
	ed.SetCursorPosition(line, 0)
	ed.ScrollToCursor()
	if m.Feedback != nil {
		m.Feedback.NotifyInfo(fmt.Sprintf("Change at line %d (vs HEAD)", line+1))
	}
}
