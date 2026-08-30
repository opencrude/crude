package ui

import (
	"crude/tui/cell"
	"crude/tui/theme"
)

// TextPromptFieldID is the focused TextField id inside the frame.
const TextPromptFieldID = "tree-prompt/field"

// BuildTextPromptFrame builds a WindowFrame-backed dialog for tree create/rename.
// This is the first product surface on the unified WindowFrame chrome path.
func BuildTextPromptFrame(th *theme.Theme, st *TextPromptState) *ComponentWidget {
	if st == nil {
		st = NewTextPromptState(TextPromptGeneric, "Prompt", "", "")
	}
	st.SyncFromEdit()
	tokens := NewTokenSet(th)

	w := TextPromptPopupWidth(st)
	h := TextPromptPopupHeight(st)

	// Body: prompt label, input field, spacer, hint — colors via semantic
	// tokens (same pipeline as every other dialog).
	promptLabel := NewLabel(NewID(RootID, "tree-prompt/prompt"), st.Prompt, th)
	promptLabel.SetStyle(tokens.Color(ColorMuted), tokens.Color(ColorPopupBg), 0)

	placeholder := "name"
	switch st.Kind {
	case TextPromptNewFile:
		placeholder = "filename"
	case TextPromptNewFolder:
		placeholder = "folder"
	case TextPromptRename:
		placeholder = "new name"
	}

	field := NewTextField(ComponentID(TextPromptFieldID), tokens)
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
	hint := NewLabel(NewID(RootID, "tree-prompt/hint"), hintText, th)
	hint.SetStyle(tokens.Color(ColorMuted), tokens.Color(ColorPopupBg), cell.AttrItalic)

	// Blank row between the field and hint becomes Inset bottom padding on the
	// field, preserving the exact 4-row body layout.
	fieldPad := NewInset(NewID(RootID, "tree-prompt/field-pad"),
		Insets{Top: 0, Right: 0, Bottom: 1, Left: 0}, field)

	body := NewStack(NewID(RootID, "tree-prompt/body"), Vertical, 0,
		promptLabel, fieldPad, hint,
	)

	frame := NewWindowFrame(NewID(RootID, "tree-prompt"), FrameDialog, tokens)
	frame.SetTitle(st.Title)
	frame.SetIcon(kindIcon(st.Kind))
	frame.SetClosable(false)
	frame.SetContent(body)

	cw := NewComponentWidget(th, frame, w, h)
	cw.Focus(ComponentID(TextPromptFieldID))
	return cw
}

// SyncTextPromptField copies TextEdit state from the frame field back into st.
func SyncTextPromptField(cw *ComponentWidget, st *TextPromptState) {
	if cw == nil || st == nil {
		return
	}
	h := cw.Host()
	if h == nil {
		return
	}
	// Walk registry via Root
	root := cw.Root()
	field := findTextField(root, ComponentID(TextPromptFieldID))
	if field == nil {
		return
	}
	if st.Edit == nil {
		st.Edit = NewTextEdit()
	}
	st.Edit.SetValue(field.Value())
	st.Edit.SetCursor(field.Edit().Cursor(), false)
	st.SyncFromEdit()
}

// ApplyTextPromptEdit pushes st.Edit into the framed TextField for repaint.
func ApplyTextPromptEdit(cw *ComponentWidget, st *TextPromptState) {
	if cw == nil || st == nil || st.Edit == nil {
		return
	}
	field := findTextField(cw.Root(), ComponentID(TextPromptFieldID))
	if field == nil {
		return
	}
	field.SetValue(st.Edit.Value())
	field.Edit().SetCursor(st.Edit.Cursor(), false)
	if h := cw.Host(); h != nil {
		h.MarkPaint()
	}
}

func findTextField(c Component, id ComponentID) *TextField {
	if c == nil {
		return nil
	}
	if tf, ok := c.(*TextField); ok && c.ID() == id {
		return tf
	}
	if p, ok := c.(Parent); ok {
		for _, ch := range p.Children() {
			if found := findTextField(ch, id); found != nil {
				return found
			}
		}
	}
	return nil
}
