package ui

import (
	"fmt"
	"strings"

	"crude/tui/rendering"
	"crude/types"
)

// Shared FieldState constructors. Every form-bearing screen (settings, session
// settings, question, confirmation, ...) builds its fields through these so
// label/description/dirty/placeholder semantics stay identical across the app.
//
// Naming: the constructors return *FieldState (the model), not the retained
// FormField component, hence the "FieldState" suffix to avoid colliding with
// the TextField component in textfield.go.

// TextFieldState builds a single-line text field.
func TextFieldState(id, label, value, description string) *FieldState {
	v := StringValue(value)
	return &FieldState{ID: id, Label: label, Description: description, Type: FieldText, Value: v, Initial: v}
}

// PasswordFieldState builds a masked single-line text field.
func PasswordFieldState(id, label, value, description string) *FieldState {
	f := TextFieldState(id, label, value, description)
	f.Type = FieldPassword
	return f
}

// TextAreaFieldState builds a multi-line text field rendered as a full-width
// editor below the label. Enter inserts a newline; Tab leaves the field.
func TextAreaFieldState(id, label, value, description string) *FieldState {
	f := TextFieldState(id, label, value, description)
	f.Type = FieldTextArea
	f.Rows = 4
	return f
}

// KeyValuesFieldState builds an editable Name/value table below the label.
// The value uses the shared "Name: value" line protocol, so a host that
// already round-trips that text (settings headers) keeps its codec.
func KeyValuesFieldState(id, label, value, description string) *FieldState {
	f := TextFieldState(id, label, value, description)
	f.Type = FieldKeyValues
	return f
}

// CycleFieldState builds a left/right cycle field over a fixed value list.
func CycleFieldState(id, label string, values []string, current, description string) *FieldState {
	options := make([]FormOption, len(values))
	for i, value := range values {
		options[i] = FormOption{ID: value, Label: value}
	}
	v := StringValue(current)
	return &FieldState{ID: id, Label: label, Description: description, Type: FieldCycle, Value: v, Initial: v, Options: options}
}

// BoolFieldState builds an on/off cycle field. Prefer this over a raw
// CycleFieldState so every boolean toggle in the app reads identically.
func BoolFieldState(id, label string, value bool, description string) *FieldState {
	current := "off"
	if value {
		current = "on"
	}
	f := CycleFieldState(id, label, []string{"on", "off"}, current, description)
	f.Options = []FormOption{{ID: "on", Label: "On"}, {ID: "off", Label: "Off"}}
	f.Style = StyleSwitch
	return f
}

// SegmentedFieldState builds a cycle field painted as inline segments. Use it
// for small, short option sets where seeing every choice beats cycling.
func SegmentedFieldState(id, label string, options []FormOption, current, description string) *FieldState {
	v := StringValue(current)
	return &FieldState{ID: id, Label: label, Description: description, Type: FieldCycle, Value: v, Initial: v, Options: options, Style: StyleSegmented}
}

// NumberFieldState builds a narrow single-line numeric field with a unit.
func NumberFieldState(id, label string, value int, unit, description string) *FieldState {
	f := TextFieldState(id, label, fmt.Sprintf("%d", value), description)
	f.Unit, f.Numeric = unit, true
	return f
}

// BoolValue reads back a BoolFieldState field.
func BoolFieldValue(f *FieldState) bool { return f != nil && f.Value.String() == "on" }

func CheckboxFieldState(id, label string, value bool, description string) *FieldState {
	v := BoolValue(value)
	return &FieldState{ID: id, Label: label, Description: description, Type: FieldCheckbox, Value: v, Initial: v}
}

// ToggleFieldState builds an explicit Yes/No cycle field for settings where
// the bare on/off wording would be ambiguous.
func ToggleFieldState(id, label string, value bool, description string) *FieldState {
	current := "no"
	if value {
		current = "yes"
	}
	f := CycleFieldState(id, label, []string{"yes", "no"}, current, description)
	f.Options = []FormOption{{ID: "yes", Label: "Yes"}, {ID: "no", Label: "No"}}
	f.Style = StyleSwitch
	return f
}

