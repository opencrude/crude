package viewmodel

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/types"
)

// A native edit is not yet permission to query canonical source. Keep one small
// view-local intent until its acknowledgement, not a timer/request per key or a
// source-text copy. Native input replaces it with the newest intent; unrelated
// edits, focus or lifetime changes retire it. Replayed history input enters here
// at actual execution, never on enqueue.
type signatureHelpIntent struct {
	editor               *editor.Editor
	source               types.InsightSource
	revision, generation uint64
	line, col            int
	deadline             time.Time
	trigger              types.LSPSignatureContext
}

func (m *App) signatureEditorOwnsInput(ed *editor.Editor) bool {
	return m.EditorState != nil && m.EditorState.Editor == ed && ed != nil && ed.IsOpen() &&
		!ed.ReadOnly() && !editorHasUserSelection(ed) && m.isEditorActive() &&
		m.focusTarget() == FocusEditor && !m.termDockOwnsKeys() && !m.sessionDeckCapturesInput() &&
		m.focusedPopup() == nil && !m.asyncIOStopped.Load()
}

// editorHasUserSelection ignores the selected default of an active snippet
// field. The editor retires a snippet when a selection leaves its field.
func editorHasUserSelection(ed *editor.Editor) bool {
	return ed.HasSelection() && !ed.SnippetActive()
}

// queueSignatureHelpAfterInsertion asks for parameter information when an
// accepted suggestion leaves the caret, or its selected first field, directly
// inside a new argument list. Typing "(" does the same.
func (m *App) queueSignatureHelpAfterInsertion(ed *editor.Editor) {
	if ed == nil || !ed.IsOpen() {
		return
	}
	if m.automaticSignatureActive(ed) {
		m.queueAutomaticSignatureHelp(ed, "")
		return
	}
	line, col := ed.CursorLine(), ed.CursorCol()
	if ed.HasSelection() {
		line, col, _, _ = ed.SelectionRange()
	}
	raw := ed.LineText(line)
	off := editor.VisualColToByteOffset(raw, col, editor.DefaultTabWidth)
	if off <= 0 || off > len(raw) || m.ws.LSPMgr() == nil {
		return
	}
	r, _ := utf8.DecodeLastRuneInString(raw[:off])
	if !slices.Contains(m.ws.LSPMgr().AssistanceCapabilities(ed.FilePath()).SignatureTriggers, string(r)) {
		return
	}
	m.queueAutomaticSignatureHelp(ed, "")
}

// A native edit may replace an invocation, never its document lifetime. Retain
// only the wish for a fresh answer; old semantic text is closed immediately.
func (m *App) automaticSignatureActive(ed *editor.Editor) bool {
	if !m.signatureEditorOwnsInput(ed) {
		return false
	}
	source, _ := ed.CurrentInsightSource()
	sameSource := func(owner *editor.Editor, generation uint64, previous types.InsightSource) bool {
		return owner == ed && generation == m.workspaceSwitchGen.Load() &&
			previous.WorkspaceGeneration == source.WorkspaceGeneration &&
			previous.DocumentID == source.DocumentID && previous.Lifetime == source.Lifetime &&
			previous.Path == source.Path
	}
	if intent := m.signatureHelpIntent; intent != nil {
		return sameSource(intent.editor, intent.generation, intent.source)
	}
	state := m.signatureHelp
	return state != nil && state.automatic && sameSource(state.editor, state.generation, state.source)
}

// Arrow keys and snippet Tab change the argument position without changing
// source. Only real key input refreshes the request; peer/API publications and
// presentation ticks cannot manufacture fresh assistance.
func (m *App) refreshAutomaticSignaturePosition(msg tea.Msg) {
	if _, native := msg.(tea.KeyPressMsg); !native || m.EditorState == nil {
		return
	}
	ed := m.EditorState.Editor
	if !m.automaticSignatureActive(ed) {
		return
	}
	line, col := ed.LSPCursorPosition()
	if intent := m.signatureHelpIntent; intent != nil {
		if ed.Revision() == intent.revision && (line != intent.line || col != intent.col) {
			m.queueAutomaticSignatureHelp(ed, "")
		}
	} else if state := m.signatureHelp; ed.Revision() == state.revision && (line != state.line || col != state.col) {
		m.queueAutomaticSignatureHelp(ed, "")
	}
}

