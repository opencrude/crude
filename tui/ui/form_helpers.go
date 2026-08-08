package ui

import (
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
	return CycleFieldState(id, label, []string{"on", "off"}, current, description)
}

// BoolValue reads back a BoolFieldState field.
func BoolFieldValue(f *FieldState) bool { return f != nil && f.Value.String() == "on" }

// SelectFieldState builds a dropdown select field.
func SelectFieldState(id, label, value string, options []FormOption, description string) *FieldState {
	v := StringValue(value)
	return &FieldState{ID: id, Label: label, Description: description, Type: FieldSelect, Value: v, Initial: v, Options: options}
}

// SeparatorFieldState builds a labeled section divider row.
func SeparatorFieldState(id, label string) *FieldState {
	return &FieldState{ID: id, Label: label, Type: FieldSeparator}
}

// ThinkingLevels is the canonical thinking-level option list, shared by the
// workspace settings and session override forms.
var ThinkingLevels = []string{"none", "low", "medium", "high", "xhigh"}

// ModelFieldState builds a model select field with the standard "fetch on
// open" placeholder used when the provider's model list is not loaded yet.
func ModelFieldState(id, label, value string, options []FormOption, description string) *FieldState {
	f := SelectFieldState(id, label, value, options, description)
	f.Placeholder = "Press Enter to fetch models"
	return f
}

// ThinkingFieldState builds a thinking-level cycle field over ThinkingLevels
// with the standard "Provider default" placeholder.
func ThinkingFieldState(id, current, description string) *FieldState {
	f := CycleFieldState(id, "Thinking level", ThinkingLevels, current, description)
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
		detail := ""
		if model.ContextWindow > 0 {
			detail = rendering.FormatTokens(model.ContextWindow)
		}
		options = append(options, FormOption{ID: model.ID, Label: label, Detail: detail})
	}
	return options
}
