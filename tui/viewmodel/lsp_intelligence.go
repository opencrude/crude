package viewmodel

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"crude/tui/editor"
	"crude/tui/ui"
)

type lspHoverResultMsg struct {
	owner                *editor.Editor
	revision, generation uint64
	path, text           string
	line, col            int
	err                  error
}

func (m *App) requestLSPHoverCmd() tea.Cmd {
	ed := m.EditorState.Editor
	if ed == nil || !ed.IsOpen() || m.ws.LSPMgr() == nil || !m.ws.LSPMgr().ServerReady() {
		return nil
	}
	ctx, generation := m.lspRequestContext()
	revision, path := ed.Revision(), ed.FilePath()
	line, col := ed.LSPCursorPosition()
	manager := m.ws.LSPMgr()
	return func() tea.Msg {
		text, err := manager.RequestHover(ctx, path, line, col)
		return lspHoverResultMsg{ed, revision, generation, path, text, line, col, err}
	}
}
func (m *App) handleLSPHoverResult(msg lspHoverResultMsg) {
	if !m.acceptsLSPDocument(msg.generation, msg.path, msg.owner, msg.revision) {
		return
	}
	line, col := msg.owner.LSPCursorPosition()
	if line != msg.line || col != msg.col {
		return
	}
	if msg.err != nil {
		m.Feedback.NotifyError("Hover: " + msg.err.Error())
		return
	}
	if strings.TrimSpace(msg.text) == "" {
		m.Feedback.NotifyInfo("No symbol documentation")
		return
	}
	reader := ui.NewTextReader("lsp-hover/reader", ui.NewTokenSet(m.Theme))
	area := reader.TextArea()
	area.SetValue(msg.text)
	area.SetCursorFlat(0)
	width := min(90, max(ui.TooltipFrameWidth("Symbol documentation", 90), ui.TooltipFrameWidth(msg.text, 90)+ui.ScrollbarWidth))
	anchor := m.editorPopoverAnchor(msg.owner, width, 18)
	if anchor == nil {
		return
	}
	bounds, visible := m.editorReaderBounds(msg.owner, editor.Position{Line: msg.owner.CursorLine(), Col: msg.owner.CursorCol()}, reader, width)
	if !visible {
		return
	}
	_, fw := m.newEditorPopover(ui.OverlaySymbolHover, "Symbol documentation", reader, area.ID(), bounds)
	m.overlayHost.Open(ui.OverlaySymbolHover, fw, ui.PopoverPolicy(true), func() {
		if m.LSPPopup.Hover == anchor {
			m.LSPPopup.Hover = nil
		}
	})
	m.LSPPopup.Hover = anchor
}
