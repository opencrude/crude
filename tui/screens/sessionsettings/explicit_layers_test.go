package sessionsettings

import (
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"testing"
)

func TestUntouchedSessionFormDoesNotCreateOverrides(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{Enabled: true, Categories: map[string]types.CategorySettings{"default": {Model: "main"}, "crafter": {Model: "parent-crafter", ThinkingEffort: "high"}}}}
	s := New(theme.DefaultDark(), base, base, nil)
	if got := s.ToSettings(); got.SessionMultiAgent != nil || len(got.SessionEdits) != 0 {
		t.Fatal("reading form pins inherited fields")
	}
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.role.open", Payload: "crafter"})
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.role.apply"})
	if got := s.ToSettings(); len(got.SessionOverrides) != 0 || len(got.SessionEdits) != 0 {
		t.Fatalf("untouched role Apply pins fields: %+v", got.SessionOverrides)
	}
}

func TestSessionResetDiscardsDraftWithoutTreatingItAsStored(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{Enabled: true, Categories: map[string]types.CategorySettings{"default": {Model: "parent"}}}}
	s := New(theme.DefaultDark(), base, base, nil)
	s.formModel.SetValue(fieldCodeIntelligence, ui.StringValue("on"))
	if len(s.ToSettings().SessionEdits) == 0 {
		t.Fatal("fixture did not stage change")
	}
	result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.reset"})
	if result.Cmd == nil {
		t.Fatal("Reset did not address the session owner")
	}
	if msg, ok := result.Cmd().(ResetMsg); !ok || msg.Target != s {
		t.Fatal("Reset replayed the draft instead of addressing its owner")
	}
}
