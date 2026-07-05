package viewmodel

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/workspace"
)

func (m *App) editorTabTitle(path string) string {
	return "editor:" + path
}

func (m *App) isEditorTabTitle(title string) bool {
	return len(title) > 7 && title[:7] == "editor:"
}

func (m *App) editorPathFromTabTitle(title string) string {
	if !m.isEditorTabTitle(title) {
		return ""
	}
	return title[7:]
}

func (m *App) openEditorTab(path string) (tea.Cmd, error) {
	tabTitle := m.editorTabTitle(path)

	// If the tab is already open, just activate it. Crucially, do NOT
	// reload the file — switching tabs must preserve cursor, scroll,
	// undo, and unsaved edits.
	if tab, ok := m.EditorState.Tabs[path]; ok && tab.Editor.IsOpen() {
		if m.Win.TabByTitle(tabTitle) == nil {
			// Tab exists in our map but the window tab was lost
			// (e.g. window tabs were reset). Re-create it.
			m.Win.OpenTab(tabTitle, "✎", true)
		}
		if winTab := m.Win.TabByTitle(tabTitle); winTab != nil {
			m.Win.SwitchTo(m.Win.IndexOf(winTab.ID))
			m.handleTabSwitch()
		}
		return nil, nil
	}

	// Get or create the shared buffer via BufferManager. Multiple tabs
	// viewing the same file share one Buffer (PieceTable + dirty flag +
	// diagnostics). The Editor wraps this buffer with view-specific state
	// (cursor, scroll, selection, undo history).
	buf, err := m.BufferMgr.OpenFile(path)
	if err != nil {
		return nil, err
	}

	// Create a fresh Editor for this tab and open the shared buffer.
	// Per-tab view state (cursor, scroll, undo, selection, chroma cache,
	// search) lives inside the Editor. Switching to another tab and back
	// gives back the same Editor instance with all state intact.
	ed := editor.New(m.Theme)
	ed.OpenBuffer(buf)

	// Wire content changes to LSP manager.
	ed.SetOnContentChanged(func(fp, text string) {
		lspCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		m.LSPMgr.UpdateFile(lspCtx, fp, text)
	})
	// Apply format-on-save preference from user settings. Defaults to off.
	if m.LastSettings.Editor.FormatOnSave {
		ed.SetFormatOnSave(true)
	} else {
		ed.SetFormatOnSave(false)
	}
	// Open the file on the LSP server.
	lspCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m.LSPMgr.OpenFile(lspCtx, path, ed.Value())

	// Apply cached diagnostics immediately so the editor is never empty
	// while waiting for the server to respond.
	if cached := m.LSPMgr.FileDiagnostics(path); len(cached) > 0 {
		ed.SetDiagnostics(cached)
	}

	displayTitle := ed.Title()

	// Store the tab and point m.EditorState.Editor at it.
	m.EditorState.Tabs[path] = workspace.NewEditorTab(path, ed)
	m.EditorState.Editor = ed
	m.EditorState.ActiveView = path

	if m.Win.TabByTitle(tabTitle) == nil {
		m.Win.OpenTab(tabTitle, "✎", true)
	}
	if winTab := m.Win.TabByTitle(tabTitle); winTab != nil {
		m.Win.SetTabDisplayTitleByIndex(m.Win.IndexOf(winTab.ID), displayTitle)
		m.Win.SwitchTo(m.Win.IndexOf(winTab.ID))
		m.handleTabSwitch()
	}

	return ed.BlinkCmd(), nil
}

