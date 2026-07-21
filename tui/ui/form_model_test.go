package ui

import "testing"

func TestFormModelTracksDirtyValidationAndReset(t *testing.T) {
	field := &FieldState{
		ID:       "name",
		Label:    "Name",
		Type:     FieldText,
		Value:    StringValue("initial"),
		Initial:  StringValue("initial"),
		Required: true,
	}
	model := NewFormModel()
	model.AddField(field)

	model.SetValue("name", StringValue("changed"))
	if !field.Dirty || model.AnyErrors() {
		t.Fatalf("changed field dirty=%v errors=%v", field.Dirty, field.Errors)
	}
	model.Reset()
	if field.Dirty || field.Value.String() != "initial" || model.AnyErrors() {
		t.Fatalf("reset field=%#v", field)
	}

	model.SetValue("name", StringValue(""))
	if !model.AnyErrors() {
		t.Fatal("required field did not validate after a change")
	}
	model.Reset()
	if model.AnyErrors() {
		t.Fatal("reset did not revalidate restored required value")
	}
}
