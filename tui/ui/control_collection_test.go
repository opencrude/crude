package ui_test

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/control"
)

// This wrapper lives outside ui: the inventory contract must survive ordinary
// embedding without a screen-specific adapter or another item/selection model.
type wrappedControlList struct {
	*ui.List
	operations int
}

func (l *wrappedControlList) HandleControl(ctx ui.EventContext, op ui.ControlOperation) (ui.EventResult, error) {
	l.operations++
	return l.List.HandleControl(ctx, op)
}

func (l *wrappedControlList) ControlActions() []control.Descriptor {
	out := l.List.ControlActions()
	for i := range out {
		if out[i].ID == "native.row.activate" {
			out[i].ID = "activate"
			out[i].Effect = control.Execute // structural metadata is not authority
		}
	}
	extra := out[len(out)-1]
	extra.ID, extra.Effect = "wrapper.inspect", control.Write
	return append(out, extra)
}

type controlCollectionOwner struct {
	ui.Base
	child       ui.Component
	activations int
	itemID      string
}

func (o *controlCollectionOwner) Children() []ui.Component { return []ui.Component{o.child} }
func (o *controlCollectionOwner) Layout(ctx ui.LayoutContext, r ui.Rect) {
	o.SetBounds(r)
	o.child.Layout(ctx, ui.Rect{X: 2, Y: 1, W: max(0, r.W-4), H: 1})
}
func (o *controlCollectionOwner) HandleAction(_ ui.EventContext, a ui.Action) ui.EventResult {
	if a.ID != "native.row.activate" {
		return ui.EventResult{}
	}
	o.activations++
	o.itemID = a.Payload.(ui.ListItem).ID
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: "owner.accept", Payload: o.itemID}}
}

func newWrappedControlHost(items []ui.ListItem) (*ui.Host, *wrappedControlList, *controlCollectionOwner) {
	list := &wrappedControlList{List: ui.NewList("collection/list", ui.NewTokenSet(theme.DefaultDark()))}
	list.SetActivateAction("native.row.activate")
	list.SetItems(items)
	owner := &controlCollectionOwner{Base: ui.NewBase("collection"), child: list}
	h := ui.NewHost(theme.DefaultDark(), owner)
	h.Resize(40, 3)
	h.Layout()
	h.Focus(list.ID())
	return h, list, owner
}

