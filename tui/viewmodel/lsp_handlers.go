package viewmodel

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/editor"
	"crude/types"
	"crude/workspace"
)

// startRename arms rename mode in the LSP popup state.
func (m *App) startRename() {
	_, _ = m.startRenameCmd(), 0
}

func renameReviewRequest(msg lspRenameResultMsg) reviewedEditDialogRequest {
	return reviewedEditDialogRequest{
		owner: msg.owner, revision: msg.revision, generation: msg.generation,
		filePath: msg.filePath, preview: msg.preview, port: msg.port,
		title: "Review rename", intro: fmt.Sprintf("Rename to %q in %d files.", msg.newName, len(msg.preview.Files)),
		applyLabel: "Apply rename", committedLabel: "Renamed",
	}
}

func (m *App) showRenameReview(msg lspRenameResultMsg) {
	m.showReviewedEditDialog(renameReviewRequest(msg))
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
	m.closeSignatureHelp()
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
