package ui

import (
	"crude/types/control"
	"encoding/json"
	"strings"
)

func textControlActions(editable bool, submit string) []control.Descriptor {
	out := controlActions(true, "select_range")
	if editable {
		out = append(out, controlActions(true, "set_value", "replace", "undo", "redo")...)
	}
	return append(out, controlActions(editable, submit)...)
}
func textOperationValue(op ControlOperation) (string, error) {
	var value string
	err := json.Unmarshal(op.Value, &value)
	return value, err
}
func (t *TextField) ControlActions() []control.Descriptor {
	return textControlActions(t.TextEditable(), t.submitID)
}
func (t *TextField) HandleControl(_ EventContext, op ControlOperation) (EventResult, error) {
	if t.disabled || t.TextSecret() {
		return EventResult{}, controlError(control.Unavailable, "field disabled")
	}
	if op.Verb == "select_range" {
		if op.Start < 0 || op.End < 0 || op.Start > t.edit.Len() || op.End > t.edit.Len() {
			return EventResult{}, controlError(control.Invalid, "invalid rune range")
		}
		t.edit.SetCursor(op.Start, false)
		t.edit.SetCursor(op.End, true)
	} else {
		if !t.TextEditable() {
			return EventResult{}, controlError(control.Unavailable, "field read only")
		}
		switch op.Verb {
		case "set_value", "replace":
			value, err := textOperationValue(op)
			if err != nil {
				return EventResult{}, err
			}
			if strings.ContainsAny(value, "\r\n") {
				return EventResult{}, controlError(control.Invalid, "single line value required")
			}
			if op.Verb == "set_value" {
				t.edit.SelectAll()
			}
			if value == "" {
				t.edit.DeleteSelection()
			} else {
				t.edit.Insert(value)
			}
		case "undo":
			t.edit.Undo()
		case "redo":
			t.edit.Redo()
		case "activate":
			if t.submitID == "" {
				return unsupportedControl()
			}
			return controlResult(t.submitID, t.Value()), nil
		default:
			return unsupportedControl()
		}
	}
	t.cursor.MarkActivity()
	t.ensureCursorVisible()
	return controlResult("text.changed", string(t.ID())), nil
}
func (t *TextArea) ControlActions() []control.Descriptor {
	return textControlActions(t.TextEditable(), t.submitID)
}
func (t *TextArea) HandleControl(_ EventContext, op ControlOperation) (EventResult, error) {
	if op.Verb == "select_range" {
		n := len([]rune(t.Value()))
		if op.Start < 0 || op.End < 0 || op.Start > n || op.End > n {
			return EventResult{}, controlError(control.Invalid, "invalid rune range")
		}
		t.edit.SetSelection(op.Start, op.End)
	} else {
		if !t.TextEditable() {
			return EventResult{}, controlError(control.Unavailable, "area read only")
		}
		switch op.Verb {
		case "set_value", "replace":
			value, err := textOperationValue(op)
			if err != nil {
				return EventResult{}, err
			}
			if op.Verb == "set_value" {
				t.edit.SetSelection(0, len([]rune(t.Value())))
			}
			t.InsertString(value)
		case "undo":
			t.edit.Undo()
		case "redo":
			t.edit.Redo()
		case "activate":
			if t.submitID == "" {
				return unsupportedControl()
			}
			return controlResult(t.submitID, t.Value()), nil
		default:
			return unsupportedControl()
		}
	}
	t.cursor.MarkActivity()
	t.EnsureCursorVisible()
	return controlResult("text.changed", string(t.ID())), nil
}
func (f *FilterField) ControlActions() []control.Descriptor { return f.field.ControlActions() }
func (f *FilterField) HandleControl(ctx EventContext, op ControlOperation) (EventResult, error) {
	before := f.Value()
	res, err := f.field.HandleControl(ctx, op)
	if err == nil && before != f.Value() && f.onChange != nil {
		f.onChange(f.Value())
	}
	return res, err
}
func (s *SearchField) ControlActions() []control.Descriptor { return s.field.ControlActions() }
func (s *SearchField) HandleControl(ctx EventContext, op ControlOperation) (EventResult, error) {
	before := s.Value()
	res, err := s.field.HandleControl(ctx, op)
	if err == nil && before != s.Value() {
		res.Cmd = combineCmd(res.Cmd, s.Schedule())
	}
	return res, err
}
func (f *Form) ControlChildren() []Component {
	out := make([]Component, 0, len(f.fields))
	for _, field := range f.fields {
		out = append(out, field)
	}
	return out
}
func (f *Form) ControlActions() []control.Descriptor {
	return controlActions(true, "set_offset", "scroll")
}
func (f *Form) HandleControl(_ EventContext, op ControlOperation) (EventResult, error) {
	switch op.Verb {
	case "set_offset":
		f.scroll.ScrollTo(op.Offset)
	case "scroll":
		f.scroll.ScrollBy(op.Offset)
	default:
		return unsupportedControl()
	}
	return controlResult("", nil), nil
}
func (ff *FormField) ControlActions() []control.Descriptor {
	f := ff.field
	if f == nil || f.skipFocus() || f.ReadOnly || f.Type == FieldPassword {
		return nil
	}
	switch f.Type {
	case FieldText, FieldPassword, FieldTextArea:
		return textControlActions(true, f.SubmitAction)
	case FieldNav:
		return controlActions(true, f.Target)
	case FieldSelect, FieldMultiSelect:
		return controlActions(true, "form.select.open", "form.changed", "select", "set_value", "dismiss")
	case FieldCycle, FieldToggle, FieldCheckbox:
		return controlActions(true, "form.changed", "select", "set_value")
	case FieldKeyValues:
		return controlActions(true, "form.changed", "set_value", "append", "set", "remove")
	default:
		return nil
	}
}
func (ff *FormField) HandleControl(ctx EventContext, op ControlOperation) (EventResult, error) {
	f := ff.field
	if f == nil || f.skipFocus() || f.ReadOnly || f.Type == FieldPassword {
		return EventResult{}, controlError(control.Unavailable, "form field is decorative, disabled or read only")
	}
	ff.Sync()
	var res EventResult
	var err error
	switch f.Type {
	case FieldText, FieldPassword:
		res, err = ff.control.HandleControl(ctx, op)
		if err == nil {
			ff.setValue(StringValue(ff.control.Value()))
		}
	case FieldTextArea:
		res, err = ff.area.HandleControl(ctx, op)
		if err == nil {
			ff.setValue(StringValue(ff.area.Value()))
		}
	case FieldNav:
		if op.Verb != "activate" {
			return unsupportedControl()
		}
		return controlResult(f.Target, f.ID), nil
	case FieldSelect, FieldMultiSelect, FieldCycle:
		if op.Verb == "expand" && (f.Type == FieldSelect || f.Type == FieldMultiSelect) {
			ff.selectOpen = true
			ff.syncSelectItems(false)
			return controlResult("form.select.open", f.ID), nil
		}
		if op.Verb == "dismiss" {
			ff.closeSelect()
			return controlResult("", nil), nil
		}
		if op.Verb != "select" && op.Verb != "set_value" {
			return unsupportedControl()
		}
		if f.Type == FieldMultiSelect && op.Verb == "set_value" {
			var ids []string
			if err := json.Unmarshal(op.Value, &ids); err != nil {
				return EventResult{}, err
			}
			seen := make(map[string]bool, len(ids))
			for _, id := range ids {
				if seen[id] {
					return EventResult{}, controlError(control.Invalid, "duplicate selection")
				}
				seen[id] = true
				matches := 0
				for _, option := range f.Options {
					if option.ID == id && !option.Disabled {
						matches++
					}
				}
				if matches != 1 {
					return EventResult{}, controlError(control.Unavailable, "selection unavailable or ambiguous")
				}
			}
			ff.setValue(StringsValue(ids))
			ff.closeSelect()
			break
		}
		id := op.ItemID
		if op.Verb == "set_value" {
			id, err = textOperationValue(op)
			if err != nil {
				return res, err
			}
		}
		found := false
		for _, option := range f.Options {
			if option.ID == id {
				if found {
					return EventResult{}, controlError(control.Invalid, "ambiguous option")
				}
				if option.Disabled {
					return EventResult{}, controlError(control.Unavailable, "option disabled")
				}
				found = true
			}
		}
		if !found {
			return EventResult{}, controlError(control.Unavailable, "option unavailable")
		}
		if f.Type == FieldSelect || f.Type == FieldMultiSelect {
			// Exact choice uses the same dropdown commit as human activation. Filtering
			// must not make a known option unreachable to semantic selection.
			ff.selectQuery = ""
			ff.syncSelectItems(false)
			for i, item := range ff.selectList.items {
				if payload, ok := item.Payload.(string); ok && payload == id {
					ff.selectList.SelectIndex(i)
					return ff.commitSelect(), nil
				}
			}
			return unsupportedControl()
		}
		ff.setValue(StringValue(id))
	case FieldToggle, FieldCheckbox:
		var value bool
		switch op.Verb {
		case "activate":
			value = !f.Value.Bool()
		case "set_value", "set_checked":
			if err = json.Unmarshal(op.Value, &value); err != nil {
				return res, err
			}
		default:
			return unsupportedControl()
		}
		ff.setValue(BoolValue(value))
	case FieldKeyValues:
		res, err = ff.grid.HandleControl(ctx, op)
		if err == nil {
			ff.setValue(StringValue(ff.grid.Value()))
		}
	default:
		return unsupportedControl()
	}
	if err != nil {
		return res, err
	}
	if op.Verb == "select_range" {
		return res, nil
	}
	if res.Action == nil || res.Action.ID == "text.changed" {
		res.Action = &Action{ID: "form.changed", Payload: f.ID}
	}
	res.Handled = true
	res.Invalidate |= InvalidateLayout | InvalidatePaint
	return res, nil
}

