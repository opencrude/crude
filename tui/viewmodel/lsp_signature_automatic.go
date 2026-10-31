package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/types"
)

// A native edit is not yet permission to query canonical source. Keep one small
// view-local intent until its acknowledgement, not a timer/request per key or a
// source-text copy. Any intervening edit, caret/focus or lifetime change retires
// it. Replayed history input enters here at actual execution, never on enqueue.
type signatureHelpIntent struct {
	editor               *editor.Editor
	source               types.InsightSource
	revision, generation uint64
	line, col            int
	deadline             time.Time
}

func (m *App) signatureEditorOwnsInput(ed *editor.Editor) bool {
	return m.EditorState != nil && m.EditorState.Editor == ed && ed != nil && ed.IsOpen() &&
		!ed.ReadOnly() && !ed.HasSelection() && m.isEditorActive() &&
		m.focusTarget() == FocusEditor && !m.termDockOwnsKeys() && !m.sessionDeckCapturesInput() &&
		!m.sessionDeckModal() && m.focusedPopup() == nil && !m.asyncIOStopped.Load()
}

func (m *App) queueAutomaticSignatureHelp(ed *editor.Editor) {
	if !m.signatureEditorOwnsInput(ed) {
		return
	}
	m.closeSignatureHelp()
	service := m.ws.LSPMgr()
	if service == nil || !service.ServerReady() {
		return // typing never creates a missing-provider notification
	}
	source, _ := ed.CurrentInsightSource()
	if source.WorkspaceGeneration == "" || source.DocumentID == "" || source.Lifetime == "" {
		return
	}
	line, col := ed.LSPCursorPosition()
	m.signatureHelpIntent = &signatureHelpIntent{editor: ed, source: source, revision: ed.Revision(),
		generation: m.workspaceSwitchGen.Load(), line: line, col: col, deadline: time.Now().Add(5 * time.Second)}
}

// Called only by Update, after document results/queued native input reconcile.
// A normal publication or idle tick drains it; no recursive Update or UI I/O.
func (m *App) drainAutomaticSignatureHelp() tea.Cmd {
	intent := m.signatureHelpIntent
	if intent == nil {
		return nil
	}
	ed := intent.editor
	line, col := ed.LSPCursorPosition()
	source, current := ed.CurrentInsightSource()
	if !m.signatureEditorOwnsInput(ed) || !m.acceptsLSPDocument(intent.generation, intent.source.Path, ed, intent.revision) ||
		line != intent.line || col != intent.col || time.Now().After(intent.deadline) ||
		source.WorkspaceGeneration != intent.source.WorkspaceGeneration || source.DocumentID != intent.source.DocumentID ||
		source.Lifetime != intent.source.Lifetime || m.ws.LSPMgr() == nil || !m.ws.LSPMgr().ServerReady() {
		m.signatureHelpIntent = nil
		return nil
	}
	if !current {
		return nil
	}
	contextOwner, contextGeneration := m.ws.LSPMgr().ContextCursor()
	if contextOwner != source.WorkspaceGeneration || contextGeneration == 0 {
		// The server-ready and source topics can precede initial context
		// publication. This is still only input intent, not an invocation or
		// semantic answer. Bind the then-current context at actual admission.
		return nil
	}
	m.signatureHelpIntent = nil
	return m.requestSignatureHelp(true)
}

func signatureHintText(help types.LSPSignatureHelp) string {
	if len(help.Signatures) == 0 || help.ActiveSignature < 0 || help.ActiveSignature >= len(help.Signatures) {
		return ""
	}
	active := help.Signatures[help.ActiveSignature]
	var text strings.Builder
	fmt.Fprintln(&text, active.Label)
	if active.ActiveParameter >= 0 && active.ActiveParameter < len(active.Parameters) {
		parameter := active.Parameters[active.ActiveParameter]
		fmt.Fprintf(&text, "\nActive parameter %d of %d: %s\n", active.ActiveParameter+1, len(active.Parameters), parameter.Label)
		if parameter.Documentation != "" {
			fmt.Fprintln(&text, parameter.Documentation)
		}
	}
	fmt.Fprintf(&text, "\nProvider: %s\nSource revision: %d; context: %d\nAlt+I details · Esc dismiss",
		help.Boundary.Producer.Provider, help.Boundary.Source.Revision, help.Boundary.Context.Generation)
	return text.String()
}

// Both explicit documentation readers and passive hints anchor to the mounted
// editor, not the unsplit shell layout. The shared editor owns inlay/scroll and
// tab/Unicode caret projection; screenRectOf supplies nested/floating offsets.
func (m *App) editorHelpBounds(ed *editor.Editor, preferW, preferH int) (ui.Rect, bool) {
	if m.EditorState == nil || m.EditorState.Editor != ed || m.editorScreen == nil {
		return ui.Rect{}, false
	}
	rect, mounted := m.screenRectOf(m.editorScreen.ID())
	if !mounted || rect.Empty() {
		return ui.Rect{}, false
	}
	caretX, caretY := ed.CursorScreenPosition(rect.X, rect.Y)
	if !rect.Contains(caretX, caretY) {
		return ui.Rect{}, false
	}
	frame := m.frameInsets()
	above, below := max(0, caretY-frame.Top), max(0, m.height-frame.Bottom-caretY-1)
	w := min(preferW, max(0, m.width-frame.Left-frame.Right))
	h := min(preferH, max(above, below))
	if w < 1 || h < 1 {
		return ui.Rect{}, false
	}
	// Shrink the reader (it remains scrollable) instead of covering its source
	// caret when neither side can fit the preferred height.
	x, y := caretX, caretY+1
	if h > below {
		y = caretY - h
	}
	x, y = m.clampShellPopupPosition(x, y, w, h)
	return ui.Rect{X: x, Y: y, W: w, H: h}, true
}

func (m *App) positionAutomaticSignatureHelp(state *signatureHelpState) {
	fw := m.overlayHost.Window(signatureHelpID)
	if fw == nil {
		m.closeSignatureHelp()
		return
	}
	w, h := min(90, max(1, m.width-4)), min(10, max(1, m.height-4))
	bounds, visible := m.editorHelpBounds(state.editor, w, h)
	if !visible {
		m.closeSignatureHelp()
		return
	}
	if oldW, oldH := fw.Size(); oldW != bounds.W || oldH != bounds.H {
		fw.SetSize(bounds.W, bounds.H)
	}
	if oldX, oldY := fw.Position(); oldX != bounds.X || oldY != bounds.Y {
		fw.SetPosition(bounds.X, bounds.Y)
	}
}