// jumpToDiagnostic opens the file containing the given diagnostic
// (if not already open), positions the cursor on it, highlights the
// range, scrolls it into view, and refreshes the hover tooltip. The
// caller is responsible for choosing the diagnostic.
func (m *App) jumpToDiagnostic(filePath string, d editor.LSPDiagnostic) {
	if filePath == "" {
		return
	}
	// Open or activate the file. openEditorTab is a no-op if the
	// tab is already the active one.
	if _, err := m.openEditorTab(filePath); err != nil {
		m.StatusBar.SetError("Cannot open " + filePath)
		return
	}
	if m.EditorState.Editor == nil {
		return
	}
	m.EditorState.Editor.SetCursorPosition(d.Line, d.Col)
	m.EditorState.Editor.SetSelection(d.Line, d.Col, d.EndLine, d.EndCol)
	m.EditorState.Editor.ScrollToCursor()
	layout := m.computeLayout()
	m.EditorState.Editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
	m.contentDirty = true
}

// gotoFirstDiagnostic opens the first error diagnostic in the
// project (sorted by file path, then by line/col) and jumps to it.
// If a file is already open and has errors, that file's first error
// wins. If the project has no errors, shows a status bar message.
func (m *App) gotoFirstDiagnostic() {
	if m.LSPMgr == nil {
		m.StatusBar.SetInfo("LSP not configured")
		return
	}
	all := m.LSPMgr.AllDiagnostics()
	if len(all) == 0 {
		m.StatusBar.SetInfo("No errors in project")
		return
	}

	// Prefer an error in the currently-active file (if any). The
	// user's current focus is more important than file-path order.
	var paths []string
	if active := m.activeEditorPath(); active != "" {
		if diags, ok := all[active]; ok {
			if e := firstErrorOf(diags); e != nil {
				m.jumpToDiagnostic(active, *e)
				m.StatusBar.SetInfo(fmt.Sprintf("Jumped to %s:%d",
					filepath.Base(active), e.Line+1))
				return
			}
		}
		paths = append(paths, active)
	}
	// Collect remaining paths in sorted order so the navigation is
	// deterministic across runs.
	for p := range all {
		if p == m.activeEditorPath() {
			continue
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		e := firstErrorOf(all[p])
		if e == nil {
			continue
		}
		m.jumpToDiagnostic(p, *e)
		m.StatusBar.SetInfo(fmt.Sprintf("Jumped to %s:%d",
			filepath.Base(p), e.Line+1))
		return
	}

	m.StatusBar.SetInfo("No errors in project")
}

// firstErrorOf returns the first error-severity diagnostic in the
// given list (sorted by line then col, as LSP publishes them). The
// returned pointer aliases the input slice — safe to dereference
// before the next mutation.
func firstErrorOf(diags []editor.LSPDiagnostic) *editor.LSPDiagnostic {
	for i := range diags {
		if diags[i].Severity == editor.DiagError {
			return &diags[i]
		}
	}
	return nil
}

// jumpToNextError moves the cursor to the next error diagnostic in
// the currently-open file (strictly after the current cursor). If
// there is no editor, no file is open, or no further errors, shows
// a status bar message and returns false. Returns true on success.
func (m *App) jumpToNextError() bool {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
		m.StatusBar.SetInfo("No file open")
		return false
	}
	d := m.EditorState.Editor.NextError(m.EditorState.Editor.CursorLine(), m.EditorState.Editor.CursorCol())
	if d == nil {
		// Wrap around to the first error so F8 cycles through
		// every error like VSCode's command.
		d = m.EditorState.Editor.FirstError()
		if d == nil {
			m.StatusBar.SetInfo("No errors in file")
			return false
		}
	}
	m.EditorState.Editor.SetCursorPosition(d.Line, d.Col)
	m.EditorState.Editor.SetSelection(d.Line, d.Col, d.EndLine, d.EndCol)
	m.EditorState.Editor.ScrollToCursor()
	layout := m.computeLayout()
	m.EditorState.Editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
	m.StatusBar.SetInfo(fmt.Sprintf("Error at line %d: %s", d.Line+1, d.Message))
	m.contentDirty = true
	return true
}

