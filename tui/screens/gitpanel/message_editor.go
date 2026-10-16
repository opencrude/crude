package gitpanel

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
)

const (
	MessageEditorID ui.ComponentID = "git-message-editor/body"

	ActionMessageSave   = "git-message-editor.save"
	ActionMessageCommit = "git-message-editor.commit"
	ActionMessageCancel = "git-message-editor.cancel"

	// commitSubjectLimit is Git's conventional subject width (prompt/commit.go
	// asks Aeron for the same).
	commitSubjectLimit = 72
)

// MessageEditor is the full commit message: subject, blank line and body,
// with the conventions shown as they are typed. It edits a copy; Save or
// Commit returns the text to the panel draft.
type MessageEditor struct {
	ui.Base
	tokens  *ui.TokenSet
	area    *ui.TextArea
	actions *ui.ActionBar
	canSave bool
	// decide turns Save, Commit or Cancel (with the edited text) into the
	// owner's command.
	decide func(action, text string) tea.Cmd
}

func NewMessageEditor(tokens *ui.TokenSet, text string, canCommit bool, decide func(action, text string) tea.Cmd) *MessageEditor {
	editor := &MessageEditor{Base: ui.NewBase(MessageEditorID), tokens: tokens, decide: decide}
	editor.area = ui.NewTextArea(MessageEditorID, tokens, true)
	editor.area.SetMultiLine(true)
	editor.area.SetPlaceholder("Subject in the imperative mood\n\nWhy the change was needed and what it does.")
	editor.area.SetBackgroundColor(tokens.Color(ui.ColorPopupSubtleBg))
	editor.area.SetValue(text)
	editor.area.SetCursorFlat(len([]rune(text)))
	editor.actions = ui.NewActionBar("git-message-editor/actions", tokens)
	editor.actions.SetCancelAction(ActionMessageCancel)
	editor.canSave = canCommit
	editor.syncButtons()
	return editor
}

func (e *MessageEditor) Value() string { return e.area.Value() }

func (e *MessageEditor) syncButtons() {
	empty := strings.TrimSpace(e.area.Value()) == ""
	e.actions.SetButtons(
		ui.ActionBarButton{ID: "git-message-editor/save", Label: "Save", Shortcut: "Ctrl+S", Action: ActionMessageSave, Enabled: true, Variant: ui.VariantPrimary},
		ui.ActionBarButton{ID: "git-message-editor/commit", Label: "Commit", Shortcut: "Ctrl+Enter", Action: ActionMessageCommit, Enabled: e.canSave && !empty, Variant: ui.VariantSecondary},
		ui.ActionBarButton{ID: "git-message-editor/cancel", Label: "Cancel", Action: ActionMessageCancel, Enabled: true, Variant: ui.VariantSecondary},
	)
}

// Children excludes the text area: the editor owns focus and delegates to it,
// so its own shortcuts are seen before the area could consume them.
func (e *MessageEditor) Children() []ui.Component { return []ui.Component{e.actions} }

func (e *MessageEditor) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (e *MessageEditor) Layout(ctx ui.LayoutContext, r ui.Rect) {
	e.SetBounds(r)
	areaH := max(0, r.H-3)
	e.area.Layout(ctx, ui.Rect{W: r.W, H: areaH})
	e.actions.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: min(1, r.H)})
}

// conventionLine summarises the message against Git's conventions.
func (e *MessageEditor) conventionLine() (string, ui.SemanticColor) {
	lines := strings.Split(e.area.Value(), "\n")
	subject := strings.TrimRight(lines[0], " ")
	width := rendering.VisibleWidth(subject)
	text := fmt.Sprintf("Subject %d/%d", width, commitSubjectLimit)
	role := ui.ColorMuted
	switch {
	case width > commitSubjectLimit:
		text += " · shorten the subject"
		role = ui.ColorWarning
	case len(lines) > 1 && strings.TrimSpace(lines[1]) != "":
		text += " · leave a blank line before the body"
		role = ui.ColorWarning
	case strings.HasSuffix(subject, "."):
		text += " · no period after the subject"
		role = ui.ColorWarning
	default:
		body := 0
		for _, line := range lines[min(2, len(lines)):] {
			if strings.TrimSpace(line) != "" {
				body++
			}
		}
		if body > 0 {
			text += fmt.Sprintf(" · body %d line", body)
			if body > 1 {
				text += "s"
			}
		}
	}
	return text, role
}

func (e *MessageEditor) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	r := e.Bounds()
	if r.H < 3 {
		return
	}
	e.area.Paint(buf, originX+r.X, originY+r.Y, ctx)
	text, role := e.conventionLine()
	y := originY + r.Y + r.H - 3
	bg := e.tokens.Color(ui.ColorPopupBg)
	buf.WriteString(originX+r.X, y, rendering.TruncateVisual(text, r.W, "…"), e.tokens.Color(role), bg, cell.Attrs(0))
}

func (e *MessageEditor) finish(action string) ui.EventResult {
	if action == ActionMessageCommit && (!e.canSave || strings.TrimSpace(e.area.Value()) == "") {
		return ui.EventResult{Handled: true}
	}
	var cmd tea.Cmd
	if e.decide != nil {
		cmd = e.decide(action, e.area.Value())
	}
	return ui.EventResult{Handled: true, Cmd: cmd}
}

func (e *MessageEditor) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		switch evt.Key.String() {
		case "ctrl+s":
			return e.finish(ActionMessageSave)
		case "ctrl+enter":
			return e.finish(ActionMessageCommit)
		case "esc":
			return e.finish(ActionMessageCancel)
		}
	}
	result := e.area.Handle(ctx, evt)
	e.syncButtons()
	result.Invalidate |= ui.InvalidatePaint
	return result
}

func (e *MessageEditor) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case ActionMessageSave, ActionMessageCommit, ActionMessageCancel:
		return e.finish(action.ID)
	}
	return ui.EventResult{Action: &action}
}

func (e *MessageEditor) TextEditor() ui.TextClipboardTarget { return e.area }
func (e *MessageEditor) CancelPointerCapture()              { e.area.CancelPointerCapture() }
func (e *MessageEditor) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: "Commit message", Description: "Ctrl+S saves, Ctrl+Enter commits, Esc cancels"}
}
