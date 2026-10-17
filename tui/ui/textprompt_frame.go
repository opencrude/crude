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

	// Body: prompt label, input field, spacer, hint; colors via semantic
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
	if st.Edit == nil {
		st.Edit = NewTextEdit()
		st.Edit.SetValue(st.Value)
	}
	// The field edits the prompt's TextEdit itself (value, caret, selection).
	field.BindEdit(st.Edit)

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

// TextPromptField returns the bound name field of a framed tree prompt.
func TextPromptField(cw *ComponentWidget) *TextField {
	if cw == nil {
		return nil
	}
	return findTextField(cw.Root(), ComponentID(TextPromptFieldID))
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