// jumpToPrevError moves the cursor to the previous error diagnostic
// in the currently-open file (strictly before the current cursor).
// Mirrors jumpToNextError and wraps around to the last error.
func (m *App) jumpToPrevError() bool {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
		m.StatusBar.SetInfo("No file open")
		return false
	}
	d := m.EditorState.Editor.PrevError(m.EditorState.Editor.CursorLine(), m.EditorState.Editor.CursorCol())
	if d == nil {
		// Wrap to the last error in the file.
		diags := m.EditorState.Editor.Diagnostics()
		for i := len(diags) - 1; i >= 0; i-- {
			if diags[i].Severity == editor.DiagError {
				d = &diags[i]
				break
			}
		}
		if d == nil {
			m.StatusBar.SetInfo("No errors in file")
			return false
		}
	}
	m.EditorState.Editor.SetCursorPosition(d.Line, d.Col)
	m.EditorState.Editor.SetSelection(d.Line, d.Col, d.EndLine, d.EndCol)
	m.EditorState.Editor.ScrollToCursor()
	layout := m.computeLayout()
	m.EditorState.Editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
	m.StatusBar.SetInfo(fmt.Sprintf("Error at line %d: %s", d.Line+1, d.Message))
	m.contentDirty = true
	return true
}

func (m *App) closeEditorTab() {
	if m.activeEditorPath() == "" {
		return
	}
	path := m.activeEditorPath()

	// If the tab has unsaved edits, prompt before destroying the
	// buffer. The popup is modal; on confirm we re-enter with a
	// force flag.
	if tab, ok := m.EditorState.Tabs[path]; ok && tab.Editor != nil && tab.Editor.Modified() {
		m.showDirtyClosePrompt(path)
		return
	}

	m.closeEditorTabForce(path)
}

// closeEditorTabForce unconditionally closes the given editor tab.
// Used both for the normal close path and after the user confirms a
// close-with-unsaved-changes prompt.
func (m *App) closeEditorTabForce(path string) {
	// Close any popups associated with this editor before closing the tab.
	m.closeFindReplace()
	m.closeReferencesPopup()
	m.closeCompletionPopup()
	m.closeOutlinePopup()
	m.clearDiagHoverPopup()
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("diag-context")
	}

	tabTitle := m.editorTabTitle(path)
	if tab := m.Win.TabByTitle(tabTitle); tab != nil {
		m.Win.CloseTabByID(tab.ID)
	}

	tab, ok := m.EditorState.Tabs[path]
	if ok {
		lspCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		m.LSPMgr.CloseFile(lspCtx, path)
		tab.Editor.Close()
		delete(m.EditorState.Tabs, path)
	}

	// Pick a new active editor tab if any remain. The window tab
	// switcher has already advanced ActiveIndex; the next call to
	// handleTabSwitch() will look up the new active path in the map.
	m.handleTabSwitch()

	if m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" {
		if m.OnListSessions != nil {
			m.launcher = NewLauncherState(m.OnListSessions())
			m.LastInteractionTime = time.Now()
		}
	}
}

func (m *App) activeEditorPath() string {
	return m.EditorState.ActiveView
}

func (m *App) closeAllEditorTabsExcept(path string) {
	toClose := []string{}
	for p := range m.EditorState.Tabs {
		if p != path {
			toClose = append(toClose, p)
		}
	}
	for _, p := range toClose {
		m.closeEditorTabForce(p)
	}
}

// closeEditorTabsToRightOf closes every tab whose index is greater
// than the given window-tab index.
func (m *App) closeEditorTabsToRightOf(idx int) {
	tabs := m.Win.Tabs()
	for i := len(tabs) - 1; i > idx; i-- {
		t := tabs[i]
		if m.isEditorTabTitle(t.Title) {
			p := m.editorPathFromTabTitle(t.Title)
			m.closeEditorTabForce(p)
		}
	}
}

func (m *App) closeAllWindows() {
	for _, w := range m.windowManager.Windows() {
		m.windowManager.RemoveWindow(w.ID)
	}
}
