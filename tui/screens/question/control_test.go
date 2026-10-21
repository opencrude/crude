package question

import (
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/control"
	"encoding/json"
	"testing"
)

func TestControlQuestionWriteInAndOptionUseAnswerOwner(t *testing.T) {
	s := New(theme.DefaultDark())
	s.Reset(`[{"question":"Choose","options":["Yes","No"]}]`)
	h := ui.NewHost(theme.DefaultDark(), s)
	h.Resize(90, 20)
	if _, err := invokeControl(t, h, OptionsID, ui.ControlOperation{Verb: "activate", ItemID: "option:0"}); err != nil {
		t.Fatal(err)
	}
	if s.Result().Answers["1"] != "Yes" {
		t.Fatal("option activation did not update canonical answer")
	}
	if !s.Reviewing() {
		t.Fatal("single option bypassed mandatory review")
	}
	if _, err := invokeControl(t, h, OptionsID, ui.ControlOperation{Verb: "activate", ItemID: "review/0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeControl(t, h, InputID, ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"Custom answer"`)}); err != nil {
		t.Fatal(err)
	}
	if s.Result().Answers["1"] != "Custom answer" {
		t.Fatalf("write-in bypassed answer owner: %+v", s.Result())
	}
	res, err := invokeControl(t, h, ui.NewID(ActionsID, "next"), ui.ControlOperation{Verb: "activate"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Cmd != nil || !s.Reviewing() {
		t.Fatal("edited answer bypassed review")
	}
	res, err = invokeControl(t, h, ui.NewID(ActionsID, "submit"), ui.ControlOperation{Verb: "activate"})
	if err != nil || res.Cmd == nil {
		t.Fatal("explicit Submit did not emit owner result", err)
	}
	msg := res.Cmd().(ResultMsg)
	if msg.Result.Answers["1"] != "Custom answer" || msg.Target != s {
		t.Fatal("wrong submitted answer")
	}
}

func invokeControl(t *testing.T, h *ui.Host, id ui.ComponentID, op ui.ControlOperation) (ui.EventResult, error) {
	t.Helper()
	page, err := h.InspectControls(control.Target{WorkspaceID: "fixture"}, control.PageRequest{Limit: 128})
	if err != nil {
		return ui.EventResult{}, err
	}
	for _, n := range page.Nodes {
		if n.ID == string(id) {
			return h.Control(id, n.OpenEpoch, &n.Revision, op)
		}
	}
	t.Fatalf("missing control %s", id)
	return ui.EventResult{}, nil
}