func (m *App) queueAutomaticSignatureHelp(ed *editor.Editor, character string) {
	if !m.signatureEditorOwnsInput(ed) {
		return
	}
	service := m.ws.LSPMgr()
	if service == nil {
		return
	}
	caps := service.AssistanceCapabilities(ed.FilePath())
	trigger := types.LSPSignatureContext{Producer: caps.Producer, TriggerKind: types.LSPSignatureInvoked}
	if m.automaticSignatureActive(ed) {
		trigger.TriggerKind = types.LSPSignatureContentChange
		if intent := m.signatureHelpIntent; intent != nil && intent.trigger.Producer == caps.Producer {
			trigger.IsRetrigger = intent.trigger.IsRetrigger
			trigger.ActiveSignatureHelp = bytes.Clone(intent.trigger.ActiveSignatureHelp)
		} else if state := m.signatureHelp; state != nil && state.trigger.Producer == caps.Producer {
			// An unanswered refresh still belongs to the last shown signature.
			// More typing replaces the request, not that original server context.
			trigger.IsRetrigger = state.trigger.IsRetrigger
			trigger.ActiveSignatureHelp = bytes.Clone(state.trigger.ActiveSignatureHelp)
			if state.shown && state.result != nil {
				trigger.IsRetrigger = true
				trigger.ActiveSignatureHelp = bytes.Clone(state.result.help.Original)
			}
		}
	}
	if character != "" && (slices.Contains(caps.SignatureTriggers, character) || trigger.IsRetrigger && slices.Contains(caps.SignatureRetriggers, character)) {
		trigger.TriggerKind, trigger.TriggerCharacter = types.LSPSignatureTriggerCharacter, character
	}
	m.closeSignatureHelp()
	if !caps.SignatureHelp {
		return
	}
	source, _ := ed.CurrentInsightSource()
	if source.WorkspaceGeneration == "" || source.DocumentID == "" || source.Lifetime == "" {
		return
	}
	line, col := ed.LSPCursorPosition()
	m.signatureHelpIntent = &signatureHelpIntent{editor: ed, source: source, revision: ed.Revision(),
		generation: m.workspaceSwitchGen.Load(), trigger: trigger, line: line, col: col, deadline: time.Now().Add(5 * time.Second)}
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
		source.Lifetime != intent.source.Lifetime || m.ws.LSPMgr() == nil {
		m.signatureHelpIntent = nil
		return nil
	}
	caps := m.ws.LSPMgr().AssistanceCapabilities(ed.FilePath())
	if !caps.SignatureHelp || caps.Producer != intent.trigger.Producer {
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
	return m.requestSignatureHelp(true, intent.trigger)
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
	return strings.TrimSpace(text.String())
}

// Both explicit documentation readers and passive hints anchor to the mounted
// editor, not the unsplit shell layout. The shared editor owns inlay/scroll and
// tab/Unicode caret projection; screenRectOf supplies nested/floating offsets.
func (m *App) editorHelpBounds(ed *editor.Editor, preferW, preferH int) (ui.Rect, bool) {
	if ed == nil {
		return ui.Rect{}, false
	}
	return m.editorPositionHelpBounds(ed, editor.Position{Line: ed.CursorLine(), Col: ed.CursorCol()}, preferW, preferH)
}

// Readers grow to fit their content before scrolling, within the existing
// editor-help limits. WindowFrame and TextArea own chrome and wrapping.
func (m *App) editorReaderBounds(ed *editor.Editor, position editor.Position, reader *ui.TextReader, preferredWidth int) (ui.Rect, bool) {
	return m.editorReaderBoundsAvoiding(ed, position, reader, preferredWidth, ui.Rect{})
}

func (m *App) editorReaderBoundsAvoiding(ed *editor.Editor, position editor.Position, reader *ui.TextReader, preferredWidth int, avoid ui.Rect) (ui.Rect, bool) {
	w, h := min(preferredWidth, max(1, m.width-4)), min(18, max(1, m.height-4))
	size := reader.Measure(ui.LayoutContext{}, ui.Constraints{MaxW: max(1, w-ui.TooltipChrome), MaxH: max(1, h-2)})
	return m.editorPositionHelpBoundsAvoiding(ed, position, w, size.H+2, avoid)
}

func (m *App) editorPositionHelpBounds(ed *editor.Editor, position editor.Position, preferW, preferH int) (ui.Rect, bool) {
	return m.editorPositionHelpBoundsAvoiding(ed, position, preferW, preferH, ui.Rect{})
}

func (m *App) editorPositionHelpBoundsAvoiding(ed *editor.Editor, position editor.Position, preferW, preferH int, avoid ui.Rect) (ui.Rect, bool) {
	if m.EditorState == nil || m.EditorState.Editor != ed || m.editorScreen == nil {
		return ui.Rect{}, false
	}
	rect, mounted := m.screenRectOf(m.editorScreen.ID())
	if !mounted || rect.Empty() {
		return ui.Rect{}, false
	}
	caretX, caretY, visible := ed.PositionScreenPosition(position, rect.X, rect.Y)
	if !visible || !rect.Contains(caretX, caretY) {
		return ui.Rect{}, false
	}
	frame := m.frameInsets()
	w := min(preferW, max(0, m.width-frame.Left-frame.Right))
	x, _ := m.clampShellPopupPosition(caretX, caretY, w, 1)
	y, h := 0, 0
	consider := func(top, bottom int, above bool) {
		height := min(preferH, max(0, bottom-top))
		if height <= h {
			return
		}
		y, h = top, height
		if above {
			y = bottom - height
		}
	}
	// Prefer the usual adjacent space below, then above. If the companion aid
	// occupies a side, use the nearest free interval without covering either
	// the caret or that aid. All editor readers share this geometry owner.
	for _, gap := range [2]struct {
		top, bottom int
		above       bool
	}{{caretY + 1, m.height - frame.Bottom, false}, {frame.Top, caretY, true}} {
		if avoid.Empty() || avoid.X >= x+w || avoid.X+avoid.W <= x ||
			avoid.Y >= gap.bottom || avoid.Y+avoid.H <= gap.top {
			consider(gap.top, gap.bottom, gap.above)
		} else if gap.above {
			consider(max(gap.top, avoid.Y+avoid.H), gap.bottom, true)
			consider(gap.top, min(gap.bottom, avoid.Y), true)
		} else {
			consider(gap.top, min(gap.bottom, avoid.Y), false)
			consider(max(gap.top, avoid.Y+avoid.H), gap.bottom, false)
		}
	}
	if w < 1 || h < 1 {
		return ui.Rect{}, false
	}
	return ui.Rect{X: x, Y: y, W: w, H: h}, true
}

func (m *App) signatureHelpAvoidance(ed *editor.Editor) ui.Rect {
	if m.completion != nil && m.completion.editor == ed && m.overlayHost != nil {
		if window := m.overlayHost.Window(ui.OverlayCompletion); window != nil {
			x, y := window.Position()
			w, h := window.Size()
			return ui.Rect{X: x, Y: y, W: w, H: h}
		}
	}
	return ui.Rect{}
}

func (m *App) positionAutomaticSignatureHelp(state *signatureHelpState) {
	fw := m.overlayHost.Window(signatureHelpID)
	if fw == nil {
		m.closeSignatureHelp()
		return
	}
	reader := fw.Content.(*ui.WindowFrameWidget).Body().(*ui.TextReader)
	bounds, visible := m.editorReaderBoundsAvoiding(state.editor, editor.Position{Line: state.editor.CursorLine(), Col: state.editor.CursorCol()},
		reader, state.preferredWidth, m.signatureHelpAvoidance(state.editor))
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