// ToggleFieldValue reads back a ToggleFieldState field. A missing field reads
// as No so absent rows never silently enable behavior.
func ToggleFieldValue(f *FieldState) bool { return f != nil && f.Value.String() == "yes" }

// TriStateFieldState builds a three-option cycle for optional booleans: the
// concrete default (defaultLabel, e.g. "On (default)"), On, and Off. A nil
// value means the default applies; the label always states what the default
// actually is.
func TriStateFieldState(id, label, defaultLabel string, value *bool, description string) *FieldState {
	options := []FormOption{
		{ID: "default", Label: defaultLabel},
		{ID: "on", Label: "On"},
		{ID: "off", Label: "Off"},
	}
	current := "default"
	if value != nil {
		current = "off"
		if *value {
			current = "on"
		}
	}
	v := StringValue(current)
	return &FieldState{ID: id, Label: label, Description: description, Type: FieldCycle, Value: v, Initial: v, Options: options}
}

// TriStateFieldValue reads back a TriStateFieldState; nil means default.
func TriStateFieldValue(f *FieldState) *bool {
	if f == nil {
		return nil
	}
	switch f.Value.String() {
	case "on":
		on := true
		return &on
	case "off":
		off := false
		return &off
	}
	return nil
}

// TriBool reads back a tri-state value ("default"/"on"/"off") as *bool.
func TriBool(value string) *bool {
	switch value {
	case "on":
		on := true
		return &on
	case "off":
		off := false
		return &off
	}
	return nil
}

// SelectFieldState builds a dropdown select field.
func SelectFieldState(id, label, value string, options []FormOption, description string) *FieldState {
	v := StringValue(value)
	return &FieldState{ID: id, Label: label, Description: description, Type: FieldSelect, Value: v, Initial: v, Options: options}
}

// SeparatorFieldState builds a labeled section divider row.
func SeparatorFieldState(id, label string) *FieldState {
	return &FieldState{ID: id, Label: label, Type: FieldSeparator}
}

// SpacerFieldState builds a blank, unfocusable row.
func SpacerFieldState(id string) *FieldState {
	return &FieldState{ID: id, Type: FieldSpacer}
}

// ModelFieldState builds a model select field with the standard "fetch on
// open" placeholder used when the provider's model list is not loaded yet.
func ModelFieldState(id, label, value string, options []FormOption, description string) *FieldState {
	f := SelectFieldState(id, label, value, options, description)
	f.Placeholder = "Press Enter to fetch models"
	return f
}

// ThinkingFieldState builds a thinking-level cycle field over types.ThinkingLevels
// with the standard "Provider default" placeholder.
func ThinkingFieldState(id, current, description string) *FieldState {
	f := CycleFieldState(id, "Thinking level", types.ThinkingLevels, current, description)
	f.Placeholder = "Provider default"
	return f
}

// ProviderOptions converts provider presets into select options.
func ProviderOptions(presets []types.ProviderPreset) []FormOption {
	options := make([]FormOption, 0, len(presets))
	for _, preset := range presets {
		options = append(options, FormOption{ID: preset.ID, Label: preset.Name, Detail: preset.HelpText})
	}
	return options
}

// ModelOptions converts fetched model entries into select options, showing the
// context window as the option detail when known.
func ModelOptions(models []types.ModelEntry) []FormOption {
	options := make([]FormOption, 0, len(models))
	for _, model := range models {
		label := model.Name
		if label == "" {
			label = model.ID
		}
		var details []string
		if model.ContextWindow > 0 {
			details = append(details, rendering.FormatTokens(model.ContextWindow)+" ctx")
		}
		if model.InputPrice > 0 || model.OutputPrice > 0 {
			details = append(details, fmt.Sprintf(
				"$%.2f/%.2f per M", model.InputPrice*1_000_000, model.OutputPrice*1_000_000,
			))
		}
		var capabilities []string
		capabilities = append(capabilities, model.Modalities...)
		if model.SupportsTools {
			capabilities = append(capabilities, "tools")
		}
		if len(capabilities) > 0 {
			details = append(details, strings.Join(capabilities, " · "))
		}
		options = append(options, FormOption{
			ID: model.ID, Label: label, Detail: strings.Join(details, "  |  "),
		})
	}
	return options
}
