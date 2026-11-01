package viewmodel

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/tui/wm"
)

// A diagnostic reader borrows one exact projected fact publication. It cannot
// survive a new source, fact, editor lifetime or private invocation owner.
type diagnosticPopover struct {
	workspace            shellport.Workspace
	editor               *editor.Editor
	generation, revision uint64
	factID               string
	line, col            int
	selection            codeActionSelection
	frame                *ui.WindowFrameWidget
}

const diagnosticReaderMaxBytes = 64 << 10

func (m *App) closeDiagnosticDetail() {
	if m.overlayHost != nil {
		m.overlayHost.Close(ui.OverlayDiagContext)
	}
	m.diagnosticDetail = nil
}

func (m *App) showDiagnosticAt(ed *editor.Editor, factID string, line, col int) {
	if ed == nil || m.EditorState.Editor != ed {
		return
	}
	id, current := ed.DiagnosticCursor()
	if !current || id != factID {
		m.Feedback.NotifyInfo("Diagnostic details changed; inspect the current source again")
		return
	}
	m.clearDiagHoverPopup()
	m.closeDiagnosticDetail()
	m.diagnosticDetail = m.openDiagnosticPopover(ui.OverlayDiagContext, ed, id, line, col)
}

func (m *App) updateDiagHoverPopup() {
	if m.EditorState == nil || m.EditorState.Editor == nil || m.overlayHost == nil {
		return
	}
	ed := m.EditorState.Editor
	state := ed.HoverState()
	id, current := ed.DiagnosticCursor()
	if !current || len(state.Messages) == 0 || m.overlayHost.IsOpen(ui.OverlayDiagContext) {
		m.clearDiagHoverPopup()
		return
	}
	if old := m.diagnosticHover; old != nil && old.editor == ed && old.factID == id && old.line == state.Line && old.col == state.Col {
		return
	}
	m.clearDiagHoverPopup()
	m.diagnosticHover = m.openDiagnosticPopover(ui.OverlayDiagHover, ed, id, state.Line, state.Col)
}

func (m *App) updateDiagHoverPopupFromCursor() { m.updateDiagHoverPopup() }

func (m *App) clearDiagHoverPopup() {
	if m.overlayHost != nil {
		m.overlayHost.Close(ui.OverlayDiagHover)
	}
	m.diagnosticHover = nil
}

func (m *App) openDiagnosticPopover(id ui.OverlayID, ed *editor.Editor, factID string, line, col int) *diagnosticPopover {
	diags := ed.DiagnosticsAt(line, col)
	if len(diags) == 0 {
		return nil
	}
	w, h := min(90, max(1, m.width-4)), min(18, max(1, m.height-4))
	explicit := id == ui.OverlayDiagContext
	if !explicit {
		w, h = min(w, 70), min(h, 10)
	}
	bounds, visible := m.editorPositionHelpBounds(ed, editor.Position{Line: line, Col: col}, w, h)
	if !visible || bounds.W < 12 || bounds.H < 4 {
		return nil
	}
	text, severity := diagnosticReaderText(ed, line, col, diags)
	tokens := ui.NewTokenSet(m.Theme)
	area := ui.NewTextArea(ui.ComponentID(string(id)+"/text"), tokens, true)
	area.SetReadOnly(true)
	area.SetValue(text)
	area.SetCursorFlat(0)
	label, accent := "Diagnostics", ui.ColorMuted
	switch severity {
	case editor.DiagError:
		label, accent = "Error", ui.ColorError
	case editor.DiagWarning:
		label, accent = "Warning", ui.ColorWarning
	case editor.DiagInfo:
		label = "Information"
	case editor.DiagHint:
		label = "Hint"
	}
	frame := ui.NewWindowFrameWidget(m.Theme, ui.ComponentID(string(id)+"/frame"), ui.FramePopover, label, area, nil, explicit, bounds.W, bounds.H)
	frame.Frame().SetBorderAccent(tokens.Color(accent))
	if explicit {
		frame.Focus(area.ID())
	}
	s := &diagnosticPopover{workspace: m.ws, editor: ed, generation: m.workspaceSwitchGen.Load(), revision: ed.Revision(), factID: factID, line: line, col: col, selection: currentCodeActionSelection(ed), frame: frame}
	fw := wm.NewFloatingWindow(string(id), "", frame, bounds.W, bounds.H, wm.WithDecoration(true), wm.WithFocusable(true), wm.WithMovable(false), wm.WithResizable(false), wm.WithClosable(explicit), wm.WithModal(false), wm.WithClickOutside(wm.ClickOutsideCloseRoute))
	fw.SetPosition(bounds.X, bounds.Y)
	policy := ui.PopoverPolicy(explicit)
	policy.Passive = !explicit
	m.overlayHost.Open(id, fw, policy, func() {
		if id == ui.OverlayDiagContext && m.diagnosticDetail == s {
			m.diagnosticDetail = nil
		}
		if id == ui.OverlayDiagHover && m.diagnosticHover == s {
			m.diagnosticHover = nil
		}
	})
	return s
}

