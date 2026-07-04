package viewmodel

import (
	"context"
	"os"
	"time"

	"crude/tui/editor"
	"crude/tui/widgets"
	"crude/tui/wm"
	"crude/tui/workspace"
)

// showDiagnosticAt displays a floating popup with diagnostics at the
// given line/col in the active editor.
func (m *App) showDiagnosticAt(line, col int) {
	diags := m.EditorState.Editor.DiagnosticsAt(line, col)
	if len(diags) == 0 {
		return
	}
	// Build inner content: one row per diagnostic message.
	var content []string
	for _, d := range diags {
		content = append(content, d.Message)
	}
	// Wrap in unified chrome.
	lines := widgets.PopupChromeLines(m.Theme, widgets.PopupChromeConfig{
		Title:     "Diagnostic",
		TitleIcon: "ℹ",
		Width:     60,
		Focused:   true,
	}, content)
	pop := widgets.NewPopupContent(lines)
	fw := wm.NewFloatingWindow("diag-context", "", pop, 60, len(lines),
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(false),
		wm.WithFocusable(true),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("diag-context")
		m.windowManager.AddWindow(fw)
	}
}

// startRename arms rename mode in the LSP popup state.
func (m *App) startRename() {
	_, _ = m.startRenameCmd(), 0
}

// handleLSPDiagnosticsUpdate processes a diagnostics update that arrived
// through the workspace.EventBus from the LSP server's reader goroutine. Runs on the
// main Bubble Tea goroutine, so it can safely update the tree model and editor.
func (m *App) handleLSPDiagnosticsUpdate(msg workspace.LSPDiagnosticsMsg) {
	if m.Tree.Model != nil {
		errs, warns, hints := 0, 0, 0
		for _, d := range msg.Diagnostics {
			switch d.Severity {
			case editor.DiagError:
				errs++
			case editor.DiagWarning:
				warns++
			default:
				hints++
			}
		}
		m.Tree.Model.SetDiagnostics(msg.FilePath, errs, warns, hints)
	}
	if msg.FilePath == m.EditorState.Editor.FilePath() && m.EditorState.Editor.IsOpen() {
		m.EditorState.Editor.SetDiagnostics(msg.Diagnostics)
	}
}

// handleFileWatcherEvent processes a file system change that arrived through
// the workspace.EventBus from the file watcher goroutine. Forwards the change to the
// LSP server for re-analysis.
func (m *App) handleFileWatcherEvent(msg workspace.FileWatcherMsg) {
	if m.LSPMgr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.LSPMgr.NotifyWatchedFilesChanged(ctx, msg.FilePath, msg.ChangeType)
	m.LSPMgr.SyncFileFromDisk(ctx, msg.FilePath, m.EditorState.Editor.FilePath(), msg.ChangeType)
}

// startLSPForWorkspace scans the working directory and starts LSP
// servers for detected project roots.
func (m *App) startLSPForWorkspace() {
	wsDir, err := os.Getwd()
	if err != nil {
		wsDir = "."
	}
	if m.Logger != nil {
		m.Logger.Debug("LSP", "starting_for_workspace", "dir", wsDir)
	}
	if m.LSPMgr != nil {
		// Clear cached diagnostics on every (re)start so a stopped
		// server doesn't leave stale errors visible until the new
		// session republishes them.
		m.LSPMgr.ClearDiagnostics()
		// Also clear per-editor diagnostic caches for open tabs.
		// The new server will repopulate them via the onDiagnostics
		// callback as it walks the project.
		m.clearOpenEditorsDiagnostics()
		m.LSPMgr.StartForWorkspace(wsDir)
	}
}

// clearOpenEditorsDiagnostics wipes the diagnostic cache for every
// open editor tab. Called on LSP restart to avoid showing stale
// errors from the previous server session.
func (m *App) clearOpenEditorsDiagnostics() {
	if m.EditorState.Editor != nil {
		m.EditorState.Editor.SetDiagnostics(nil)
	}
	for _, tab := range m.EditorState.Tabs {
		if tab != nil && tab.Editor != nil {
			tab.Editor.SetDiagnostics(nil)
		}
	}
}

// StopLSP shuts down all LSP server connections.
func (m *App) StopLSP() {
	if m.LSPMgr != nil {
		m.LSPMgr.Stop()
	}
}
