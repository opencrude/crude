package sessionsettings

import (
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
	"encoding/json"
	"testing"
)

func TestControlRoleThinkingAndAuthority(t *testing.T) {
	settings := types.Settings{Model: "initial", ThinkingEffort: "none", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {Model: "initial", ThinkingEffort: "none"}}}}
	s := New(theme.DefaultDark(), settings, settings, nil)
	s.SaveSnapshot()
	h := ui.NewHost(theme.DefaultDark(), s)
	h.Resize(90, 20)
	if _, err := invokeControl(t, h, ui.NewID(FormID, "default"), ui.ControlOperation{Verb: "activate"}); err != nil {
		t.Fatal(err)
	}
	if s.page != rolePage {
		t.Fatal("role navigation did not reach owner")
	}
	if _, err := invokeControl(t, h, ui.NewID(FormID, fieldThinking), ui.ControlOperation{Verb: "select", ItemID: "high"}); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeControl(t, h, ui.NewID(ActionsID, "apply"), ui.ControlOperation{Verb: "activate"}); err != nil {
		t.Fatal(err)
	}
	if s.ToSettings().ThinkingEffort != "high" {
		t.Fatal("thinking selection was not adopted")
	}
	effect, human, err := s.ControlMetadata(ui.NewID(ActionsID, "save"), "session-settings.save")
	if err != nil || human || effect != control.Write {
		t.Fatalf("ordinary save misclassified %s %v %v", effect, human, err)
	}
	if _, err := invokeControl(t, h, ui.NewID(FormID, fieldMultiAgent), ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"on"`)}); err != nil {
		t.Fatal(err)
	}
	effect, human, err = s.ControlMetadata(ui.NewID(ActionsID, "save"), "session-settings.save")
	if err != nil || !human || effect != control.Authority {
		t.Fatalf("multi agent save laundered %s %v %v", effect, human, err)
	}
}

func TestControlPickerFilterAndSelectionReachOwner(t *testing.T) {
	settings := types.Settings{Model: "initial", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {Model: "initial"}}}}
	s := New(theme.DefaultDark(), settings, settings, nil)
	s.openRole("default")
	s.openModelPicker()
	s.models[sessionModelKey(s.currentProviderID(), s.currentAccountID())] = []types.ModelEntry{{ID: "chosen", Name: "Chosen"}, {ID: "other", Name: "Other"}}
	s.syncPickerList()
	h := ui.NewHost(theme.DefaultDark(), s)
	h.Resize(90, 20)
	if _, err := invokeControl(t, h, FilterID, ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"Chosen"`)}); err != nil {
		t.Fatal(err)
	}
	if s.modelQuery != "Chosen" {
		t.Fatal("filter bypassed query owner")
	}
	if _, err := invokeControl(t, h, ListID, ui.ControlOperation{Verb: "activate", ItemID: "chosen"}); err != nil {
		t.Fatal(err)
	}
	if s.page != rolePage || s.formModel.Field(fieldModel).Value.String() != "chosen" {
		t.Fatal("picker did not adopt exact selected model")
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