func (g *KeyValueGrid) ControlActions() []control.Descriptor {
	return controlActions(!g.readOnly, "set_value", "append", "set", "remove")
}

func (g *KeyValueGrid) HandleControl(_ EventContext, op ControlOperation) (EventResult, error) {
	if g.readOnly {
		return EventResult{}, controlError(control.Unavailable, "grid read only")
	}
	rows := g.Rows()
	switch op.Verb {
	case "set_value":
		if err := json.Unmarshal(op.Value, &rows); err != nil {
			return EventResult{}, err
		}
	case "append", "set":
		var row KeyValuePair
		if err := json.Unmarshal(op.Value, &row); err != nil {
			return EventResult{}, err
		}
		if op.Verb == "append" {
			rows = append(rows, row)
		} else {
			if op.Offset < 0 || op.Offset >= len(rows) {
				return EventResult{}, controlError(control.Invalid, "grid row unavailable")
			}
			rows[op.Offset] = row
		}
	case "remove":
		if op.Offset < 0 || op.Offset >= len(rows) {
			return EventResult{}, controlError(control.Invalid, "grid row unavailable")
		}
		rows = append(rows[:op.Offset:op.Offset], rows[op.Offset+1:]...)
	default:
		return unsupportedControl()
	}
	for _, row := range rows {
		if strings.ContainsAny(row.Key+row.Value, "\r\n") {
			return EventResult{}, controlError(control.Invalid, "grid cells must be single line")
		}
	}
	g.BlurCell()
	g.SetRows(rows)
	g.emit()
	return controlResult("text.changed", string(g.ID())), nil
}
