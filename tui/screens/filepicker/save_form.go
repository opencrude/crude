package filepicker

import (
	"crude/text/textencoding"
	"crude/tui/rendering"
	"crude/tui/ui"
)

// saveNameWidth keeps the file name a readable line instead of a pane-wide bar.
const saveNameWidth = 40

// saveFormRows is the name row, one breathing row and the encoding row.
const saveFormRows = 3

func (s *Screen) initSaveForm() {
	s.saveStatus = ui.NewLabel("filepicker/save-status", "", s.th)
	s.saveModel = ui.NewFormModel()
	s.saveModel.AddField(&ui.FieldState{
		ID: "name", Label: "File name", Type: ui.FieldText,
		Placeholder: "notes.txt", SubmitAction: "filepicker.save.submit",
		Value: ui.StringValue(""), Width: saveNameWidth,
	})
	var options []ui.FormOption
	widest := 0
	for _, encoding := range textencoding.Choices() {
		options = append(options, ui.FormOption{ID: encoding.Key(), Label: encoding.Label()})
		widest = max(widest, rendering.VisibleWidth(encoding.Label()))
	}
	// A dropdown sized to its longest choice: one pad cell, the value, a gap
	// and the ▾ indicator with its pad.
	s.saveModel.AddField(&ui.FieldState{
		ID: "encoding", Label: "Encoding", Type: ui.FieldSelect,
		Value: ui.StringValue(textencoding.UTF8.Key()), Options: options, Width: widest + 5,
	})
	s.saveForm = ui.NewForm(SaveFormID, s.saveModel, "", "", s.tokens)
	s.saveForm.SetFieldGap(1)
	s.saveForm.SetHideDescriptions(true)
	s.saveForm.SetArrowExitsForm(true)
}

func (s *Screen) SaveName() string { return s.saveModel.Field("name").Value.String() }
func (s *Screen) SaveEncoding() textencoding.Encoding {
	return textencoding.FromKey(s.saveModel.Field("encoding").Value.String())
}

func (s *Screen) SetSaveOptions(name string, encoding textencoding.Encoding) {
	s.saveModel.SetValue("name", ui.StringValue(name))
	s.saveModel.SetValue("encoding", ui.StringValue(encoding.Key()))
	s.saveForm.SetForm(s.saveModel, "", "")
}

func (s *Screen) lockSaveForm(locked bool) {
	s.saveForm.CloseOpenSelects()
	for _, field := range s.saveModel.Fields() {
		field.Disabled = locked
	}
	s.saveForm.SetForm(s.saveModel, "", "")
}
