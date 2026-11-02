package settings

import (
	"encoding/json"

	"crude/tui/ui"
	"crude/types"
)

const fieldAnalysisTrust = "analysis.trust"

func (m *Model) analysisTrustField(settings types.WorkspaceAnalysisSettings) *ui.FieldState {
	if m.Scope() != types.SettingsScopeWorkspace {
		return &ui.FieldState{
			ID: fieldAnalysisTrust, Label: "Trust", Type: ui.FieldText, ReadOnly: true,
			Value:       ui.StringValue("Workspace only"),
			Description: "Choose Trust in Workspace settings; application settings cannot grant it.",
		}
	}
	field := ui.SegmentedFieldState(fieldAnalysisTrust, "Trust", []ui.FormOption{
		{ID: string(types.AnalysisUndecided), Label: "Ask when the folder opens"},
		{ID: string(types.AnalysisRestricted), Label: "Restricted"},
		{ID: string(types.AnalysisTrusted), Label: "Trusted"},
	}, string(types.NormalizeWorkspaceAnalysis(settings).Trust), "Restricted mode allows editing but does not run project tools. Trusted folders can run project tools.")
	field.Validator = func(value ui.FieldValue) []string {
		if value.String() == string(types.AnalysisUndecided) {
			return nil
		}
		raw, _ := json.Marshal(value.String())
		if err := types.ValidateAnalysisSettingEdits(types.SettingsScopeWorkspace, []types.SettingEdit{
			{Path: []string{"analysis", "trust"}, After: raw},
		}); err != nil {
			return []string{err.Error()}
		}
		return nil
	}
	return field
}

func (m *Model) collectAnalysis() types.WorkspaceAnalysisSettings {
	if m.Scope() != types.SettingsScopeWorkspace {
		return types.DefaultWorkspaceAnalysisSettings()
	}
	return types.WorkspaceAnalysisSettings{Trust: types.WorkspaceAnalysisTrust(m.value(catLSP, fieldAnalysisTrust))}
}

func (m *Model) syncAnalysisChoice() {
	if m.Scope() == types.SettingsScopeWorkspace && m.value(catLSP, fieldAnalysisTrust) == string(types.AnalysisUndecided) {
		m.resetWorkspacePath("analysis")
	}
}

// Direct scope changes replace this workspace-only row, never carrying a grant
// into application writes. The surface asks to discard dirty preferences before
// switching scopes; its shell owner then opens a fresh scoped model.
func (m *Model) syncAnalysisScope() {
	field := m.fieldAt(catLSP, fieldAnalysisTrust)
	if field == nil {
		return
	}
	settings := types.DefaultWorkspaceAnalysisSettings()
	if m.Scope() == types.SettingsScopeWorkspace {
		// The core already resolved native-root identity and runtime policy in
		// the effective snapshot. Raw stored choices are not another authority.
		settings = types.NormalizeWorkspaceAnalysis(m.layerBase.Analysis)
	}
	*field = *m.analysisTrustField(settings)
	m.collectedBase.Analysis = settings
	var baseline types.AppSettings
	if json.Unmarshal(m.preferencesBase, &baseline) == nil {
		baseline.Analysis = settings
		m.preferencesBase, _ = json.Marshal(baseline)
	}
}