func collectionNode(t *testing.T, h *ui.Host, id ui.ComponentID) control.Node {
	t.Helper()
	n, err := h.InspectControl(id, control.Target{WorkspaceID: "original-workspace"})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func requireCollectionFault(t *testing.T, err error, code control.FaultCode) {
	t.Helper()
	var fault *control.Fault
	if !errors.As(err, &fault) || fault.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestWrappedListControlInventoryAndNativeSelection(t *testing.T) {
	h, list, owner := newWrappedControlHost([]ui.ListItem{
		{ID: "original/first", Label: "Same label"},
		{ID: "original/second", Label: "Same label"},
	})
	target := control.Target{WorkspaceID: "original-workspace"}
	page, err := h.InspectControls(target, control.PageRequest{Limit: 3})
	if err != nil || len(page.Nodes) != 3 || page.NextCursor == "" {
		t.Fatalf("first bounded page: %+v, %v", page, err)
	}
	parent, first := page.Nodes[1], page.Nodes[2]
	if parent.ID != string(list.ID()) || parent.Role != control.List || parent.Bounds == nil ||
		*parent.Bounds != (control.Bounds{X: 2, Y: 1, Width: 36, Height: 1}) {
		t.Fatalf("native wrapper bounds: %+v", parent)
	}
	if first.ID != "collection/list/item/original/first" || first.ParentID != parent.ID ||
		first.Name != "Same label" || first.Role != control.Item || !first.State.Selected ||
		!first.State.Enabled || first.Bounds != nil || len(first.Actions) != 0 ||
		first.OpenEpoch != parent.OpenEpoch || first.Revision != parent.Revision || first.Target != target {
		t.Fatalf("first exact item: %+v", first)
	}
	tail, err := h.InspectControls(target, control.PageRequest{Limit: 3, Cursor: page.NextCursor})
	if err != nil || len(tail.Nodes) != 1 || tail.NextCursor != "" ||
		tail.Nodes[0].ID != "collection/list/item/original/second" ||
		tail.Nodes[0].Name != first.Name || tail.Nodes[0].State.Selected {
		t.Fatalf("second exact item: %+v, %v", tail, err)
	}

	h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	selected, ok := list.Selected()
	if !ok || selected.ID != "original/second" || list.SelectedIndex() != 1 || list.Scroll().Offset() != 1 {
		t.Fatalf("native Down state: %+v, offset %d", selected, list.Scroll().Offset())
	}
	after := collectionNode(t, h, list.ID())
	if after.Revision == parent.Revision || after.OpenEpoch != parent.OpenEpoch {
		t.Fatalf("equal labels hid native selection revision: before %+v, after %+v", parent, after)
	}
	_, err = h.InspectControls(target, control.PageRequest{Limit: 3, Cursor: page.NextCursor})
	requireCollectionFault(t, err, control.Stale)
	_, err = h.Control(list.ID(), parent.OpenEpoch, &parent.Revision, ui.ControlOperation{Verb: "activate", ItemID: "original/first"})
	requireCollectionFault(t, err, control.Stale)
	if owner.activations != 0 || list.SelectedIndex() != 1 {
		t.Fatal("stale operation activated or changed selection")
	}
	current, err := h.InspectControls(target, control.PageRequest{Limit: 4})
	if err != nil || len(current.Nodes) != 4 || current.Nodes[2].State.Selected || !current.Nodes[3].State.Selected {
		t.Fatalf("inventory diverged from native Down: %+v, %v", current, err)
	}

	// Inspecting geometry/focus/paint does not create another source generation.
	h.MarkPaint()
	h.SetFrame(123)
	buf := surface.NewCellBuffer(40, 3)
	h.Paint(buf)
	var line strings.Builder
	for x := range 40 {
		line.WriteRune(buf.Get(x, 1).Char)
	}
	if !strings.Contains(line.String(), "Same label") {
		t.Fatalf("selected native row was not painted: %q", line.String())
	}
	if painted := collectionNode(t, h, list.ID()); painted.Revision != after.Revision {
		t.Fatal("paint changed the source revision")
	}
}

func TestWrappedListControlExactActionsAndLocalSelection(t *testing.T) {
	h, list, owner := newWrappedControlHost([]ui.ListItem{
		{ID: "original/first", Label: "Same label", Payload: "first payload"},
		{ID: "original/second", Label: "Same label", Payload: "second payload"},
	})
	for _, tc := range []struct {
		verb, action string
		effect       control.Effect
	}{
		// A wrapper can observe even native selection/scroll operations.
		// Generic descriptors retain exact IDs but never infer their effects;
		// only the contextual owner can authorize its observed actions.
		{"select", "select", ""},
		{"activate", "activate", ""},
		{"wrapper.inspect", "wrapper.inspect", ""},
		{"scroll", "scroll", ""},
		{"set_offset", "set_offset", ""},
	} {
		d, err := h.DescribeControl(list.ID(), ui.ControlOperation{Verb: tc.verb, ItemID: "original/second"})
		if err != nil || d.ID != tc.action || d.Effect != tc.effect {
			t.Fatalf("%s description: %+v, %v", tc.verb, d, err)
		}
	}
	n := collectionNode(t, h, list.ID())
	res, err := h.Control(list.ID(), n.OpenEpoch, &n.Revision, ui.ControlOperation{Verb: "select", ItemID: "original/second"})
	if err != nil || res.Action != nil || owner.activations != 0 || list.SelectedIndex() != 1 || list.Scroll().Offset() != 1 {
		t.Fatalf("selection was not native/local: %+v, %v", res, err)
	}
	n = collectionNode(t, h, list.ID())
	res, err = h.Control(list.ID(), n.OpenEpoch, &n.Revision, ui.ControlOperation{Verb: "set_offset", Offset: 0})
	if err != nil || res.Action != nil || list.Scroll().Offset() != 0 || list.SelectedIndex() != 1 {
		t.Fatalf("scroll mutated selection or lost native offset: %+v, %v", res, err)
	}
	if next := collectionNode(t, h, list.ID()); next.Revision == n.Revision {
		t.Fatal("native offset did not fence inspection")
	}
	n = collectionNode(t, h, list.ID())
	res, err = h.Control(list.ID(), n.OpenEpoch, &n.Revision, ui.ControlOperation{Verb: "activate", ItemID: "original/first"})
	if err != nil || res.Action == nil || res.Action.ID != "owner.accept" ||
		res.Action.Payload != "original/first" || owner.itemID != "original/first" ||
		owner.activations != 1 || list.SelectedIndex() != 0 {
		t.Fatalf("exact activation was lost/duplicated: %+v, %v, owner %+v", res, err, owner)
	}
	human := h.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if human.Action == nil || human.Action.ID != res.Action.ID || human.Action.Payload != res.Action.Payload || owner.activations != 2 {
		t.Fatalf("human/semantic activation paths diverged or duplicated: %+v", human)
	}
}

func TestWrappedListControlRefusesInvalidItems(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		rows     []ui.ListItem
		code     control.FaultCode
	}{
		{"missing", "", []ui.ListItem{{ID: "", Label: "Empty ID"}}, control.Invalid},
		{"blank", " ", []ui.ListItem{{ID: " ", Label: "Blank ID"}}, control.Invalid},
		{"unknown", "Same label", []ui.ListItem{{ID: "original", Label: "Same label"}}, control.Unavailable},
		{"disabled", "locked", []ui.ListItem{{ID: "original"}, {ID: "locked", Disabled: true}}, control.Unavailable},
		{"header", "header", []ui.ListItem{{ID: "original"}, {ID: "header", Header: true}}, control.Unavailable},
		{"duplicate", "duplicate", []ui.ListItem{{ID: "original"}, {ID: "duplicate"}, {ID: "duplicate"}}, control.Invalid},
		{"disabled duplicate", "duplicate", []ui.ListItem{{ID: "original"}, {ID: "duplicate", Disabled: true}, {ID: "duplicate"}}, control.Invalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, list, owner := newWrappedControlHost(tc.rows)
			n := collectionNode(t, h, list.ID())
			index, offset := list.SelectedIndex(), list.Scroll().Offset()
			for _, verb := range []string{"select", "activate"} {
				op := ui.ControlOperation{Verb: verb, ItemID: tc.id}
				_, err := h.DescribeControl(list.ID(), op)
				requireCollectionFault(t, err, tc.code)
				res, err := h.Control(list.ID(), n.OpenEpoch, &n.Revision, op)
				requireCollectionFault(t, err, tc.code)
				if res.Action != nil || list.operations != 0 || owner.activations != 0 || list.SelectedIndex() != index || list.Scroll().Offset() != offset {
					t.Fatal("refused item entered wrapper, changed native state or activated")
				}
			}
		})
	}
}

