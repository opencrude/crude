package settings

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
)

// These tests construct the real settings model, unlike the independent
// prediction-only body tests. A missing category/collector/path hook must fail.
func TestPredictionProductNativeEntryAndAuthority(t *testing.T) {
	for _, size := range []ui.Size{{W: 160, H: 40}, {W: 80, H: 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := NewScoped(theme.DefaultDark(), types.AppSettings{}, nil,
				types.SettingsScopeWorkspace, types.AppSettings{}, types.WorkspaceOverrides{})
			m.SelectCategory(catLSP)
			form := m.categories[catLSP].Form
			for _, id := range []string{"prediction.mode", "prediction.hourly_token_limit"} {
				if form.Field(id) == nil {
					t.Fatal("Code Intelligence omitted the native prediction preference", id)
				}
			}
			s := NewSurface(theme.DefaultDark(), m)
			host := ui.NewHost(theme.DefaultDark(), s)
			s.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
			host.Resize(size.W, size.H)
			host.Layout()
			s.FocusForm()
			if !s.form.FocusField("prediction.mode") {
				t.Fatal("native Code Intelligence cannot focus the prediction row")
			}
			host.MarkLayout()
			host.Layout()
			host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
			if got := m.AppSettings().Prediction; got.Mode != types.PredictionAsk || got.HourlyTokenLimit != types.DefaultPredictionHourlyTokens {
				t.Fatal("real form key did not collect the offered hourly limit", got)
			}
			// A deliberately zero limit must still refuse Save through the real surface.
			m.SetValueForTest(catLSP, "prediction.hourly_token_limit", "0")
			if result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"}); result.Cmd != nil ||
				m.Saving() || m.SaveError() == "" {
				t.Fatal("native Save admitted an enabled prediction with no explicit cap")
			}
			m.SetValueForTest(catLSP, "prediction.hourly_token_limit", "4200")
			form.ValidateAll()
			if form.AnyErrors() {
				t.Fatal("correcting the native cap left a validation error")
			}
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Layout()
			host.Paint(buf)
			if !strings.Contains(bufferPlain(buf), "Errand suggestions") {
				t.Fatal("native scrolled prediction row was not painted", bufferPlain(buf))
			}
			for _, id := range []string{"prediction.mode", "prediction.hourly_token_limit"} {
				component := ui.NewID(WorkspaceSettingsFormID, id)
				if _, path := s.controlField(component); path != id {
					t.Fatal("prediction row has no exact canonical control path", id, path)
				}
				if effect, human, err := s.ControlMetadata(component, "form.changed"); err != nil ||
					effect != control.Authority || !human || s.ControlValueAllowed(component) {
					t.Fatal("generic controls acquired prediction authority", id, effect, human, err)
				}
			}
			foundMode, foundCap := false, false
			for _, edit := range m.Edits() {
				switch strings.Join(edit.Path, ".") {
				case "prediction.mode":
					foundMode = string(edit.After) == `"ask"`
				case "prediction.hourly_token_limit":
					foundCap = string(edit.After) == "4200"
				}
			}
			if !foundMode || !foundCap {
				t.Fatal("actual preference Save payload lost the explicit prediction choices", m.Edits())
			}
			s.openCategory(catEditor)
			host.MarkLayout()
			host.Layout()
			if effect, human, err := s.ControlMetadata(WorkspaceSettingsSaveID, "settings.save"); err != nil ||
				effect != control.Authority || !human {
				t.Fatal("off-page prediction draft lost human-only Save", effect, human, err)
			}
		})
	}
}

