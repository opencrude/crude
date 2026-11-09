package viewmodel

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/imagepreview"
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

// openEditorTab shows path in the focused pane: that pane's view of the file
// is selected, or a view is added there. A file open elsewhere is not pulled
// out of its pane; the new view shares its document.
func (m *App) openEditorTab(path string) (tea.Cmd, error) {
	if imagepreview.SupportedPath(path) {
		m.editorCommands = append(m.editorCommands, m.openPaneFiles([]string{path}, m.Win.ActiveGroup()))
		return nil, nil
	}
	if _, connected := m.ws.(shellport.LiveDocuments); connected {
		if tab := m.EditorState.Tabs[path]; tab != nil && tab.Editor.IsOpen() {
			m.activateEditorTab(path)
			return nil, nil
		}
		// Some established entry points cannot return a command. Update drains
		// this queue, so none can silently lose an asynchronous open.
		m.editorCommands = append(m.editorCommands, m.openPaneFiles([]string{path}, m.Win.ActiveGroup()))
		return nil, nil
	}
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

// installEditorBuffer installs a prepared buffer as an open document without
// showing it; activateEditorTab or showDocument adds its views. Existing
// views retain their cursor, undo, selection and unsaved text.
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
	ed.SetOnTyping(func(key tea.KeyPressMsg) { m.onEditorTyping(ed, key) })

	// Apply cached diagnostics immediately so the editor is never empty
	// while waiting for the server to respond.
	if m.ws.LSPMgr() != nil {
		if cached, ok := m.ws.LSPMgr().FileDiagnostics(path); ok {
			ed.SetDiagnosticsVisible(m.ws.LSPMgr().ShowDiagnosticsInEditor())
			ed.SetDiagnostics(cached)
		}
	}

	m.EditorState.Tabs[path] = NewEditorTab(path, ed)
}

// activateEditorTab focuses path's view in the focused pane, adding one there
// when the pane does not show the file yet.
func (m *App) activateEditorTab(path string) {
	m.showDocument(path, m.Win.ActiveGroup(), true)
}

// jumpToDiagnostic opens the file containing the given diagnostic
// (if not already open), positions the cursor on it, highlights the
// range, scrolls it into view, and refreshes the hover tooltip. The
// caller supplies raw UTF-16 coordinates from the LSP cache, never the visual
// values returned by Editor.NextError/Diagnostics.
func (m *App) jumpToDiagnostic(filePath string, d editor.LSPDiagnostic) {
	if filePath == "" {
		return
	}
	facts, ok := m.diagnosticForNavigation(filePath, d)
	if !ok || !m.canNavigateDiagnostic(filePath, facts) {
		m.Feedback.NotifyInfo("This result cannot be matched to the current file. Open Problems to check the results.")
		return
	}
	if m.queueLiveDiagnosticNavigation(filePath, d, facts) {
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
	if err := m.EditorState.Editor.SetLSPDiagnosticPosition(d); err != nil {
		m.Feedback.NotifyError(err.Error())
		return
	}
	m.EditorState.Editor.ScrollToCursor()
	layout := m.computeLayout()
	m.EditorState.Editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
}

// gotoFirstDiagnostic opens the first error diagnostic in the
// project (sorted by file path, then by line/col) and jumps to it.
// If a file is already open and has errors, that file's first error
// wins. If the project has no errors, shows a status bar message.
func (m *App) gotoFirstDiagnostic() {
	if m.ws.LSPMgr() == nil {
		m.Feedback.NotifyInfo("Language servers are not available. Open Language Tools in Settings to set them up.")
		return
	}
	all := m.ws.LSPMgr().AllDiagnostics()
	if len(all) == 0 {
		m.Feedback.NotifyInfo("No errors are available to navigate. Open Problems to check the results.")
		return
	}

	// Prefer an error in the currently-active file (if any). The
	// user's current focus is more important than file-path order.
	var paths []string
	if active := m.activeEditorPath(); active != "" {
		if diags, ok := all[active]; ok {
			if e := firstErrorOf(diags.Diagnostics); e != nil && m.canNavigateDiagnostic(active, diags) {
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
		e := firstErrorOf(all[p].Diagnostics)
		if e == nil || !m.canNavigateDiagnostic(p, all[p]) {
			continue
		}
		m.jumpToDiagnostic(p, *e)
		m.Feedback.NotifyInfo(fmt.Sprintf("Jumped to %s:%d",
			filepath.Base(p), e.Line+1))
		return
	}

	m.Feedback.NotifyInfo("No errors are available to navigate. Open Problems to check the results.")
}

// firstErrorOf returns the first error-severity diagnostic in the
// given list (sorted by line then col, as LSP publishes them). The
// returned pointer aliases the input slice; it is safe to dereference
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
			m.Feedback.NotifyInfo(m.EditorState.Editor.DiagnosticStatus())
			return false
		}
	}
	m.EditorState.Editor.SetCursorPosition(d.Line, d.Col)
	m.EditorState.Editor.SetSelection(d.Line, d.Col, d.EndLine, d.EndCol)
	m.EditorState.Editor.ScrollToCursor()
	layout := m.computeLayout()
	m.EditorState.Editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
	m.Feedback.NotifyInfo(fmt.Sprintf("Error on line %d", d.Line+1))
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
			m.Feedback.NotifyInfo(m.EditorState.Editor.DiagnosticStatus())
			return false
		}
	}
	m.EditorState.Editor.SetCursorPosition(d.Line, d.Col)
	m.EditorState.Editor.SetSelection(d.Line, d.Col, d.EndLine, d.EndCol)
	m.EditorState.Editor.ScrollToCursor()
	layout := m.computeLayout()
	m.EditorState.Editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
	m.Feedback.NotifyInfo(fmt.Sprintf("Error on line %d", d.Line+1))
	return true
}

