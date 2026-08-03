package widgets

import (
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
)

// TextPromptFieldID is the focused TextField id inside the frame.
const TextPromptFieldID = "tree-prompt/field"

// BuildTextPromptFrame builds a WindowFrame-backed dialog for tree create/rename.
// This is the first product surface on the unified WindowFrame chrome path.
func BuildTextPromptFrame(th *theme.Theme, st *TextPromptState) *ui.ComponentWidget {
	if st == nil {
		st = NewTextPromptState(TextPromptGeneric, "Prompt", "", "")
	}
	st.SyncFromEdit()
	tokens := ui.NewTokenSet(th)

	w := TextPromptPopupWidth(st)
	h := TextPromptPopupHeight(st)

	// Body: prompt label, input field, spacer, hint
	promptLabel := ui.NewLabel(ui.NewID(ui.RootID, "tree-prompt/prompt"), st.Prompt, th)
	if th != nil {
		promptLabel.SetStyle(
			rendering.ThemeColorToCell(th.Colors.Muted),
			rendering.ThemeColorToCell(th.Colors.PopupBg),
			0,
		)
	}

	placeholder := "name"
	switch st.Kind {
	case TextPromptNewFile:
		placeholder = "filename"
	case TextPromptNewFolder:
		placeholder = "folder"
	case TextPromptRename:
		placeholder = "new name"
	}

	field := ui.NewTextField(ui.ComponentID(TextPromptFieldID), tokens)
	field.SetPrompt("")
	field.SetPlaceholder(placeholder)
	field.SetSubmitAction("") // Enter handled by viewmodel
	field.SetValue(st.ValueText())
	// Sync cursor from state engine
	if st.Edit != nil {
		field.Edit().SetCursor(st.Edit.Cursor(), false)
	}

	hintText := "enter submit · esc cancel"
	switch st.Kind {
	case TextPromptNewFile, TextPromptNewFolder:
		hintText = "enter create · esc cancel"
	case TextPromptRename:
		hintText = "enter rename · esc cancel"
	}
	hint := ui.NewLabel(ui.NewID(ui.RootID, "tree-prompt/hint"), hintText, th)
	if th != nil {
		hint.SetStyle(
			rendering.ThemeColorToCell(th.Colors.Muted),
			rendering.ThemeColorToCell(th.Colors.PopupBg),
			cell.AttrItalic,
		)
	}

	// Blank row between the field and hint becomes Inset bottom padding on the
	// field, preserving the exact 4-row body layout.
	fieldPad := ui.NewInset(ui.NewID(ui.RootID, "tree-prompt/field-pad"),
		ui.Insets{Top: 0, Right: 0, Bottom: 1, Left: 0}, field)

	body := ui.NewStack(ui.NewID(ui.RootID, "tree-prompt/body"), ui.Vertical, 0,
		promptLabel, fieldPad, hint,
	)

	frame := ui.NewWindowFrame(ui.NewID(ui.RootID, "tree-prompt"), ui.FrameDialog, tokens)
	frame.SetTitle(st.Title)
	frame.SetIcon(kindIcon(st.Kind))
	frame.SetClosable(false)
	frame.SetContent(body)

	cw := ui.NewComponentWidget(th, frame, w, h)
	cw.Focus(ui.ComponentID(TextPromptFieldID))
	return cw
}

// SyncTextPromptField copies TextEdit state from the frame field back into st.
func SyncTextPromptField(cw *ui.ComponentWidget, st *TextPromptState) {
	if cw == nil || st == nil {
		return
	}
	h := cw.Host()
	if h == nil {
		return
	}
	// Walk registry via Root
	root := cw.Root()
	field := findTextField(root, ui.ComponentID(TextPromptFieldID))
	if field == nil {
		return
	}
	if st.Edit == nil {
		st.Edit = ui.NewTextEdit()
	}
	st.Edit.SetValue(field.Value())
	st.Edit.SetCursor(field.Edit().Cursor(), false)
	st.SyncFromEdit()
}

// ApplyTextPromptEdit pushes st.Edit into the framed TextField for repaint.
func ApplyTextPromptEdit(cw *ui.ComponentWidget, st *TextPromptState) {
	if cw == nil || st == nil || st.Edit == nil {
		return
	}
	field := findTextField(cw.Root(), ui.ComponentID(TextPromptFieldID))
	if field == nil {
		return
	}
	field.SetValue(st.Edit.Value())
	field.Edit().SetCursor(st.Edit.Cursor(), false)
	if h := cw.Host(); h != nil {
		h.MarkPaint()
	}
}

func findTextField(c ui.Component, id ui.ComponentID) *ui.TextField {
	if c == nil {
		return nil
	}
	if tf, ok := c.(*ui.TextField); ok && c.ID() == id {
		return tf
	}
	if p, ok := c.(ui.Parent); ok {
		for _, ch := range p.Children() {
			if found := findTextField(ch, id); found != nil {
				return found
			}
		}
	}
	return nil
}
