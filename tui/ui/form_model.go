package ui

import (
	"fmt"
	"strings"
)

// FieldType identifies the kind of form field.
//
// Indicator contract — each type renders one canonical affordance, owned by
// FormField.paint* (form_field.go). New field types must pick an affordance
// from this list or extend the list, never invent an ad-hoc glyph:
//
//	FieldText, FieldPassword  free-form TextField; no indicator
//	FieldSelect               "▾" right-aligned when closed, "▴" when open
//	FieldMultiSelect          "▾" right-aligned when closed, "▴" when open;
//	                          option rows prefix "[x]"/"[ ]"
//	FieldCycle                "←" left edge, "→" right edge (value centered)
//	FieldToggle, FieldCheckbox "[x]" / "[ ]" inline
//	FieldNav                  drill-in row: label + trailing ▸; detail on the next row
//	FieldSeparator            label + "─" rule; not focusable
type FieldType int

const (
	FieldText FieldType = iota
	FieldPassword
	FieldSelect
	FieldCycle
	FieldToggle
	FieldCheckbox
	FieldNav
	FieldSeparator
	FieldMultiSelect
)

// FieldValue is a typed wrapper for field values.
type FieldValue struct {
	kind    FieldType
	str     string
	b       bool
	strings []string
}

func StringValue(s string) FieldValue     { return FieldValue{kind: FieldText, str: s} }
func BoolValue(b bool) FieldValue         { return FieldValue{kind: FieldToggle, b: b} }
func StringsValue(ss []string) FieldValue { return FieldValue{kind: FieldMultiSelect, strings: ss} }

func (v FieldValue) String() string {
	switch v.kind {
	case FieldText, FieldPassword:
		return v.str
	case FieldMultiSelect:
		return strings.Join(v.strings, ", ")
	case FieldToggle, FieldCheckbox:
		if v.b {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func (v FieldValue) Bool() bool {
	switch v.kind {
	case FieldToggle, FieldCheckbox:
		return v.b
	default:
		return false
	}
}

func (v FieldValue) Strings() []string {
	if v.kind == FieldMultiSelect {
		return v.strings
	}
	return nil
}

func (v FieldValue) IsEmpty() bool {
	switch v.kind {
	case FieldText, FieldPassword:
		return v.str == ""
	case FieldToggle, FieldCheckbox:
		return !v.b
	case FieldSelect, FieldMultiSelect:
		return len(v.strings) == 0
	default:
		return true
	}
}

func (v FieldValue) Equal(other FieldValue) bool {
	if v.kind != other.kind {
		return false
	}
	switch v.kind {
	case FieldText, FieldPassword:
		return v.str == other.str
	case FieldToggle, FieldCheckbox:
		return v.b == other.b
	case FieldMultiSelect:
		if len(v.strings) != len(other.strings) {
			return false
		}
		for i := range v.strings {
			if v.strings[i] != other.strings[i] {
				return false
			}
		}
		return true
	default:
		return true
	}
}

// FormOption is a choice in a select/cycle field.
type FormOption struct {
	ID       string
	Label    string
	Detail   string
	Disabled bool
}

// FieldState tracks the current state of a single field.
type FieldState struct {
	ID            string
	Label         string
	Description   string
	Type          FieldType
	Value         FieldValue
	Initial       FieldValue
	Default       FieldValue
	Errors        []string
	Dirty         bool
	Touched       bool
	Disabled      bool
	ReadOnly      bool
	Loading       bool
	LoadMessage   string
	Placeholder   string
	SubmitAction  string
	Required      bool
	Inherited     bool
	InheritedFrom string
	Options       []FormOption
	Validator     func(value FieldValue) []string
	Target        string // for FieldNav: target page/focus ID
}

func (f *FieldState) HasErrors() bool { return len(f.Errors) > 0 }

func (f *FieldState) Validate() {
	if f.Validator != nil {
		f.Errors = f.Validator(f.Value)
	} else {
		f.Errors = nil
	}
	if f.Required && f.Value.IsEmpty() {
		f.Errors = append(f.Errors, fmt.Sprintf("%s is required", f.Label))
	}
}

// FormModel holds the values and state for all fields in a form.
type FormModel struct {
	fields    []*FieldState
	fieldByID map[string]*FieldState
	pages     [][]string // nil or single-page = all fields in one page
	onChanged func(fieldID string)
}

func NewFormModel() *FormModel {
	return &FormModel{
		fieldByID: make(map[string]*FieldState),
	}
}

func (m *FormModel) Pages() [][]string { return m.pages }

func (m *FormModel) SetPages(p [][]string) { m.pages = p }

func (m *FormModel) OnChanged(fn func(fieldID string)) { m.onChanged = fn }

func (m *FormModel) AddField(f *FieldState) {
	m.fields = append(m.fields, f)
	m.fieldByID[f.ID] = f
}

func (m *FormModel) Field(id string) *FieldState { return m.fieldByID[id] }

func (m *FormModel) Fields() []*FieldState { return m.fields }

func (m *FormModel) FieldCount() int { return len(m.fields) }

func (m *FormModel) SetValue(id string, v FieldValue) {
	f := m.fieldByID[id]
	if f == nil || f.ReadOnly {
		return
	}
	if f.Value.Equal(v) {
		return
	}
	f.Value = v
	f.Dirty = !f.Value.Equal(f.Initial)
	f.Validate()
	if m.onChanged != nil {
		m.onChanged(id)
	}
}

func (m *FormModel) AnyDirty() bool {
	for _, f := range m.fields {
		if f.Dirty {
			return true
		}
	}
	return false
}

func (m *FormModel) AnyErrors() bool {
	for _, f := range m.fields {
		if f.HasErrors() {
			return true
		}
	}
	return false
}

func (m *FormModel) ValidateAll() {
	for _, f := range m.fields {
		f.Validate()
	}
}

func (m *FormModel) Commit() {
	for _, f := range m.fields {
		f.Initial = f.Value
		f.Dirty = false
	}
}

func (m *FormModel) Reset() {
	for _, f := range m.fields {
		f.Value = f.Initial
		f.Dirty = false
	}
	m.ValidateAll()
}
