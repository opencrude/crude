package viewmodel

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/types"
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
	reader               *ui.TextReader
	preferredWidth       int
}

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
		m.Feedback.NotifyInfo("This problem changed. Open it again.")
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
	explicit := id == ui.OverlayDiagContext
	// DiagnosticsAt returns a detached projection; order overlapping messages by
	// severity without changing the provider's retained result.
	slices.SortStableFunc(diags, func(a, b editor.LSPDiagnostic) int { return cmp.Compare(a.Severity, b.Severity) })
	text, severity, colors := diagnosticReaderText(diags)
	tokens := ui.NewTokenSet(m.Theme)
	reader := ui.NewTextReader(ui.ComponentID(string(id)+"/reader"), tokens)
	area := reader.TextArea()
	area.SetValue(text)
	area.SetRuneColors(colors)
	area.SetCursorFlat(0)
	label, accent := diagnosticSeverityLabel(severity), ui.ColorMuted
	switch severity {
	case editor.DiagError:
		accent = ui.ColorError
	case editor.DiagWarning:
		accent = ui.ColorWarning
	}
	if len(diags) > 1 {
		label = "Diagnostics"
	}
	titleWidth := ui.TooltipFrameWidth(label, 90)
	// Leave room for the scrollbar so tall messages do not rewrap merely
	// because the bar appears.
	preferredWidth := min(90, max(titleWidth, ui.TooltipFrameWidth(text, 90)+ui.ScrollbarWidth))
	bounds, visible := m.editorReaderBounds(ed, editor.Position{Line: line, Col: col}, reader, preferredWidth)
	if !visible || bounds.W <= ui.TooltipChrome || bounds.H < 3 {
		return nil
	}
	var focus ui.ComponentID
	if explicit {
		focus = area.ID()
	}
	frame, fw := m.newEditorPopover(id, label, reader, focus, bounds)
	frame.Frame().SetBorderAccent(tokens.Color(accent))
	s := &diagnosticPopover{workspace: m.ws, editor: ed, generation: m.workspaceSwitchGen.Load(), revision: ed.Revision(), factID: factID, line: line, col: col, selection: currentCodeActionSelection(ed), frame: frame, reader: reader, preferredWidth: preferredWidth}
	policy := ui.PopoverPolicy(true)
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

// The fact store already bounds retained diagnostics. Show the complete message;
// provider controls are literal escapes, not terminal effects.
func diagnosticReaderText(diags []editor.LSPDiagnostic) (string, int, map[int]ui.SemanticColor) {
	severity := 0
	for _, d := range diags {
		severity = types.HighestDiagnosticSeverity(severity, d.Severity)
	}
	var b strings.Builder
	colors := make(map[int]ui.SemanticColor, len(diags))
	offset := 0
	for i, d := range diags {
		if i > 0 {
			b.WriteString("\n\n")
			offset += 2
		}
		colors[offset] = ui.DiagnosticColor(d.Severity)
		icon := ui.DiagnosticGlyph(d.Severity) + " "
		b.WriteString(icon)
		offset += len([]rune(icon))
		if len(diags) > 1 {
			label := diagnosticSeverityLabel(d.Severity) + ": "
			b.WriteString(label)
			offset += len([]rune(label))
		}
		message := strings.Trim(d.Message, "\r\n")
		if strings.TrimSpace(message) == "" {
			message = "No description provided."
		}
		// Keep useful rule attribution compact, as in IDE marker hovers. This
		// is reported source/code, not Crude's fact or provider identity.
		source, code := strings.TrimSpace(d.Source), strings.TrimSpace(d.Code)
		if source != "" || code != "" {
			message += "  " + source
			if code != "" {
				message += " (" + code + ")"
			}
		}
		for _, r := range message {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				q := strconv.QuoteRune(r)
				b.WriteString(q[1 : len(q)-1])
				offset += len(q) - 2 // quoted control escapes are ASCII
			} else {
				b.WriteRune(r)
				offset++
			}
		}
	}
	return b.String(), severity, colors
}

func diagnosticSeverityLabel(severity int) string {
	switch severity {
	case editor.DiagError:
		return "Error"
	case editor.DiagWarning:
		return "Warning"
	case editor.DiagInfo:
		return "Information"
	case editor.DiagHint:
		return "Hint"
	default:
		return "Diagnostic"
	}
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
		bounds, visible := m.editorReaderBounds(s.editor, editor.Position{Line: s.line, Col: s.col}, s.reader, s.preferredWidth)
		if !visible || bounds.W <= ui.TooltipChrome || bounds.H < 3 {
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
