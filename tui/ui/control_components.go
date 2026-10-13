package ui

import (
	"encoding/json"
	"math"
	"strings"

	"crude/types/control"
)

func (b *Button) ControlActions() []control.Descriptor {
	return controlActions(!b.disabled, b.actionID)
}
func (b *Button) HandleControl(_ EventContext, op ControlOperation) (EventResult, error) {
	if op.Verb != "activate" {
		return unsupportedControl()
	}
	if b.disabled {
		return EventResult{}, controlError(control.Unavailable, "button disabled")
	}
	return b.activateControl(), nil
}
func (b *Button) activateControl() EventResult {
	return EventResult{Handled: true, Action: &Action{ID: b.actionID, Payload: b.label}, Invalidate: InvalidatePaint}
}
func (b *ToggleButton) ControlActions() []control.Descriptor {
	return controlActions(!b.disabled, b.actionID)
}
func (b *ToggleButton) HandleControl(_ EventContext, op ControlOperation) (EventResult, error) {
	if b.disabled {
		return EventResult{}, controlError(control.Unavailable, "toggle disabled")
	}
	if op.Verb == "set_checked" {
		var value bool
		if err := json.Unmarshal(op.Value, &value); err != nil {
			return EventResult{}, err
		}
		if value == b.checked {
			return controlResult("", nil), nil
		}
	} else if op.Verb != "activate" {
		return unsupportedControl()
	}
	// The owning action handler, not this projected toggle, owns checked state.
	return controlResult(b.actionID, nil), nil
}

type controlItem struct {
	ID, Name                    string
	Enabled, Selected, Expanded bool
	Height                      int
}

