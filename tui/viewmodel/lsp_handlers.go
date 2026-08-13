package viewmodel

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/workspace"
)

// showDiagnosticAt displays a floating popup with diagnostics at the
// given line/col in the active editor.
func (m *App) showDiagnosticAt(line, col int) {
	diags := m.EditorState.Editor.DiagnosticsAt(line, col)
	if len(diags) == 0 {
		return
	}
	// Build structured content lines.
	var lines []ui.DiagLine
	lines = append(lines, ui.DiagLine{
		Text: fmt.Sprintf("%s:%d:%d", m.EditorState.Editor.Title(), line+1, col),
		Kind: ui.DiagLineTitle,
	})
	sourceLine := strings.TrimRight(m.EditorState.Editor.LineText(line), "\n\r ")
	if sourceLine != "" {
		lines = append(lines, ui.DiagLine{
			Text: sourceLine,
			Kind: ui.DiagLineSource,
		})
	}
	lines = append(lines, ui.DiagLine{
		Text: ui.DiagLineDividerText(54),
		Kind: ui.DiagLineDivider,
	})
	for _, d := range diags {
		lines = append(lines, ui.DiagLine{
			Text: d.Message,
			Kind: ui.DiagLineBody,
		})
	}

	// Determine severity from the first diagnostic.
	sev := ui.DiagSevInfo
	if len(diags) > 0 {
		switch diags[0].Severity {
		case 1:
			sev = ui.DiagSevError
		case 2:
			sev = ui.DiagSevWarning
		}
	}

	frame := ui.BuildDiagnosticFrame(m.Theme, ui.DiagFrameSpec{
		Title:    "Diagnostic",
		Icon:     "ℹ",
		Severity: sev,
		Lines:    lines,
		Width:    60,
		Height:   len(lines) + 2,
	})
	fw := wm.NewFloatingWindow(string(ui.OverlayDiagContext), "", frame, 60, len(lines)+2,
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(false),
		wm.WithFocusable(true),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(ui.OverlayDiagContext, fw, ui.PopoverPolicy(true), nil)
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
		if m.LSPMgr == nil || m.LSPMgr.ShowDiagnosticsInTree() {
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
		} else {
			m.Tree.Model.SetDiagnostics(msg.FilePath, 0, 0, 0)
		}
	}
	if msg.FilePath == m.EditorState.Editor.FilePath() && m.EditorState.Editor.IsOpen() {
		if m.LSPMgr != nil && !m.LSPMgr.ShowDiagnosticsInEditor() {
			m.EditorState.Editor.SetDiagnostics(nil)
		} else {
			m.EditorState.Editor.SetDiagnostics(msg.Diagnostics)
		}
	}
}

func (m *App) wireLSPDiagnostics() {
	if m == nil || m.LSPMgr == nil {
		return
	}
	m.LSPMgr.SetOnDiagnostics(func(fp string, diags []editor.LSPDiagnostic) {
		if m.eventBus != nil {
			m.eventBus.Send(workspace.LSPDiagnosticsMsg{FilePath: fp, Diagnostics: diags})
		}
	})
}

func (m *App) syncFileTreeDiagnosticsFromLSP() {
	if m == nil || m.LSPMgr == nil || m.Tree == nil || m.Tree.Model == nil {
		return
	}
	for fp, diags := range m.LSPMgr.AllDiagnostics() {
		m.handleLSPDiagnosticsUpdate(workspace.LSPDiagnosticsMsg{FilePath: fp, Diagnostics: diags})
	}
}

func (m *App) clearTreeDiagnostics() {
	if m == nil || m.Tree == nil || m.Tree.Model == nil {
		return
	}
	m.Tree.Model.ClearDiagnostics()
}

// handleFileWatcherEvent processes a file system change that arrived through
// the workspace.EventBus from the file watcher goroutine. Forwards the change to the
// LSP server for re-analysis.
func (m *App) handleFileWatcherEvent(msg workspace.FileWatcherMsg) tea.Cmd {
	if m.LSPMgr == nil {
		return nil
	}
	lspMgr := m.LSPMgr
	activeFilePath := ""
	if m.EditorState.Editor != nil {
		activeFilePath = m.EditorState.Editor.FilePath()
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		lspMgr.NotifyWatchedFilesChanged(ctx, msg.FilePath, msg.ChangeType)
		lspMgr.SyncFileFromDisk(ctx, msg.FilePath, activeFilePath, msg.ChangeType)
		return nil
	}
}

// startLSPForWorkspace scans the active workspace root and starts LSP
// servers for detected project roots.
func (m *App) startLSPForWorkspace() {
	wsDir := "."
	if m.RootPath != "" {
		wsDir = m.RootPath
	}
	if m.Logger != nil {
		m.Logger.Debug("LSP", "starting_for_workspace", "dir", wsDir)
	}
	if m.LSPMgr != nil {
		// Clear cached diagnostics on every (re)start so a stopped
		// server doesn't leave stale errors visible until the new
		// session republishes them.
		m.LSPMgr.ClearDiagnostics()
		m.clearTreeDiagnostics()
		// Also clear per-editor diagnostic caches for open tabs.
		// The new server will repopulate them via the onDiagnostics
		// callback as it walks the project.
		m.clearOpenEditorsDiagnostics()
		m.LSPMgr.StartForWorkspace(wsDir)
		m.syncFileTreeDiagnosticsFromLSP()
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