func TestWrappedListControlPageBoundsAndReplacement(t *testing.T) {
	h, list, owner := newWrappedControlHost([]ui.ListItem{{ID: "first"}, {ID: "second"}})
	for _, limit := range []int{0, control.MaxPageLimit + 1} {
		_, err := h.InspectControls(control.Target{}, control.PageRequest{Limit: limit})
		requireCollectionFault(t, err, control.Invalid)
	}
	page, err := h.InspectControls(control.Target{}, control.PageRequest{Limit: 1})
	if err != nil || len(page.Nodes) != 1 || page.NextCursor == "" {
		t.Fatalf("single record page: %+v, %v", page, err)
	}
	cursor := page.NextCursor[:strings.LastIndex(page.NextCursor, ":")+1] + "999"
	_, err = h.InspectControls(control.Target{}, control.PageRequest{Limit: 1, Cursor: cursor})
	requireCollectionFault(t, err, control.Invalid)
	n := collectionNode(t, h, list.ID())
	list.Items()[1].ID = "replacement-original"
	_, err = h.Control(list.ID(), n.OpenEpoch, &n.Revision, ui.ControlOperation{Verb: "activate", ItemID: "first"})
	requireCollectionFault(t, err, control.Stale)
	_, err = h.InspectControls(control.Target{}, control.PageRequest{Limit: 1, Cursor: page.NextCursor})
	requireCollectionFault(t, err, control.Stale)
	owner.child = &wrappedControlList{List: ui.NewList(list.ID(), nil)}
	h.MarkLayout()
	replacement := collectionNode(t, h, list.ID())
	if replacement.OpenEpoch == n.OpenEpoch {
		t.Fatal("replacement wrapper retained the original native lifetime")
	}
	_, err = h.Control(list.ID(), n.OpenEpoch, nil, ui.ControlOperation{Verb: "activate", ItemID: "first"})
	requireCollectionFault(t, err, control.Stale)
	if owner.activations != 0 {
		t.Fatal("stale source or replacement activated")
	}
}

type embeddedControlList struct{ *ui.List }

func TestEmbeddedListControlKeepsNativeActivationIdentity(t *testing.T) {
	native := ui.NewList("embedded", nil)
	native.SetActivateAction("native.original.activate")
	native.SetItems([]ui.ListItem{{ID: "original"}})
	for _, component := range []ui.Component{native, &embeddedControlList{native}} {
		h := ui.NewHost(theme.DefaultDark(), component)
		h.Resize(40, 3)
		d, err := h.DescribeControl(component.ID(), ui.ControlOperation{Verb: "activate", ItemID: "original"})
		if err != nil || d.ID != "native.original.activate" || d.Effect != "" {
			t.Fatalf("native activation identity changed for %T: %+v, %v", component, d, err)
		}
		d, err = h.DescribeControl(component.ID(), ui.ControlOperation{Verb: "select", ItemID: "original"})
		wantID, wantEffect := "select", control.Effect("")
		if component == native {
			wantID, wantEffect = "ui.control.select", control.Write
		}
		if err != nil || d.ID != wantID || d.Effect != wantEffect {
			t.Fatalf("selection authority changed for %T: %+v, %v", component, d, err)
		}
	}
}
