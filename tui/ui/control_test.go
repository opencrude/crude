package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/types/control"
)

func controlTestHost(c Component) *Host {
	h := NewHost(theme.DefaultDark(), c)
	h.Resize(50, 10)
	h.Layout()
	return h
}
func inspectControl(t *testing.T, h *Host, id ComponentID) control.Node {
	t.Helper()
	cursor := ""
	for {
		p, err := h.InspectControls(control.Target{WorkspaceID: "w"}, control.PageRequest{Limit: 128, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range p.Nodes {
			if n.ID == string(id) {
				return n
			}
		}
		if p.NextCursor == "" {
			t.Fatalf("missing %s", id)
		}
		cursor = p.NextCursor
	}
}
func operateControl(t *testing.T, h *Host, id ComponentID, op ControlOperation) EventResult {
	t.Helper()
	n := inspectControl(t, h, id)
	res, err := h.Control(id, n.OpenEpoch, &n.Revision, op)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

type controlTestParent struct {
	Base
	child  Component
	action string
}

func (p *controlTestParent) Children() []Component { return []Component{p.child} }
func (p *controlTestParent) Layout(ctx LayoutContext, r Rect) {
	p.SetBounds(r)
	p.child.Layout(ctx, Rect{X: 2, Y: 1, W: r.W - 2, H: r.H - 1})
}
func (p *controlTestParent) HandleAction(_ EventContext, a Action) EventResult {
	p.action = a.ID
	return controlResult("translated", a.ID)
}
func TestHostControlLifetimeAndHumanAction(t *testing.T) {
	b := NewButton("root/button", "Run", "exact.owner.run")
	parent := &controlTestParent{Base: NewBase("root"), child: b}
	h := controlTestHost(parent)
	n := inspectControl(t, h, b.ID())
	if n.Bounds.X != 2 || n.Bounds.Y != 1 || n.Actions[0].ID != "exact.owner.run" {
		t.Fatalf("descriptor %+v", n)
	}
	res, err := h.Control(b.ID(), n.OpenEpoch, &n.Revision, ControlOperation{Verb: "activate"})
	if err != nil || res.Action.ID != "translated" || parent.action != "exact.owner.run" {
		t.Fatalf("action %+v %v", res, err)
	}
	h.Focus(parent.ID())
	h.MarkPaint()
	h.SetFrame(999)
	if next := inspectControl(t, h, b.ID()); next.Revision != n.Revision {
		t.Fatal("presentation changed source revision")
	}
	h.Focus(b.ID())
	human := h.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if human.Action.ID != res.Action.ID {
		t.Fatal("human and semantic actions differ")
	}
	b.SetDisabled(true)
	before := parent.action
	_, err = h.Control(b.ID(), n.OpenEpoch, nil, ControlOperation{Verb: "activate"})
	if err == nil || parent.action != before {
		t.Fatal("disabled button acted")
	}
	parent.child = NewButton(b.ID(), "Other", "other")
	h.MarkLayout()
	replacement := inspectControl(t, h, b.ID())
	if replacement.OpenEpoch == n.OpenEpoch {
		t.Fatal("replacement retained lifetime")
	}
	if _, err = h.Control(b.ID(), n.OpenEpoch, nil, ControlOperation{Verb: "activate"}); err == nil {
		t.Fatal("old lifetime accepted")
	}
	h.PushFocusScope(parent.ID()+"/modal", true)
	if _, err = h.Control(b.ID(), replacement.OpenEpoch, nil, ControlOperation{Verb: "activate"}); err == nil {
		t.Fatal("outside modal accepted")
	}
}
func TestHostControlFormModelAndSecret(t *testing.T) {
	model := NewFormModel()
	model.AddField(&FieldState{ID: "text", Label: "Text", Type: FieldText, Value: StringValue("old"), Initial: StringValue("old"), Validator: func(v FieldValue) []string {
		if v.String() == "invalid" {
			return []string{"bad"}
		}
		return nil
	}})
	model.AddField(&FieldState{ID: "secret", Label: "Password", Type: FieldPassword, Value: StringValue("never-leak-this")})
	model.AddField(&FieldState{ID: "choice", Label: "Choice", Type: FieldSelect, Value: StringValue("a"), Options: []FormOption{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta"}, {ID: "no", Label: "Disabled", Disabled: true}}})
	model.AddField(&FieldState{ID: "locked", Label: "Locked", Type: FieldText, Value: StringValue("stay"), ReadOnly: true})
	form := NewForm("form", model, "Settings", "", NewTokenSet(theme.DefaultDark()))
	h := controlTestHost(form)
	p, err := h.InspectControls(control.Target{}, control.PageRequest{Limit: 128})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "never-leak-this") {
		t.Fatal("password exposed")
	}
	res := operateControl(t, h, "form/text", ControlOperation{Verb: "set_value", Value: json.RawMessage(`"invalid"`)})
	if model.Field("text").Value.String() != "invalid" || !model.AnyErrors() || res.Action.ID != "form.changed" {
		t.Fatal("model validation missing")
	}
	operateControl(t, h, "form/text", ControlOperation{Verb: "undo"})
	if model.Field("text").Value.String() != "old" {
		t.Fatal("undo lost original")
	}
	operateControl(t, h, "form/choice", ControlOperation{Verb: "expand"})
	if !form.fields[2].selectOpen {
		t.Fatal("dropdown not open")
	}
	n := inspectControl(t, h, "form/text")
	if _, err := h.Control("form/text", n.OpenEpoch, nil, ControlOperation{Verb: "set_value", Value: json.RawMessage(`"blocked"`)}); err == nil {
		t.Fatal("dropdown input ownership bypassed")
	}
	operateControl(t, h, "form/choice", ControlOperation{Verb: "select", ItemID: "b"})
	if model.Field("choice").Value.String() != "b" || form.fields[2].selectOpen {
		t.Fatal("dropdown not committed")
	}
	n = inspectControl(t, h, "form/choice")
	if _, err := h.Control("form/choice", n.OpenEpoch, nil, ControlOperation{Verb: "select", ItemID: "no"}); err == nil {
		t.Fatal("disabled choice accepted")
	}
	n = inspectControl(t, h, "form/locked")
	if _, err := h.Control("form/locked", n.OpenEpoch, nil, ControlOperation{Verb: "set_value", Value: json.RawMessage(`"changed"`)}); err == nil || model.Field("locked").Value.String() != "stay" {
		t.Fatal("read only changed")
	}
}
func TestHostControlOffscreenPagesAndPaint(t *testing.T) {
	list := NewList("list", NewTokenSet(theme.DefaultDark()))
	items := make([]ListItem, 400)
	for i := range items {
		items[i] = ListItem{ID: fmt.Sprint(i), Label: fmt.Sprintf("row-%03d", i), Payload: map[string]string{"secret": "payload-must-not-appear"}}
	}
	list.SetItems(items)
	h := controlTestHost(list)
	p, err := h.InspectControls(control.Target{}, control.PageRequest{Limit: 10})
	if err != nil || len(p.Nodes) != 10 || p.NextCursor == "" {
		t.Fatalf("page %+v %v", p, err)
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "payload-must-not-appear") {
		t.Fatal("payload leaked")
	}
	next, err := h.InspectControls(control.Target{}, control.PageRequest{Limit: 10, Cursor: p.NextCursor})
	if err != nil || next.Nodes[0].Name != "row-009" {
		t.Fatalf("next %+v %v", next, err)
	}
	operateControl(t, h, "list", ControlOperation{Verb: "select", ItemID: "350"})
	buf := surface.NewCellBuffer(50, 10)
	h.Paint(buf)
	found := false
	for y := 0; y < 10; y++ {
		line := ""
		for x := 0; x < 50; x++ {
			line += string(buf.Get(x, y).Char)
		}
		found = found || strings.Contains(line, "row-350")
	}
	if !found {
		t.Fatal("offscreen selection not painted")
	}
	if _, err = h.InspectControls(control.Target{}, control.PageRequest{Limit: 10, Cursor: p.NextCursor}); err == nil {
		t.Fatal("stale cursor accepted")
	}
}
func TestHostControlFilterAndScrollbar(t *testing.T) {
	filter := NewFilterField("filter", NewTokenSet(theme.DefaultDark()))
	changed := ""
	filter.OnChanged(func(value string) { changed = value })
	h := controlTestHost(filter)
	operateControl(t, h, "filter", ControlOperation{Verb: "set_value", Value: json.RawMessage(`"query"`)})
	if changed != "query" {
		t.Fatal("filter owner not notified")
	}
	operateControl(t, h, "filter", ControlOperation{Verb: "undo"})
	if changed != "" {
		t.Fatal("filter undo owner not notified")
	}
	m := NewScrollModel()
	m.SetContent(100)
	m.SetViewport(10)
	sb := NewScrollbarControl("scroll", m, NewTokenSet(theme.DefaultDark()))
	observed := -1
	sb.OnScroll(func(_, new int) { observed = new })
	h = controlTestHost(sb)
	operateControl(t, h, "scroll", ControlOperation{Verb: "set_offset", Offset: 50})
	if observed != 50 || m.Offset() != 50 {
		t.Fatal("scroll mirror not notified")
	}
}

func TestHostDescribeUnderlyingActions(t *testing.T) {
	cases := []struct {
		component Component
		op        ControlOperation
		action    string
		effect    control.Effect
	}{
		{NewButton("button", "Approve", "exact.approval"), ControlOperation{Verb: "activate"}, "exact.approval", ""},
		{NewTextField("text", nil), ControlOperation{Verb: "set_value", Value: json.RawMessage(`"v"`)}, "ui.control.set_value", control.Write},
	}
	for _, tc := range cases {
		h := controlTestHost(tc.component)
		d, err := h.DescribeControl(tc.component.ID(), tc.op)
		if err != nil || d.ID != tc.action || d.Effect != tc.effect || d.Input.ID == "" {
			t.Fatalf("%+v %v", d, err)
		}
	}
}
func TestHostControlCollectionOwners(t *testing.T) {
	menu := NewMenu("menu", nil)
	menu.SetItems([]MenuItem{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}})
	table := NewTable("table", nil)
	table.SetRows([]TableRow{{ID: "a"}, {ID: "b"}})
	tree := NewTree("tree", nil)
	tree.SetRows([]TreeRow{{ID: "a", Label: "A"}, {ID: "b", Label: "B", IsDir: true}})
	tabs := NewTabs("tabs", nil)
	tabs.SetItems([]TabItem{{ID: "a", Label: "A"}, {ID: "b", Label: "B", Closable: true}}, "a")
	virtual := NewVirtualList("virtual", nil)
	virtual.SetItems([]VirtItem{{ID: "a", Label: "A", Height: 1}, {ID: "b", Label: "B", Height: 20}})
	for _, c := range []Component{menu, table, tree, tabs, virtual} {
		t.Run(string(c.ID()), func(t *testing.T) {
			h := controlTestHost(c)
			res := operateControl(t, h, c.ID(), ControlOperation{Verb: "activate", ItemID: "b"})
			if res.Action == nil {
				t.Fatal("activation lost")
			}
			if !controlItemAt(c, 1).Selected {
				t.Fatal("selection not updated")
			}
		})
	}
	h := controlTestHost(tree)
	res := operateControl(t, h, "tree", ControlOperation{Verb: "expand", ItemID: "b", Value: json.RawMessage(`true`)})
	if res.Action.ID != "tree.toggle" || res.Action.Payload != "b" || tree.rows[1].Expanded {
		t.Fatal("tree projection mutated instead of owner intent")
	}
	h = controlTestHost(tabs)
	res = operateControl(t, h, "tabs", ControlOperation{Verb: "close", ItemID: "b"})
	if res.Action.ID != "tabs.close" || len(tabs.items) != 2 {
		t.Fatal("tab topology mutated instead of owner intent")
	}
}
func TestHostControlTextAreaRangeAndUndo(t *testing.T) {
	area := NewTextArea("area", nil, true)
	area.SetValue("abc\n雪")
	h := controlTestHost(area)
	operateControl(t, h, "area", ControlOperation{Verb: "select_range", Start: 0, End: 3})
	operateControl(t, h, "area", ControlOperation{Verb: "replace", Value: json.RawMessage(`"xyz"`)})
	if area.Value() != "xyz\n雪" {
		t.Fatal(area.Value())
	}
	operateControl(t, h, "area", ControlOperation{Verb: "undo"})
	if area.Value() != "abc\n雪" {
		t.Fatal("area undo failed")
	}
	operateControl(t, h, "area", ControlOperation{Verb: "redo"})
	if area.Value() != "xyz\n雪" {
		t.Fatal("area redo failed")
	}
}
func TestHostControlRemoveAndReaddAndCapture(t *testing.T) {
	button := NewButton("root/button", "B", "b")
	parent := &controlTestParent{Base: NewBase("root"), child: button}
	h := controlTestHost(parent)
	old := inspectControl(t, h, button.ID())
	parent.child = NewButton("root/other", "Other", "other")
	h.MarkLayout()
	h.Layout()
	parent.child = button
	h.MarkLayout()
	current := inspectControl(t, h, button.ID())
	if old.OpenEpoch == current.OpenEpoch {
		t.Fatal("remove/readd retained lifetime")
	}
	h.CapturePointer(parent.ID(), tea.MouseLeft)
	if _, err := h.Control(button.ID(), current.OpenEpoch, nil, ControlOperation{Verb: "activate"}); err == nil {
		t.Fatal("foreign pointer capture bypassed")
	}
}

func TestControlFormExactMultiSelectionAndToggle(t *testing.T) {
	m := NewFormModel()
	m.AddField(&FieldState{ID: "many", Type: FieldMultiSelect, Value: StringsValue([]string{"a"}), Options: []FormOption{{ID: "a"}, {ID: "b"}, {ID: "locked", Disabled: true}}})
	m.AddField(&FieldState{ID: "toggle", Type: FieldToggle, Value: BoolValue(false)})
	h := controlTestHost(NewForm("form", m, "", "", nil))
	operateControl(t, h, "form/many", ControlOperation{Verb: "set_value", Value: json.RawMessage(`["b"]`)})
	if got := m.Field("many").Value.Strings(); len(got) != 1 || got[0] != "b" || !m.AnyDirty() {
		t.Fatalf("whole selection not replaced: %v", got)
	}
	n := inspectControl(t, h, "form/many")
	for _, value := range []string{`["locked"]`, `["b","b"]`, `["missing"]`} {
		if _, err := h.Control("form/many", n.OpenEpoch, nil, ControlOperation{Verb: "set_value", Value: json.RawMessage(value)}); err == nil {
			t.Fatal("invalid selection accepted", value)
		}
	}
	res := operateControl(t, h, "form/toggle", ControlOperation{Verb: "activate"})
	if !m.Field("toggle").Value.Bool() || res.Action == nil || res.Action.ID != "form.changed" {
		t.Fatal("activation did not notify owning model", res)
	}
}

func TestControlKeyValueFormDirtyValidationAndSecret(t *testing.T) {
	m := NewFormModel()
	m.AddField(&FieldState{ID: "pairs", Type: FieldKeyValues, Value: StringValue(""), Validator: func(v FieldValue) []string {
		if strings.Contains(v.String(), "bad") {
			return []string{"bad key"}
		}
		return nil
	}})
	m.AddField(&FieldState{ID: "secret", Type: FieldPassword, Value: StringValue("hidden")})
	h := controlTestHost(NewForm("form", m, "", "", nil))
	res := operateControl(t, h, "form/pairs", ControlOperation{Verb: "append", Value: json.RawMessage(`{"Key":"bad","Value":"draft"}`)})
	if !m.AnyDirty() || !m.AnyErrors() || res.Action.ID != "form.changed" {
		t.Fatal("grid bypassed form model", res)
	}
	operateControl(t, h, "form/pairs", ControlOperation{Verb: "set", Offset: 0, Value: json.RawMessage(`{"Key":"safe","Value":"value"}`)})
	if m.AnyErrors() || !strings.Contains(m.Field("pairs").Value.String(), "safe") {
		t.Fatal("grid validation not refreshed")
	}
	operateControl(t, h, "form/pairs", ControlOperation{Verb: "remove", Offset: 0})
	if m.Field("pairs").Value.String() != "" || m.AnyDirty() {
		t.Fatal("removal failed to restore clean initial value")
	}
	n := inspectControl(t, h, "form/secret")
	if len(n.Value) != 0 || len(n.Actions) != 0 {
		t.Fatal("secret inspection exposed control", n)
	}
	if _, err := h.Control("form/secret", n.OpenEpoch, &n.Revision, ControlOperation{Verb: "set_value", Value: json.RawMessage(`"changed"`)}); err == nil {
		t.Fatal("secret mutation allowed")
	}
}
