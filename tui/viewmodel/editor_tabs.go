package viewmodel

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/ui"
	"crude/tui/window"
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
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	path = filepath.Clean(absPath)
	if tab := m.EditorState.Tabs[path]; tab != nil && tab.Editor.IsOpen() {
		m.activateEditorTab(path)
		return nil, nil
	}
	buf, err := m.BufferMgr.OpenFile(path)
	if err != nil {
		return nil, err
	}
	m.installEditorBuffer(buf)
	m.activateEditorTab(path)
	return m.refreshEditorGitCmd(), nil
}

// installEditorBuffer installs a prepared live buffer without changing selection.
// Existing views retain their cursor, undo, selection and unsaved text.
func (m *App) installEditorBuffer(buf *editor.Buffer) {
	path := buf.Path
	if tab := m.EditorState.Tabs[path]; tab != nil && tab.Editor.IsOpen() {
		return
	}

	// Create a fresh Editor for this tab and open the shared buffer.
	// Per-tab view state (cursor, scroll, undo, selection, chroma cache,
	// search) lives inside the Editor. Switching to another tab and back
	// gives back the same Editor instance with all state intact.
	ed := editor.New(m.Theme)
	ed.OpenBuffer(buf)

	// Wire content changes to LSP manager.
	ed.SetOnContentChanged(func(fp, text string) { m.onEditorContentChanged(ed, fp, text) })
	// Register the exact editor lifetime immediately. The manager owns async
	// discovery/start; a close cannot be overtaken by an old tea.Cmd didOpen.
	if m.LSPMgr != nil {
		m.LSPMgr.OpenFile(context.Background(), path, ed.Value())
	}

	// Apply cached diagnostics immediately so the editor is never empty
	// while waiting for the server to respond.
	if m.LSPMgr != nil {
		if cached := m.LSPMgr.FileDiagnostics(path); len(cached) > 0 {
			ed.SetDiagnostics(cached)
		}
	}

	m.EditorState.Tabs[path] = NewEditorTab(path, ed)
}

func (m *App) activateEditorTab(path string) {
	tabTitle := m.editorTabTitle(path)
	if m.Win.TabByTitle(tabTitle) == nil {
		m.Win.OpenTab(tabTitle, "✎", true)
	}
	if winTab := m.Win.TabByTitle(tabTitle); winTab != nil {
		m.Win.SetTabDisplayTitleByIndex(m.Win.IndexOf(winTab.ID), m.EditorState.Tabs[path].Editor.BaseTitle())
		m.Win.SwitchTo(m.Win.IndexOf(winTab.ID))
		m.handleTabSwitch()
	}
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
		m.Feedback.NotifyError("Cannot open " + filePath)
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
}