// The display budget is separate from the fact authority's retained evidence.
// Overbudget content is refused visibly, never clipped into an apparent complete
// message. Provider controls are shown as literal escapes, not terminal effects.
func diagnosticReaderText(ed *editor.Editor, line, col int, diags []editor.LSPDiagnostic) (string, int) {
	severity := editor.DiagInfo
	var b strings.Builder
	appendText := func(s string) bool {
		if len(s) > diagnosticReaderMaxBytes-b.Len() {
			return false
		}
		b.WriteString(s)
		return true
	}
	refused := func() (string, int) {
		return "Diagnostic details exceed the 64 KiB reader budget. No partial diagnostic text is displayed.", severity
	}
	if !appendText(fmt.Sprintf("%s:%d:%d (visual columns)\n%s\n\n", ed.BaseTitle(), line+1, col+1, ed.DiagnosticStatus())) {
		return refused()
	}
	source := strings.TrimRight(ed.LineText(line), "\r\n")
	if len(source) > 4096 {
		source = "Source line preview omitted (over 4 KiB)."
	}
	if !appendText(source + "\n\n") {
		return refused()
	}
	for i, d := range diags {
		if d.Severity > 0 && (i == 0 || d.Severity < severity) {
			severity = d.Severity
		}
		label := "Diagnostic"
		switch d.Severity {
		case editor.DiagError:
			label = "Error"
		case editor.DiagWarning:
			label = "Warning"
		case editor.DiagInfo:
			label = "Information"
		case editor.DiagHint:
			label = "Hint"
		}
		if !appendText(fmt.Sprintf("%s %d of %d\n", label, i+1, len(diags))) {
			return refused()
		}
		if d.Source != "" && (!appendText("Source: ") || !appendText(d.Source) || !appendText("\n")) {
			return refused()
		}
		if d.Code != "" && (!appendText("Code: ") || !appendText(d.Code) || !appendText("\n")) {
			return refused()
		}
		if !appendText(d.Message) || !appendText("\n\n") {
			return refused()
		}
	}
	if !appendText(ed.DiagnosticProvenance()) {
		return refused()
	}
	raw := b.String()
	b.Reset()
	for _, r := range raw {
		v := string(r)
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			q := strconv.QuoteRune(r)
			v = q[1 : len(q)-1]
		}
		if !appendText(v) {
			return refused()
		}
	}
	return b.String(), severity
}

// Update alone retires stale evidence. It reads only the small publication
// cursor, never clones all diagnostics or reconstructs the reader each tick.
func (m *App) syncDiagnosticPopups() {
	for _, id := range []ui.OverlayID{ui.OverlayDiagContext, ui.OverlayDiagHover} {
		s := m.diagnosticDetail
		if id == ui.OverlayDiagHover {
			s = m.diagnosticHover
		}
		if s == nil {
			continue
		}
		fw := m.overlayHost.Window(id)
		factID, current := s.editor.DiagnosticCursor()
		valid := fw != nil && fw.Content == s.frame && s.workspace == m.ws && s.generation == m.workspaceSwitchGen.Load() &&
			!m.asyncIOStopped.Load() && m.EditorState.Editor == s.editor && s.editor.IsOpen() && s.editor.Revision() == s.revision &&
			current && factID == s.factID
		if id == ui.OverlayDiagContext {
			valid = valid && m.focusedPopup() == fw && currentCodeActionSelection(s.editor) == s.selection
		}
		if id == ui.OverlayDiagHover {
			valid = valid && (m.focusedPopup() == nil || m.focusedPopup() == fw) &&
				currentCodeActionSelection(s.editor) == s.selection
		}
		if !valid {
			if id == ui.OverlayDiagContext {
				m.closeDiagnosticDetail()
			} else {
				m.clearDiagHoverPopup()
			}
		}
	}
}

// Paint publishes only geometry after the mounted editor has its new viewport.
// The source point uses the editor's one tab/grapheme/inlay/scroll projection.
func (m *App) positionDiagnosticPopups() {
	for _, id := range []ui.OverlayID{ui.OverlayDiagContext, ui.OverlayDiagHover} {
		s := m.diagnosticDetail
		if id == ui.OverlayDiagHover {
			s = m.diagnosticHover
		}
		if s == nil {
			continue
		}
		fw := m.overlayHost.Window(id)
		if fw == nil {
			continue
		}
		w, h := min(90, max(1, m.width-4)), min(18, max(1, m.height-4))
		if id == ui.OverlayDiagHover {
			w, h = min(w, 70), min(h, 10)
		}
		bounds, visible := m.editorPositionHelpBounds(s.editor, editor.Position{Line: s.line, Col: s.col}, w, h)
		if !visible || bounds.W < 12 || bounds.H < 4 {
			if id == ui.OverlayDiagContext {
				m.closeDiagnosticDetail()
			} else {
				m.clearDiagHoverPopup()
			}
			continue
		}
		if oldW, oldH := fw.Size(); oldW != bounds.W || oldH != bounds.H {
			fw.SetSize(bounds.W, bounds.H)
		}
		if x, y := fw.Position(); x != bounds.X || y != bounds.Y {
			fw.SetPosition(bounds.X, bounds.Y)
		}
	}
}
