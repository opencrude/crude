package ui

import tea "charm.land/bubbletea/v2"

// NewDockedReadingForm presents immutable details in the same docked form as
// other session decisions, but gives the text its own wrapping and paging
// viewport instead of clipping it into a table cell.
func NewDockedReadingForm(id ComponentID, title, detail string, tokens *TokenSet) *Form {
	const fieldID = "details"
	field := TextAreaFieldState(fieldID, "", detail, "")
	field.ReadOnly = true
	model := NewFormModel()
	model.AddField(field)
	f := NewDockedForm(id, model, title, "", tokens)
	f.reading = f.Field(fieldID)
	f.reading.fitReader = true
	return f
}

// ReadPage lets a docked decision page its details while an answer has focus.
// It uses the same read-only control and scroll position as direct reading.
func (f *Form) ReadPage(key tea.KeyPressMsg) EventResult {
	if f.reading == nil || f.reading.Bounds().Empty() || (key.Code != tea.KeyPgUp && key.Code != tea.KeyPgDown) {
		return EventResult{}
	}
	return f.reading.area.Handle(EventContext{Focused: true}, KeyEvent(key))
}