// gotoFirstDiagnostic opens the first error diagnostic in the
// project (sorted by file path, then by line/col) and jumps to it.
// If a file is already open and has errors, that file's first error
// wins. If the project has no errors, shows a status bar message.
func (m *App) gotoFirstDiagnostic() {
	if m.LSPMgr == nil {
		m.Feedback.NotifyInfo("LSP not configured")
		return
	}
	all := m.LSPMgr.AllDiagnostics()
	if len(all) == 0 {
		m.Feedback.NotifyInfo("No errors in project")
		return
	}

	// Prefer an error in the currently-active file (if any). The
	// user's current focus is more important than file-path order.
	var paths []string
	if active := m.activeEditorPath(); active != "" {
		if diags, ok := all[active]; ok {
			if e := firstErrorOf(diags); e != nil {
				m.jumpToDiagnostic(active, *e)
				m.Feedback.NotifyInfo(fmt.Sprintf("Jumped to %s:%d",
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
		m.Feedback.NotifyInfo(fmt.Sprintf("Jumped to %s:%d",
			filepath.Base(p), e.Line+1))
		return
	}

	m.Feedback.NotifyInfo("No errors in project")
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
		m.Feedback.NotifyInfo("No file open")
		return false
	}
	d := m.EditorState.Editor.NextError(m.EditorState.Editor.CursorLine(), m.EditorState.Editor.CursorCol())
	if d == nil {
		// Wrap around to the first error so F8 cycles through
		// every error like VSCode's command.
		d = m.EditorState.Editor.FirstError()
		if d == nil {
			m.Feedback.NotifyInfo("No errors in file")
			return false
		}
	}
	m.EditorState.Editor.SetCursorPosition(d.Line, d.Col)
	m.EditorState.Editor.SetSelection(d.Line, d.Col, d.EndLine, d.EndCol)
	m.EditorState.Editor.ScrollToCursor()
	layout := m.computeLayout()
	m.EditorState.Editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
	m.Feedback.NotifyInfo(fmt.Sprintf("Error at line %d: %s", d.Line+1, d.Message))
	return true
}

// jumpToPrevError moves the cursor to the previous error diagnostic
// in the currently-open file (strictly before the current cursor).
// Mirrors jumpToNextError and wraps around to the last error.
func (m *App) jumpToPrevError() bool {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
		m.Feedback.NotifyInfo("No file open")
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
			m.Feedback.NotifyInfo("No errors in file")
			return false
		}
	}
	m.EditorState.Editor.SetCursorPosition(d.Line, d.Col)
	m.EditorState.Editor.SetSelection(d.Line, d.Col, d.EndLine, d.EndCol)
	m.EditorState.Editor.ScrollToCursor()
	layout := m.computeLayout()
	m.EditorState.Editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
	m.Feedback.NotifyInfo(fmt.Sprintf("Error at line %d: %s", d.Line+1, d.Message))
	return true
}

func (m *App) closeEditorTab() {
	if m.activeEditorPath() == "" {
		return
	}
	m.releaseTabBarFocus()
	defer m.syncRootFocus()
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
	if m.editorChanges != nil && m.editorChanges.source.FilePath() == path {
		m.overlayHost.Close(editorChangesOverlay)
	}
	if tab := m.EditorState.Tabs[path]; tab != nil && tab.Editor != nil {
		id := tab.Editor.BufferID()
		if m.Documents != nil {
			m.Documents.Cancel(id)
		}
		if state := m.editorSaves[id]; state != nil && state.inFlight {
			state.discard = true
			state.cancel()
			return
		}
		delete(m.editorSaves, id)
	}
	// Close any popups associated with this editor before closing the tab.
	m.closeFindReplace()
	m.closeReferencesPopup()
	m.closeCompletionPopup()
	m.closeOutlinePopup()
	m.clearDiagHoverPopup()
	m.overlayHost.Close(ui.OverlayDiagContext)

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
		if m.BufferMgr != nil {
			m.BufferMgr.Close(path)
		}
	}

	// Pick a new active editor tab if any remain. The window tab
	// switcher has already advanced ActiveIndex; the next call to
	// handleTabSwitch() will look up the new active path in the map.
	m.handleTabSwitch()

	if m.AgentView.VisibleCount() == 0 && m.EditorState.ActiveView == "" {
		m.openLauncher()
		m.LastInteractionTime = time.Now()
	}
}

func (m *App) activeEditorPath() string {
	return m.EditorState.ActiveView
}

// editorTabByBufferID resolves an open tab from its buffer lifetime ID,
// preferring preferredPath when several tabs share one buffer. Path is not an
// identity: a save acknowledgement can arrive after the tab was renamed or
// retargeted, and the committed result still belongs to that buffer.
func (m *App) editorTabByBufferID(bufferID, preferredPath string) (*EditorTab, string) {
	if bufferID == "" || m.EditorState == nil {
		return nil, ""
	}
	if tab := m.EditorState.Tabs[preferredPath]; tab != nil && tab.Editor != nil && tab.Editor.BufferID() == bufferID {
		return tab, preferredPath
	}
	for path, tab := range m.EditorState.Tabs {
		if tab != nil && tab.Editor != nil && tab.Editor.BufferID() == bufferID {
			return tab, path
		}
	}
	return nil, ""
}

