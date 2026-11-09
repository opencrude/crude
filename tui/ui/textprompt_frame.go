package ui

import (
	"unicode"
	"unicode/utf8"

	"crude/tui/cell"
	"crude/tui/theme"
)

// TextPromptFieldID is the focused TextField id inside the frame.
const TextPromptFieldID = "text-prompt/field"

// BuildTextPromptFrame builds the shared WindowFrame-backed text dialog used by
// file create/rename, attachment rename and go to line. It is
// the reference surface on the unified WindowFrame chrome path.
func BuildTextPromptFrame(th *theme.Theme, st *TextPromptState) *ComponentWidget {
	if st == nil {
		st = NewTextPromptState(TextPromptGeneric, "Prompt", "", "")
	}
	tokens := NewTokenSet(th)
	w := TextPromptPopupWidth(st)
	h := TextPromptPopupHeight(st)
	body := TextPromptBody(th, st)
	frame := NewWindowFrame(NewID(RootID, "text-prompt"), FrameDialog, tokens)
	frame.SetTitle(st.Title)
	frame.SetIcon(kindIcon(st.Kind))
	frame.SetClosable(false)
	frame.SetContent(body)
	cw := NewComponentWidget(th, frame, w, h)
	cw.Focus(ComponentID(TextPromptFieldID))
	return cw
}

// TextPromptBody shares the bound field and validation copy without imposing
// modal chrome. Editor rename uses it inside an anchored transient popover.
func TextPromptBody(th *theme.Theme, st *TextPromptState) Component {
	st.SyncFromEdit()
	tokens := NewTokenSet(th)
	// Body: prompt label, input field, spacer, hint; colors via semantic
	// tokens (same pipeline as every other dialog). A rejected value states
	// why in the guidance row, where the accepted input normally stands, so
	// the dialog itself says what to do next.
	guidance, guidanceColor := st.Prompt, tokens.Color(ColorMuted)
	if st.Error != "" {
		guidance, guidanceColor = sentenceStart(st.Error), tokens.Color(ColorError)
	}
	promptLabel := NewLabel(NewID(RootID, "text-prompt/prompt"), guidance, th)
	promptLabel.SetStyle(guidanceColor, tokens.Color(ColorPopupBg), 0)

	placeholder := "name"
	switch st.Kind {
	case TextPromptNewFile:
		placeholder = "filename"
	case TextPromptNewFolder:
		placeholder = "folder"
	case TextPromptRename:
		placeholder = "new name"
	case TextPromptGoToLine:
		placeholder = "42"
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
	case TextPromptGoToLine:
		hintText = "enter jump · esc cancel"
	}
	hint := NewLabel(NewID(RootID, "text-prompt/hint"), hintText, th)
	hint.SetStyle(tokens.Color(ColorMuted), tokens.Color(ColorPopupBg), cell.AttrItalic)

	// Blank row between the field and hint becomes Inset bottom padding on the
	// field, preserving the exact 4-row body layout.
	fieldPad := NewInset(NewID(RootID, "text-prompt/field-pad"),
		Insets{Top: 0, Right: 0, Bottom: 1, Left: 0}, field)

	return NewStack(NewID(RootID, "text-prompt/body"), Vertical, 0,
		promptLabel, fieldPad, hint,
	)
}

// sentenceStart renders a rejection message as dialog copy: the guidance row
// reads as a sentence, while the message itself stays a conventional lowercase
// Go error.
func sentenceStart(message string) string {
	r, size := utf8.DecodeRuneInString(message)
	if size == 0 {
		return message
	}
	return string(unicode.ToUpper(r)) + message[size:]
}

// TextPromptField returns the bound name field of a framed text prompt.
func TextPromptField(cw interface{ Root() Component }) *TextField {
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
