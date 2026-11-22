package settingspages

import (
	"encoding/json"

	"crude/tui/ui"
	"crude/types/setting"
)

const fieldAnalysisTrust = "analysis.trust"

func (m *Model) analysisTrustField(settings setting.WorkspaceAnalysis) *ui.FieldState {
	if m.Scope() != setting.ScopeWorkspace {
		return &ui.FieldState{
			ID: fieldAnalysisTrust, Label: "Trust", Type: ui.FieldText, ReadOnly: true,
			Value:       ui.StringValue("Workspace only"),
			Description: "Choose Trust in Workspace settings; application settings cannot grant it.",
		}
	}
	field := ui.SegmentedFieldState(fieldAnalysisTrust, "Trust", []ui.FormOption{
		{ID: string(setting.AnalysisUndecided), Label: "Ask when the folder opens"},
		{ID: string(setting.AnalysisRestricted), Label: "Restricted"},
		{ID: string(setting.AnalysisTrusted), Label: "Trusted"},
	}, string(setting.NormalizeWorkspaceAnalysis(settings).Trust), "Restricted mode allows editing but does not run project tools. Trusted folders can run project tools.")
	field.Validator = func(value ui.FieldValue) []string {
		if value.String() == string(setting.AnalysisUndecided) {
			return nil
		}
		raw, _ := json.Marshal(value.String())
		if err := setting.ValidateAnalysisEdits(setting.ScopeWorkspace, []setting.Edit{
			{Path: []string{"analysis", "trust"}, After: raw},
		}); err != nil {
			return []string{err.Error()}
		}
		return nil
	}
	return field
}

func (m *Model) collectAnalysis() setting.WorkspaceAnalysis {
	if m.Scope() != setting.ScopeWorkspace {
		return setting.DefaultWorkspaceAnalysis()
	}
	return setting.WorkspaceAnalysis{Trust: setting.WorkspaceAnalysisTrust(m.value(catLSP, fieldAnalysisTrust))}
}

func (m *Model) syncAnalysisChoice() {
	if m.Scope() == setting.ScopeWorkspace && m.value(catLSP, fieldAnalysisTrust) == string(setting.AnalysisUndecided) {
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
	settings := setting.DefaultWorkspaceAnalysis()
	if m.Scope() == setting.ScopeWorkspace {
		// The core already resolved native-root identity and runtime policy in
		// the effective snapshot. Raw stored choices are not another authority.
		settings = setting.NormalizeWorkspaceAnalysis(m.layerBase.Analysis)
	}
	*field = *m.analysisTrustField(settings)
	m.collectedBase.Analysis = settings
	var baseline setting.App
	if json.Unmarshal(m.preferencesBase, &baseline) == nil {
		baseline.Analysis = settings
		m.preferencesBase, _ = json.Marshal(baseline)
	}
}
