package viewmodel

import (
	"context"
	"crude/tui/editor"
)

// resetLSPRequestContext invalidates every request issued by the previous
// workspace generation. Result messages still carry a generation as a second
// ownership check because an LSP implementation may return after cancellation.
func (m *App) resetLSPRequestContext() {
	if m == nil {
		return
	}
	if m.lspRequestCancel != nil {
		m.lspRequestCancel()
	}
	m.lspRequestCtx, m.lspRequestCancel = context.WithCancel(context.Background())
}

func (m *App) lspRequestContext() (context.Context, uint64) {
	if m.lspRequestCtx == nil {
		m.resetLSPRequestContext()
	}
	return m.lspRequestCtx, m.workspaceSwitchGen.Load()
}

func (m *App) acceptsLSPGeneration(generation uint64) bool {
	return m != nil && generation == m.workspaceSwitchGen.Load()
}

func (m *App) acceptsLSPDocument(generation uint64, path string, owner *editor.Editor, revision uint64) bool {
	return m.acceptsLSPGeneration(generation) && owner != nil && m.EditorState.Editor == owner && owner.IsOpen() && owner.FilePath() == path && owner.Revision() == revision
}
