package ui

import (
	"crude/tui/theme"
	"crude/types/control"
	"testing"
)

func TestControlFormRejectsUnobservedABA(t *testing.T) {
	m := NewFormModel()
	m.AddField(TextFieldState("value", "Value", "original", ""))
	th := theme.DefaultDark()
	f := NewForm("form", m, "", "", NewTokenSet(th))
	h := NewHost(th, f)
	h.Resize(80, 20)
	page, err := h.InspectControls(control.Target{}, control.PageRequest{Limit: 128})
	if err != nil {
		t.Fatal(err)
	}
	var node control.Node
	for _, n := range page.Nodes {
		if n.ID == "form/value" {
			node = n
		}
	}
	if node.ID == "" {
		t.Fatal("missing field")
	}
	m.SetValue("value", StringValue("changed"))
	m.SetValue("value", StringValue("original"))
	if _, err := h.Control("form/value", node.OpenEpoch, &node.Revision, ControlOperation{Verb: "set_value", Value: []byte(`"unexpected"`)}); err == nil {
		t.Fatal("unobserved value ABA accepted")
	}
	if m.Field("value").Value.String() != "original" {
		t.Fatal("stale operation mutated source")
	}
}
