package viewmodel

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types"
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
		Text: fmt.Sprintf("%s:%d:%d · %s", m.EditorState.Editor.BaseTitle(), line+1, col, m.EditorState.Editor.DiagnosticStatus()),
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

	lines = append(lines, ui.DiagLine{Text: m.EditorState.Editor.DiagnosticProvenance(), Kind: ui.DiagLineBody})
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
	if !m.acceptsLSPGeneration(msg.Generation) {
		return
	}
	// Event delivery and the independent snapshot drain can overtake each other.
	// Re-read this path from the fact authority, never revive an older queued
	// publication merely because its path still names an open editor.
	if service := m.ws.LSPMgr(); service != nil {
		msg.Facts, _ = service.FileDiagnostics(msg.FilePath)
	}
	m.applyLSPDiagnostics(msg.FilePath, msg.Facts)
}

// applyLSPDiagnostics consumes one authority-validated changed projection.
func (m *App) applyLSPDiagnostics(path string, facts types.InsightDiagnostics) {
	if m.lspFacts == nil {
		m.lspFacts = make(map[string]types.InsightDiagnostics)
	}
	if facts.ID == "" {
		delete(m.lspFacts, path)
	} else {
		m.lspFacts[path] = types.CloneInsightDiagnostics(facts)
	}
	m.projectTreeDiagnostic(path, facts)
	install := func(ed *editor.Editor) {
		ed.SetDiagnosticsVisible(m.ws.LSPMgr() == nil || m.ws.LSPMgr().ShowDiagnosticsInEditor())
		ed.SetDiagnostics(facts)
	}
	if tab := m.EditorState.Tabs[path]; tab != nil && tab.Editor != nil {
		install(tab.Editor)
	} else if ed := m.EditorState.Editor; ed != nil && path == ed.FilePath() && ed.IsOpen() {
		install(ed)
	}

	if m.EditorState.Editor != nil && path == m.EditorState.Editor.FilePath() {
		m.clearDiagHoverPopup()
	}
}

// Each shell consumes its own versioned replica; no callback or shared drain.
func (m *App) wireLSPDiagnostics() {
	m.lspDiagnostics = shellport.LSPDiagnosticConsumer{}
	m.lspFacts = nil
	m.lspTreeProjection = nil
}

func (m *App) drainLSPDiagnostics() {
	if m.ws.LSPMgr() == nil {
		return
	}
	// The cheap clock is checked before asking for a detached complete snapshot.
	generation, version := m.ws.LSPMgr().DiagnosticCursor()
	if m.lspDiagnostics.NeedsSnapshot(generation, version) {
		snapshot := m.ws.LSPMgr().DiagnosticSnapshot()
		for path, facts := range m.lspDiagnostics.Consume(snapshot) {
			m.applyLSPDiagnostics(path, facts)
		}
	}
	showTree := m.ws.LSPMgr().ShowDiagnosticsInTree()
	if showTree != m.lspTreeVisible {
		m.lspTreeVisible = showTree
		for path, facts := range m.lspFacts {
			m.projectTreeDiagnostic(path, facts)
		}
	}
	// Independent source publications can catch up without another LSP batch.
	for _, tab := range m.EditorState.Tabs {
		if tab != nil && tab.Editor != nil {
			tab.Editor.SetDiagnosticsVisible(m.ws.LSPMgr().ShowDiagnosticsInEditor())
			tab.Editor.RefreshDiagnostics()
			m.projectTreeDiagnostic(tab.Editor.FilePath(), m.lspFacts[tab.Editor.FilePath()])
		}
	}
}

func (m *App) syncFileTreeDiagnosticsFromLSP() {
	if m == nil || m.ws.LSPMgr() == nil || m.Tree == nil || m.Tree.Model == nil {
		return
	}
	m.lspTreeProjection = nil
	for fp, facts := range m.ws.LSPMgr().AllDiagnostics() {
		m.applyLSPDiagnostics(fp, facts)
	}
}

// handleFileWatcherEvent processes a file system change that arrived through
// the workspace.EventBus from the file watcher goroutine. Forwards the change to the
// LSP server for re-analysis.
func (m *App) handleFileWatcherEvent(msg workspace.FileWatcherMsg) tea.Cmd {
	if m.ws.LSPMgr() == nil {
		return nil
	}
	lspMgr := m.ws.LSPMgr()
	if tab := m.EditorState.Tabs[msg.FilePath]; tab != nil && tab.Editor != nil {
		// Inactive unsaved buffers remain editor-owned, not watcher-owned overlays.
		tab.Editor.SignalDiskChanged()
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		lspMgr.NotifyWatchedFilesChanged(ctx, msg.FilePath, msg.ChangeType)
		return nil
	}
}

// startLSPForWorkspace explicitly resumes intelligence for open editor buffers.
func (m *App) startLSPForWorkspace() {
	wsDir := "."
	if m.ws.RootPath() != "" {
		wsDir = m.ws.RootPath()
	}
	if m.ws.Logger() != nil {
		m.ws.Logger().Debug("LSP", "starting_for_workspace", "dir", wsDir)
	}
	if m.ws.LSPMgr() != nil {
		m.ws.LSPMgr().StartForWorkspace(wsDir)
		m.syncFileTreeDiagnosticsFromLSP()
	}
}

// StopLSP shuts down all LSP server connections.
func (m *App) StopLSP() {
	if m.lspRequestCancel != nil {
		m.lspRequestCancel()
		m.lspRequestCancel = nil
		m.lspRequestCtx = nil
	}
	if m.ws.LSPMgr() != nil {
		m.ws.LSPMgr().Stop()
		m.drainLSPDiagnostics()
	}
}
