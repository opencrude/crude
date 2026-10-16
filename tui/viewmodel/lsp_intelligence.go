package viewmodel

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/tui/wm"
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
	area := ui.NewTextArea("lsp-hover/text", ui.NewTokenSet(m.Theme), true)
	area.SetReadOnly(true)
	area.SetValue(msg.text)
	area.SetCursorFlat(0)
	w, h := min(90, max(1, m.width-4)), min(18, max(1, m.height-4))
	frame := ui.NewWindowFrameWidget(m.Theme, "lsp-hover/frame", ui.FramePopover, "Symbol documentation", area, nil, false, w, h)
	fw := wm.NewFloatingWindow("lsp-hover", "", frame, w, h, wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true), wm.WithClosable(true), wm.WithFocusable(true), wm.WithClickOutside(wm.ClickOutsideCloseRoute))
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(ui.OverlayID("lsp-hover"), fw, ui.PopoverPolicy(true), nil)
}

func (m *App) autoCompletionCmd(now time.Time) tea.Cmd {
	if m.LSPPopup == nil || m.LSPPopup.CompletionDue.IsZero() || now.Before(m.LSPPopup.CompletionDue) {
		return nil
	}
	m.LSPPopup.CompletionDue = time.Time{}
	ed := m.EditorState.Editor
	if m.ws.LSPMgr() == nil || !m.ws.LSPMgr().Settings().AutoComplete || m.EditorState.ActiveView == "" || ed == nil || !ed.IsOpen() || m.focusTarget() != FocusEditor || m.LSPPopup.CompletionID != "" {
		return nil
	}
	if m.focusedPopup() != nil {
		return nil
	}
	raw := ed.LineText(ed.CursorLine())
	off := editor.VisualColToByteOffset(raw, ed.CursorCol(), editor.DefaultTabWidth)
	if off <= 0 {
		return nil
	}
	r, _ := utf8.DecodeLastRuneInString(raw[:off])
	if r != '.' && r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
		return nil
	}
	return m.requestCompletion(true)
}