func TestPredictionProductApplicationAndScopeCannotCarryConsent(t *testing.T) {
	forged := types.AppSettings{Prediction: types.WorkspacePredictionSettings{
		Mode: types.PredictionType, HourlyTokenLimit: 4200,
	}}
	m := NewScoped(theme.DefaultDark(), forged, nil, types.SettingsScopeApplication, forged, types.WorkspaceOverrides{})
	for _, scope := range []types.SettingsScope{types.SettingsScopeApplication, types.SettingsScopeWorkspace, types.SettingsScopeApplication} {
		m.SetScope(scope)
		if got := m.AppSettings().Prediction; got != types.DefaultWorkspacePredictionSettings() {
			t.Fatal("application snapshot or direct scope transition granted source egress", scope, got)
		}
		for _, edit := range m.Edits() {
			if edit.Path[0] == "prediction" {
				t.Fatal("scope transition created a prediction authority edit", edit)
			}
		}
	}
	m.SetValueForTest(catEditor, "tab-size", "7")
	if m.AppSettings().Editor.TabSize != 7 {
		t.Fatal("workspace-only prediction controls blocked ordinary preferences")
	}
}

func TestPredictionNativeCorrectionRevalidatesRetainedSiblingFields(t *testing.T) {
	m := NewScoped(theme.DefaultDark(), types.AppSettings{}, nil,
		types.SettingsScopeWorkspace, types.AppSettings{}, types.WorkspaceOverrides{})
	form := m.categories[catLSP].Form
	form.SetValue(fieldPredictionLimit, ui.StringValue("0"))
	form.SetValue(fieldPredictionMode, ui.StringValue("ask"))
	if !form.AnyErrors() {
		t.Fatal("native incomplete enable did not retain a validation error")
	}
	form.SetValue(fieldPredictionLimit, ui.StringValue("4200"))
	if form.AnyErrors() {
		t.Fatal("native limit correction left obsolete sibling validation errors")
	}
}

func TestPredictionProductResetOwnsConsentButPreservesNeighbors(t *testing.T) {
	var app types.AppSettings
	types.ApplyAppDefaults(&app)
	stored := types.WorkspaceOverrides{
		Prediction: json.RawMessage(`{"mode":"ask","hourly_token_limit":4200}`),
		LSP:        json.RawMessage(`{"enabled":false,"languages":{"go":{"enabled":false}}}`),
	}
	m := NewScoped(theme.DefaultDark(), types.MergeAppWorkspace(app, stored), nil,
		types.SettingsScopeWorkspace, app, stored)
	m.SelectCategory(catLSP)
	m.SetValueForTest(catEditor, "tab-size", "7")
	edits, ok := m.PageResetEdits()
	if !ok {
		t.Fatal("Code Intelligence reset unavailable")
	}
	nextStored := applyResetToOverrides(t, stored, edits)
	if len(nextStored.Prediction) != 0 || string(nextStored.LSP) != `{"languages":{"go":{"enabled":false}}}` {
		t.Fatal("reset retained prediction consent or consumed the server inventory", nextStored)
	}
	next := m.SectionResetModel(app, types.MergeAppWorkspace(app, nextStored), nextStored, catLSP)
	if next.AppSettings().Prediction != types.DefaultWorkspacePredictionSettings() || next.AppSettings().Editor.TabSize != 7 {
		t.Fatal("reset revived consent or consumed an unrelated private draft")
	}
	for _, edit := range next.Edits() {
		if edit.Path[0] == "prediction" || edit.Path[0] == "lsp" {
			t.Fatal("rebased reset replayed the old Code Intelligence draft", edit)
		}
	}
	appModel := NewScoped(theme.DefaultDark(), app, nil, types.SettingsScopeApplication, app, stored)
	if !appModel.workspaceOverridesPage(catLSP) {
		t.Fatal("prediction workspace choices are absent from the Code Intelligence marker")
	}
	for _, term := range []string{"Errand suggestions", "prediction.mode", "Hourly limit"} {
		m.SetSearchQuery(term)
		found := false
		for _, index := range m.visibleCategoryIndices() {
			found = found || index == catLSP
		}
		if !found {
			t.Fatal("prediction preference was undiscoverable in settings search", term)
		}
	}
}