// syncEditorTabPresentation derives every editor window tab's label and unsaved
// state from its live editor. The strip is rebuilt from the window each frame,
// so this is the single owner of both facts: the label is the plain base name
// and the unsaved marker is the tab's attention prefix (ui.Tabs paints it in
// front of the label). Nothing appends a marker to the filename, so a tab can
// never show two dots or glue one to the extension.
func (m *App) syncEditorTabPresentation() {
	if m.Win == nil || m.EditorState == nil {
		return
	}
	for i, count := 0, m.Win.TabCount(); i < count; i++ {
		tab, ok := m.Win.TabAt(i)
		if !ok || !m.isEditorTabTitle(tab.Title) {
			continue
		}
		ed := m.editorForWindowTab(tab.Title)
		if ed == nil {
			continue
		}
		if label := ed.BaseTitle(); label != "" && tab.DisplayTitle != label {
			m.Win.SetTabDisplayTitleByIndex(i, label)
		}
		state := window.TabReady
		if ed.Modified() {
			state = window.TabModified
		}
		if tab.State != state {
			m.Win.SetTabState(i, state)
		}
	}
}

// editorForWindowTab resolves the editor behind a strip tab key.
func (m *App) editorForWindowTab(title string) *editor.Editor {
	tab := m.EditorState.Tabs[m.editorPathFromTabTitle(title)]
	if tab == nil {
		return nil
	}
	return tab.Editor
}

// retitleEditorWindowTab re-keys one strip tab after a retarget. The tab key is
// the editor path, so leaving it stale would show the old filename, break
// close/hit lookups, and hide the tab's unsaved state.
func (m *App) retitleEditorWindowTab(oldPath, newPath string, ed *editor.Editor) {
	if m.Win == nil || ed == nil || oldPath == newPath {
		return
	}
	winTab := m.Win.TabByTitle(m.editorTabTitle(oldPath))
	if winTab == nil {
		return
	}
	idx := m.Win.IndexOf(winTab.ID)
	m.Win.SetTabTitleByIndex(idx, m.editorTabTitle(newPath))
	m.Win.SetTabDisplayTitleByIndex(idx, ed.BaseTitle())
}

// closeAllEditorTabsExcept closes every editor tab other than path. Dirty
// buffers are never discarded here: the walk stops at the first dirty tab and
// opens the unsaved-changes prompt.
func (m *App) closeAllEditorTabsExcept(path string) {
	m.releaseTabBarFocus()
	defer m.syncRootFocus()
	toClose := []string{}
	for p := range m.EditorState.Tabs {
		if p != path {
			toClose = append(toClose, p)
		}
	}
	sort.Strings(toClose) // map iteration order would make prompts nondeterministic
	m.closeEditorTabs(toClose)
}

// closeEditorTabs closes the given tabs in order. It stops at the first dirty
// buffer, prompts, and stores the remainder for continuePendingTabClose so a
// confirmed close resumes the bulk operation instead of losing unsaved work.
func (m *App) closeEditorTabs(paths []string) {
	m.pendingTabClose = nil
	for i, path := range paths {
		tab := m.EditorState.Tabs[path]
		if tab == nil || tab.Editor == nil {
			continue
		}
		if tab.Editor.Modified() {
			m.pendingTabClose = append(m.pendingTabClose, paths[i+1:]...)
			m.showDirtyClosePrompt(path)
			return
		}
		m.closeEditorTabForce(path)
	}
}

// continuePendingTabClose resumes a bulk close that stopped at a dirty buffer.
func (m *App) continuePendingTabClose() {
	if len(m.pendingTabClose) == 0 {
		return
	}
	paths := m.pendingTabClose
	m.pendingTabClose = nil
	m.closeEditorTabs(paths)
}

// abortPendingTabClose drops the remainder of a bulk close the user cancelled.
func (m *App) abortPendingTabClose() {
	m.pendingTabClose = nil
}

// closeEditorTabsToRightOf closes every tab whose index is greater
// than the given window-tab index. Dirty buffers are prompted, not destroyed.
func (m *App) closeEditorTabsToRightOf(idx int) {
	m.releaseTabBarFocus()
	defer m.syncRootFocus()
	tabs := m.Win.Tabs()
	toClose := make([]string, 0, len(tabs))
	for i := len(tabs) - 1; i > idx; i-- {
		t := tabs[i]
		if m.isEditorTabTitle(t.Title) {
			toClose = append(toClose, m.editorPathFromTabTitle(t.Title))
		}
	}
	m.closeEditorTabs(toClose)
}