func (m *App) closeEditorTab() {
	if m.activeEditorPath() == "" {
		return
	}
	m.releaseTabBarFocus()
	defer m.syncRootFocus()
	if v := m.editorView(m.EditorState.ActiveTab); v != nil && len(m.documentViews(v.Path)) > 1 {
		m.closeLinkedView(v)
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

// closeEditorTabForce unconditionally closes the document at path and every
// view of it. Used both for the normal close path and after the user confirms
// a close-with-unsaved-changes prompt.
func (m *App) closeEditorTabForce(path string) {
	if tab := m.EditorState.Tabs[path]; tab != nil {
		if state := m.liveEditors[tab.Editor.Primary()]; state != nil {
			ds := tab.Editor.DocumentState()
			if state.busy || state.uncertain != nil || state.err != nil || ds.PendingEdits > 0 || ds.InFlight || ds.UndoPending || ds.HistoryQueued || state.undo != nil || ds.Conflict != nil {
				state.closeView = true
				return
			}
		}
	}
	if m.editorChanges != nil && m.editorChanges.source.FilePath() == path {
		m.overlayHost.Close(editorChangesOverlay)
	}
	if tab := m.EditorState.Tabs[path]; tab != nil && tab.Editor != nil {
		id := tab.Editor.BufferID()
		if m.ws.Documents() != nil {
			m.ws.Documents().Cancel(id)
		}
		if state := m.editorSaves[id]; state != nil && state.inFlight {
			state.discard = true
			state.cancel()
			return
		}
		delete(m.editorSaves, id)
	}
	// Close any popups associated with this editor before closing the tab.
	m.closeEditorViewPopups()

	// Linked views release their share first; the primary closes the document.
	for _, v := range m.documentViews(path) {
		m.Win.CloseTabByID(v.Tab)
		delete(m.EditorState.Views, v.Tab)
		delete(m.inlayViews, v.Editor)
		if tab := m.EditorState.Tabs[path]; tab == nil || v.Editor != tab.Editor {
			v.Editor.Close()
		}
	}

	tab, ok := m.EditorState.Tabs[path]
	if ok {
		if live, shared := m.ws.(shellport.LiveDocuments); shared {
			id := tab.Editor.DocumentState().DocumentID
			state := m.liveEditors[tab.Editor.Primary()]
			delete(m.liveEditors, tab.Editor)
			if state != nil && state.refs != nil {
				m.editorCommands = append(m.editorCommands, state.refs.releaseCmd(id))
			} else {
				m.editorCommands = append(m.editorCommands, func() tea.Msg { _ = live.CloseDocument(context.Background(), id); return nil })
			}
		}
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
		ed := m.editorForTab(tab.ID)
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

// retitleEditorWindowTab re-keys every view of a retargeted document. The tab
// key is the editor path, so leaving it stale would show the old filename,
// break close/hit lookups, and hide the tab's unsaved state.
func (m *App) retitleEditorWindowTab(oldPath, newPath string, ed *editor.Editor) {
	if m.Win == nil || ed == nil || oldPath == newPath {
		return
	}
	for _, v := range m.documentViews(oldPath) {
		v.Path = newPath
		idx := m.Win.IndexOf(v.Tab)
		m.Win.SetTabTitleByIndex(idx, m.editorTabTitle(newPath))
		if tab := m.Win.TabByID(v.Tab); tab != nil {
			tab.Resource.ID = newPath
		}
		m.Win.SetTabDisplayTitleByIndex(idx, ed.BaseTitle())
	}
	if m.EditorState.ActiveView == oldPath {
		m.EditorState.ActiveView = newPath
	}
}

// closeEditorViews closes the given editor tabs in order. A view of a
// document still shown by another tab closes at once; the last view of a
// dirty document stops the walk, prompts, and stores the remainder for
// continuePendingTabClose so a confirmed close resumes the bulk operation
// instead of losing unsaved work.
func (m *App) closeEditorViews(ids []window.TabID) {
	m.pendingTabClose = nil
	for i, id := range ids {
		if isImageTab(m.Win.TabByID(id)) {
			m.closeImageTab(id)
			continue
		}
		v := m.editorView(id)
		if v == nil {
			continue
		}
		if len(m.documentViews(v.Path)) > 1 {
			m.closeLinkedView(v)
			continue
		}
		if v.Editor.Modified() {
			m.pendingTabClose = append(m.pendingTabClose, ids[i+1:]...)
			m.showDirtyClosePrompt(v.Path)
			return
		}
		m.closeEditorTabForce(v.Path)
	}
}

// continuePendingTabClose resumes a bulk close that stopped at a dirty buffer.
func (m *App) continuePendingTabClose() {
	if len(m.pendingTabClose) == 0 {
		return
	}
	ids := m.pendingTabClose
	m.pendingTabClose = nil
	m.closeEditorViews(ids)
}

// abortPendingTabClose drops the remainder of a bulk close the user cancelled.
func (m *App) abortPendingTabClose() {
	m.pendingTabClose = nil
}