func controlItemCount(c Component) int {
	switch c := c.(type) {
	case *List:
		return len(c.items)
	case *Menu:
		return len(c.items)
	case *Table:
		return len(c.rows)
	case *Tree:
		return c.count()
	case *Tabs:
		return len(c.items)
	case *VirtualList:
		return len(c.items)
	case *FormField:
		return len(c.field.Options)
	}
	return 0
}
func controlItemAt(c Component, i int) controlItem {
	out := controlItem{Enabled: true, Height: 1}
	switch c := c.(type) {
	case *List:
		it := c.items[i]
		out.ID = it.ID
		out.Name = it.Label
		out.Enabled = it.Selectable()
		out.Selected = c.sel.Index() == i
	case *Menu:
		it := c.items[i]
		out.ID = it.ID
		out.Name = it.Label
		out.Enabled = it.Selectable()
		out.Selected = c.sel.Index() == i
	case *Table:
		it := c.rows[i]
		out.ID = it.ID
		labels := make([]string, 0, len(it.Cells))
		for _, cell := range it.Cells {
			labels = append(labels, cell.Text)
		}
		out.Name = strings.Join(labels, " · ")
		out.Selected = c.sel.Index() == i
	case *Tree:
		out.Selected = c.sel.Index() == i
		if c.useRows {
			it := c.rows[i]
			out.ID = it.ID
			out.Name = it.Label
			out.Expanded = it.Expanded
		} else {
			it := c.items[i]
			out.ID = it.ID
			out.Name = it.Label
		}
	case *Tabs:
		it := c.items[i]
		out.ID = it.ID
		out.Name = it.Label
		out.Selected = c.active == i
	case *VirtualList:
		it := c.items[i]
		out.ID = it.ID
		out.Name = it.Label
		out.Height = max(1, it.Height)
		out.Selected = c.sel.Index() == i
	case *FormField:
		it := c.field.Options[i]
		out.ID = it.ID
		out.Name = it.Label
		out.Enabled = !it.Disabled
		out.Selected = c.field.Value.String() == it.ID
		for _, id := range c.field.Value.Strings() {
			out.Selected = out.Selected || id == it.ID
		}
	}
	return out
}
func controlItemNode(c Component, i int, parent control.Node) control.Node {
	it := controlItemAt(c, i)
	out := parent
	out.ParentID = parent.ID
	out.ID = parent.ID + "/item/" + it.ID
	out.Role = control.Item
	out.Name = it.Name
	out.Value = nil
	out.Actions = nil
	out.Bounds = nil
	out.State = control.NodeState{Enabled: parent.State.Enabled && it.Enabled, Selected: it.Selected, Expanded: it.Expanded}
	// Items carry exact IDs and collection fences. Mutations target the parent
	// control with ItemID; they are not separately mounted components.
	return out
}
func controlSource(c Component) any {
	if t, ok := c.(*TextField); ok {
		a, b := t.edit.SelectionRange()
		return []any{t.Value(), t.edit.Cursor(), a, b, t.readOnly, t.disabled}
	}
	if t, ok := c.(*TextArea); ok {
		return []any{t.EditState(), t.readOnly}
	}
	if f, ok := c.(*FilterField); ok {
		return controlSource(f.field)
	}
	if f, ok := c.(*SearchField); ok {
		return controlSource(f.field)
	}
	if f, ok := c.(*FormField); ok {
		return struct {
			Value                    FieldValue
			Disabled, ReadOnly, Open bool
			Options                  []FormOption
			Text                     any
		}{f.field.Value, f.field.Disabled, f.field.ReadOnly, f.selectOpen, f.field.Options, func() any {
			if f.control != nil {
				return controlSource(f.control)
			}
			if f.area != nil {
				return controlSource(f.area)
			}
			return controlValue(f)
		}()}
	}
	n := controlItemCount(c)
	if n > 0 {
		items := make([]controlItem, n)
		for i := range items {
			items[i] = controlItemAt(c, i)
		}
		var offset int
		if s, ok := c.(interface{ Scroll() *ScrollModel }); ok {
			offset = s.Scroll().Offset()
		}
		if tabs, ok := c.(*Tabs); ok {
			offset = tabs.scroll
		}
		return struct {
			Items  []controlItem
			Value  any
			Offset int
		}{items, controlValue(c), offset}
	}
	return controlValue(c)
}
func controlValue(c Component) any {
	switch c := c.(type) {
	case *TextField:
		if c.TextSecret() {
			return nil
		}
		return c.Value()
	case *TextArea:
		return c.Value()
	case *FilterField:
		return controlValue(c.field)
	case *SearchField:
		return controlValue(c.field)
	case *FormField:
		if c.field.Type == FieldPassword {
			return nil
		}
		if c.field.Type == FieldToggle || c.field.Type == FieldCheckbox {
			return c.field.Value.Bool()
		}
		if c.field.Type == FieldMultiSelect {
			return c.field.Value.Strings()
		}
		return c.field.Value.String()
	case *KeyValueGrid:
		return c.Rows()
	case *ToggleButton:
		return c.checked
	case *ScrollbarControl:
		if c.scroll != nil {
			return c.scroll.Offset()
		}
	case *Viewport:
		return []int{c.scrollX, c.scrollY}
	case *SplitDivider:
		if c.model != nil {
			return []any{c.model.first, c.model.fraction}
		}
	}
	return nil
}
func collectionActions(c Component, activate string) []control.Descriptor {
	return controlActions(true, "select", "set_offset", "scroll", activate)
}
func collectionControl(c Component, op ControlOperation, activate string) (EventResult, error) {
	if op.Verb == "set_offset" || op.Verb == "scroll" {
		if s, ok := c.(interface{ Scroll() *ScrollModel }); ok {
			m := s.Scroll()
			if op.Verb == "scroll" {
				m.ScrollBy(op.Offset)
			} else {
				m.ScrollTo(op.Offset)
			}
			return controlResult("", nil), nil
		}
		return unsupportedControl()
	}
	if op.Verb != "select" && op.Verb != "activate" && op.Verb != "expand" && op.Verb != "close" {
		return unsupportedControl()
	}
	index := -1
	for i := 0; i < controlItemCount(c); i++ {
		if controlItemAt(c, i).ID == op.ItemID {
			if index >= 0 {
				return EventResult{}, controlError(control.Invalid, "ambiguous item ID")
			}
			index = i
		}
	}
	if index < 0 {
		return EventResult{}, controlError(control.Unavailable, "item ID not found")
	}
	if !controlItemAt(c, index).Enabled {
		return EventResult{}, controlError(control.Unavailable, "item disabled")
	}
	if op.Verb == "expand" {
		t, ok := c.(*Tree)
		if !ok || !t.useRows || !t.rows[index].IsDir {
			return unsupportedControl()
		}
		var expanded bool
		if err := json.Unmarshal(op.Value, &expanded); err != nil {
			return EventResult{}, err
		}
		if expanded == t.rows[index].Expanded {
			return controlResult("", nil), nil
		}
		return controlResult(t.toggle, op.ItemID), nil
	}
	if op.Verb == "close" {
		t, ok := c.(*Tabs)
		if !ok || !t.items[index].Closable {
			return unsupportedControl()
		}
		return controlResult("tabs.close", op.ItemID), nil
	}
	var payload any
	switch c := c.(type) {
	case *List:
		c.SelectIndex(index)
		payload = c.items[index]
	case *Menu:
		c.SelectIndex(index)
		payload = c.items[index]
	case *Table:
		c.SelectIndex(index)
		payload = c.rows[index]
	case *Tree:
		c.SelectIndex(index)
		payload = op.ItemID
	case *Tabs:
		c.active = index
		c.manualScroll = false
		c.pointerFocus = true
		c.ranges = c.computeRanges(c.Bounds().W)
		payload = op.ItemID
	case *VirtualList:
		c.SelectIndex(index)
		c.ensureSelectedVisible()
		payload = c.items[index]
	}
	if op.Verb == "activate" || isControlTabs(c) {
		return controlResult(activate, payload), nil
	}
	if _, ok := c.(*Tree); ok {
		return controlResult("tree.select", op.ItemID), nil
	}
	return controlResult("", nil), nil
}
func isControlTabs(c Component) bool                 { _, ok := c.(*Tabs); return ok }
func (c *List) ControlActions() []control.Descriptor { return collectionActions(c, c.activate) }
func (c *List) HandleControl(_ EventContext, op ControlOperation) (EventResult, error) {
	return collectionControl(c, op, c.activate)
}
func (c *Menu) ControlActions() []control.Descriptor { return collectionActions(c, c.activate) }
func (c *Menu) HandleControl(_ EventContext, op ControlOperation) (EventResult, error) {
	return collectionControl(c, op, c.activate)
}
func (c *Table) ControlActions() []control.Descriptor { return collectionActions(c, c.activate) }
func (c *Table) HandleControl(_ EventContext, op ControlOperation) (EventResult, error) {
	return collectionControl(c, op, c.activate)
}
func (c *Tree) ControlActions() []control.Descriptor {
	return append(collectionActions(c, c.activate), controlActions(true, c.toggle)...)
}
func (c *Tree) HandleControl(_ EventContext, op ControlOperation) (EventResult, error) {
	return collectionControl(c, op, c.activate)
}
func (c *Tabs) ControlActions() []control.Descriptor {
	return controlActions(true, "tabs.activate", "tabs.close", "select", "set_offset", "scroll")
}
func (c *Tabs) HandleControl(_ EventContext, op ControlOperation) (EventResult, error) {
	if op.Verb == "scroll" {
		c.ScrollBy(op.Offset)
		return controlResult("", nil), nil
	}
	if op.Verb == "set_offset" {
		c.SetScroll(op.Offset)
		return controlResult("", nil), nil
	}
	return collectionControl(c, op, "tabs.activate")
}
func (c *VirtualList) ControlActions() []control.Descriptor {
	if c.pointerPolicy == PointerPolicyPassthrough {
		return nil
	}
	return collectionActions(c, c.activate)
}
func (c *VirtualList) HandleControl(_ EventContext, op ControlOperation) (EventResult, error) {
	if c.pointerPolicy == PointerPolicyPassthrough {
		return unsupportedControl()
	}
	return collectionControl(c, op, c.activate)
}
func (c *ScrollbarControl) ControlActions() []control.Descriptor {
	return controlActions(c.scroll != nil, "set_offset", "scroll")
}
func (c *ScrollbarControl) HandleControl(_ EventContext, op ControlOperation) (EventResult, error) {
	if c.scroll == nil {
		return unsupportedControl()
	}
	old := c.scroll.Offset()
	switch op.Verb {
	case "scroll":
		c.scroll.ScrollBy(op.Offset)
	case "set_offset":
		c.scroll.ScrollTo(op.Offset)
	default:
		return unsupportedControl()
	}
	return EventResult{Handled: true, Invalidate: c.scrollInvalidation(old)}, nil
}
func (c *Viewport) ControlActions() []control.Descriptor {
	return controlActions(true, "set_offset", "scroll")
}
func (c *Viewport) HandleControl(_ EventContext, op ControlOperation) (EventResult, error) {
	x, y := op.SecondaryOffset, op.Offset
	if op.Verb == "scroll" {
		x += c.scrollX
		y += c.scrollY
	} else if op.Verb != "set_offset" {
		return unsupportedControl()
	}
	c.SetScrollOffset(min(max(0, x), max(0, c.resolvedW-c.Bounds().W)), min(max(0, y), max(0, c.resolvedH-c.Bounds().H)))
	return controlResult("", nil), nil
}
func (c *SplitDivider) ControlActions() []control.Descriptor {
	return controlActions(c.model != nil, ActionSplitResize)
}
func (c *SplitDivider) HandleControl(_ EventContext, op ControlOperation) (EventResult, error) {
	if c.model == nil || op.Verb != "resize" {
		return unsupportedControl()
	}
	if op.Fraction != nil {
		if math.IsNaN(*op.Fraction) || math.IsInf(*op.Fraction, 0) || *op.Fraction < 0 || *op.Fraction > 1 {
			return EventResult{}, controlError(control.Invalid, "invalid split fraction")
		}
		c.model.SetFraction(*op.Fraction)
		first, _, _ := c.model.Resolve(op.SecondaryOffset)
		c.currentFirst = first
		return controlResult(ActionSplitResize, SplitResize{ID: c.ID(), First: first}), nil
	}
	if op.Offset < 0 {
		return EventResult{}, controlError(control.Invalid, "negative split size")
	}
	c.model.SetFirstSize(op.Offset)
	return c.resizeResult(), nil
}
