package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
)

func analysisModel(app types.AppSettings, stored types.WorkspaceOverrides) *Model {
	return NewScoped(theme.DefaultDark(), types.MergeAppWorkspace(app, stored), nil, types.SettingsScopeWorkspace, app, stored)
}

func TestWorkspaceAnalysisTrustNativeForm(t *testing.T) {
	for _, size := range []ui.Size{{W: 160, H: 38}, {W: 80, H: 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := analysisModel(types.AppSettings{}, types.WorkspaceOverrides{})
			m.SelectCategory(catLSP)
			s := NewSurface(theme.DefaultDark(), m)
			frame := ui.NewWindowFrameWidget(theme.DefaultDark(), "trust-settings", ui.FrameWindow, "Settings", s, nil, true, size.W, size.H)
			s.SetFocusHandler(frame.Focus)
			host := frame.Host()
			host.Resize(size.W, size.H)
			host.Layout()
			s.form.FocusFirst()
			s.FocusForm()
			host.Layout()
			if m.categories[catLSP].Form.Fields()[0].ID != "analysis.trust" || s.form.HelpField() == nil || s.form.HelpField().ID != "analysis.trust" {
				t.Fatal("Trust is not the first control")
			}
			field := m.fieldAt(catLSP, "analysis.trust")
			if field.Type != ui.FieldCycle || field.Style != ui.StyleSegmented || field.ReadOnly ||
				len(field.Options) != 3 || field.Options[0].Label != "Ask when the folder opens" || field.Options[1].Label != "Restricted" || field.Options[2].Label != "Trusted" {
				t.Fatalf("not the native three-choice Trust control: %+v", field)
			}
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buf)
			plain := bufferPlain(buf)
			ask := "Ask when the folder opens"
			if size.W == 80 {
				ask = "Ask when the …"
			}
			for _, text := range []string{"Trust", ask} {
				if !strings.Contains(plain, text) {
					t.Fatalf("missing %q:\n%s", text, plain)
				}
			}
			if size.W == 160 && (!strings.Contains(plain, "Trusted") ||
				!strings.Contains(plain, "Restricted mode allows editing")) {
				t.Fatalf("wide form omitted choices or help:\n%s", plain)
			}
			if m.AppSettings().Analysis.Trust != types.AnalysisUndecided || m.hasUnsavedPreferences() || len(m.Edits()) != 0 {
				t.Fatal("opening Trust changed its default or dirtied preferences")
			}
			if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyRight}); !result.Handled {
				t.Fatal("human keyboard choice was not handled")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
			host.MarkLayout()
			host.Layout()
			host.Paint(buf)
			wantChoice := "Trusted"
			if m.AppSettings().Analysis.Trust != types.AnalysisTrusted ||
				!strings.Contains(bufferPlain(buf), wantChoice) || !m.categoryDirty(catLSP) {
				t.Fatalf("choice not collected and painted:\n%s", bufferPlain(buf))
			}
			edits := m.Edits()
			if len(edits) != 1 || strings.Join(edits[0].Path, ".") != "analysis.trust" ||
				string(edits[0].After) != `"trusted"` {
				t.Fatalf("Trust wrote unrelated leaves: %+v", edits)
			}
			result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"})
			if result.Cmd == nil {
				t.Fatal("human Save omitted command")
			}
			msg, ok := result.Cmd().(SaveMsg)
			if !ok || msg.Scope != types.SettingsScopeWorkspace || len(msg.Edits) != 1 {
				t.Fatalf("bad scoped save: %+v", msg)
			}
			m.FinishSave(errors.New("fixture persistence failure"))
			if !m.categoryDirty(catLSP) || m.SaveError() == "" {
				t.Fatal("failed save lost the trust draft")
			}
		})
	}
}

func TestApplicationAnalysisTrustCannotGrant(t *testing.T) {
	app := types.AppSettings{Analysis: types.WorkspaceAnalysisSettings{Trust: types.AnalysisTrusted}}
	stored := types.WorkspaceOverrides{Analysis: json.RawMessage(`{"trust":"trusted"}`)}
	m := NewScoped(theme.DefaultDark(), app, nil, types.SettingsScopeApplication, app, stored)
	m.SelectCategory(catLSP)
	s := NewSurface(theme.DefaultDark(), m)
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(140, 32)
	host.Layout()
	buf := surface.NewCellBuffer(140, 32)
	host.Paint(buf)
	if !strings.Contains(bufferPlain(buf), "Workspace only") || strings.Contains(bufferPlain(buf), "Trusted") {
		t.Fatal("application page exposes a trust grant", bufferPlain(buf))
	}
	if field := m.fieldAt(catLSP, "analysis.trust"); !field.ReadOnly || len(field.Options) != 0 {
		t.Fatal("application Trust is editable")
	}
	m.SetValueForTest(catLSP, "analysis.trust", "trusted")
	m.SetValueForTest(catEditor, "tab-size", "8")
	for _, edit := range m.Edits() {
		if edit.Path[0] == "analysis" {
			t.Fatal("application writes carried a grant", edit)
		}
	}
	if m.AppSettings().Analysis.Trust != types.AnalysisUndecided || !m.workspaceOverridesPage(catLSP) || m.workspaceOverridesPage(catLanguageServers) {
		t.Fatal("application grant or workspace page marker is wrong")
	}
	reset, _ := m.PageResetEdits()
	for _, edit := range reset {
		if edit.Path[0] == "analysis" {
			t.Fatal("application reset writes analysis")
		}
	}
}

func TestAnalysisScopeAndSearch(t *testing.T) {
	stored := types.WorkspaceOverrides{Analysis: json.RawMessage(`{"trust":"trusted"}`)}
	m := analysisModel(types.AppSettings{}, stored)
	for _, scope := range []types.SettingsScope{types.SettingsScopeApplication, types.SettingsScopeWorkspace, types.SettingsScopeApplication, types.SettingsScopeWorkspace} {
		m.SetScope(scope)
		f := m.fieldAt(catLSP, "analysis.trust")
		if f.ReadOnly != (scope == types.SettingsScopeApplication) || m.hasUnsavedPreferences() || len(m.Edits()) != 0 {
			t.Fatalf("scope change created an editable app grant or dirty form: %s %+v", scope, m.Edits())
		}
		if scope == types.SettingsScopeWorkspace && m.AppSettings().Analysis.Trust != types.AnalysisTrusted {
			t.Fatal("workspace scope lost the workspace grant")
		}
	}
	for _, query := range []string{"Trust", "analysis.trust", "Ask when the folder opens", "Restricted", "Trusted"} {
		m.SetSearchQuery(query)
		if !slices.Contains(m.visibleCategoryIndices(), catLSP) || m.searchTarget() != catLSP {
			t.Fatalf("%q did not find Code Intelligence", query)
		}
	}
}

func TestAnalysisScopeDoesNotReauthorizeAStoredButBlockedGrant(t *testing.T) {
	stored := types.WorkspaceOverrides{
		Analysis: json.RawMessage(`{"trust":"trusted"}`), AnalysisRootIdentity: "old-directory",
	}
	// The core observed a different or unavailable directory and resolved the
	// current effective choice to Undecided. The form cannot undo that
	// adjudication by re-merging raw persisted choices during a scope round trip.
	m := NewScoped(theme.DefaultDark(), types.AppSettings{Analysis: types.DefaultWorkspaceAnalysisSettings()}, nil,
		types.SettingsScopeWorkspace, types.AppSettings{}, stored)
	m.SetScope(types.SettingsScopeApplication)
	m.SetScope(types.SettingsScopeWorkspace)
	if m.AppSettings().Analysis.Trust != types.AnalysisUndecided || len(m.Edits()) != 0 {
		t.Fatal("form scope re-merged a root-blocked stored grant as current execution trust")
	}
	// An application observation, even with a forged application value and a
	// remembered workspace overlay, cannot supply a verified workspace policy.
	m = NewScoped(theme.DefaultDark(), types.AppSettings{Analysis: types.WorkspaceAnalysisSettings{Trust: types.AnalysisTrusted}}, nil,
		types.SettingsScopeApplication, types.AppSettings{}, stored)
	m.SetScope(types.SettingsScopeWorkspace)
	if m.AppSettings().Analysis.Trust != types.AnalysisUndecided || len(m.Edits()) != 0 {
		t.Fatal("application-to-workspace scope switch synthesized execution authority")
	}
}

func TestAnalysisResetAndRebaseOwnership(t *testing.T) {
	// Even an invalid application-owned grant can never become the reset parent.
	app := types.AppSettings{Analysis: types.WorkspaceAnalysisSettings{Trust: types.AnalysisTrusted}}
	stored := types.WorkspaceOverrides{
		Analysis: json.RawMessage(`{"trust":"trusted"}`),
		LSP:      json.RawMessage(`{"enabled":false,"languages":{"go":{"enabled":false}}}`),
	}
	m := analysisModel(app, stored)
	m.SelectCategory(catLSP)
	m.SetValueForTest(catUI, "sidebar-width", "45")
	reset, ok := m.PageResetEdits()
	if !ok {
		t.Fatal("Code Intelligence reset unavailable")
	}
	nextStored := applyResetToOverrides(t, stored, reset)
	if len(nextStored.Analysis) != 0 || !strings.Contains(string(nextStored.LSP), `"languages"`) {
		t.Fatal("reset kept analysis or removed the server inventory", nextStored)
	}
	next := m.SectionResetModel(app, types.MergeAppWorkspace(app, nextStored), nextStored, catLSP)
	if next.AppSettings().Analysis.Trust != types.AnalysisUndecided || next.AppSettings().UI.SidebarWidth != 45 {
		t.Fatal("reset inherited application trust or consumed unrelated draft")
	}
	for _, edit := range next.Edits() {
		if edit.Path[0] == "analysis" || edit.Path[0] == "lsp" {
			t.Fatal("reset replayed the old page draft", edit)
		}
	}

	// An unrelated committed reset retains pending Trust and its Save authority.
	m = analysisModel(types.AppSettings{}, types.WorkspaceOverrides{})
	m.SetValueForTest(catLSP, "analysis.trust", "trusted")
	m.SelectCategory(catEditor)
	next = m.SectionResetModel(types.AppSettings{}, types.MergeAppWorkspace(types.AppSettings{}, types.WorkspaceOverrides{}), types.WorkspaceOverrides{}, catEditor)
	if next.AppSettings().Analysis.Trust != types.AnalysisTrusted || !next.hasUnsavedPreferences() {
		t.Fatal("unrelated reset consumed off-page Trust")
	}
	s := NewSurface(theme.DefaultDark(), next)
	if effect, human, err := s.ControlMetadata(WorkspaceSettingsSaveID, "settings.save"); err != nil || effect != control.Authority || !human {
		t.Fatal("rebased off-page Trust lost Save protection", effect, human, err)
	}
	saved := next.Overrides()
	clean := next.SavedModel(types.AppSettings{}, types.MergeAppWorkspace(types.AppSettings{}, saved), saved)
	if clean.hasUnsavedPreferences() || len(clean.Edits()) != 0 || clean.AppSettings().Analysis.Trust != types.AnalysisTrusted {
		t.Fatal("saved Trust was not rebased cleanly")
	}
}

func TestAnalysisTrustControlAdmissionIncludesOffPageDraft(t *testing.T) {
	m := analysisModel(types.AppSettings{}, types.WorkspaceOverrides{})
	m.SelectCategory(catLSP)
	s := NewSurface(theme.DefaultDark(), m)
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(140, 32)
	host.Layout()
	id := ui.NewID(WorkspaceSettingsFormID, "analysis.trust")
	if _, path := s.controlField(id); path != "analysis.trust" {
		t.Fatal("Trust does not have its canonical path", path)
	}
	for _, verb := range []string{"set_value", "select"} {
		desc, err := host.DescribeControl(id, ui.ControlOperation{Verb: verb, ItemID: "trusted", Value: json.RawMessage(`"trusted"`)})
		if err != nil {
			t.Fatal(err)
		}
		effect, human, err := s.ControlMetadata(id, desc.ID)
		if err != nil || !human || effect != control.Authority || s.ControlValueAllowed(id) {
			t.Fatal("generic Trust operation admitted", verb, effect, human, err)
		}
	}
	before := s.ControlEditGeneration()
	m.SetValueForTest(catLSP, "analysis.trust", "trusted")
	s.openCategory(catEditor)
	host.MarkLayout()
	host.Layout()
	if s.ControlEditGeneration() <= before {
		t.Fatal("off-page draft did not fence controls")
	}
	for _, action := range []string{"settings.save", "settings.reset", "settings.close", "settings.cancel"} {
		effect, human, err := s.ControlMetadata(WorkspaceSettingsSaveID, action)
		if err != nil || !human || effect != control.Authority {
			t.Fatal("off-page Trust was excluded from admission", action, effect, human, err)
		}
	}
	// Ordinary fields remain ordinary, but their Save cannot carry human Trust.
	effect, human, err := s.ControlMetadata(ui.NewID(WorkspaceSettingsFormID, "tab-size"), "form.changed")
	if err != nil || human || effect != control.Write {
		t.Fatal("ordinary field stopped being editable", effect, human, err)
	}
}

func TestAnalysisTrustDiscardScopeAndServerReset(t *testing.T) {
	m := analysisModel(types.AppSettings{}, types.WorkspaceOverrides{})
	m.Snapshot()
	m.SetValueForTest(catLSP, "analysis.trust", "trusted")
	s := NewSurface(theme.DefaultDark(), m)
	prompt := settingsPrompt(t, s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.scope", Payload: scopeTabApplication}))
	if prompt.Kind != PromptDiscardSettings || !s.PromptRelevant(prompt) {
		t.Fatal("scope switch did not protect the Trust draft")
	}
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.discard.stay"})
	if m.Scope() != types.SettingsScopeWorkspace || m.AppSettings().Analysis.Trust != types.AnalysisTrusted {
		t.Fatal("canceling scope switch consumed Trust")
	}
	m.SelectCategory(catLanguageServers)
	s.syncModel()
	s.lsp.begin("go")
	s.lsp.HandleAction(ui.EventContext{}, ui.Action{ID: "lsp.reset"})
	if m.AppSettings().Analysis.Trust != types.AnalysisTrusted {
		t.Fatal("server Restore default changed Trust")
	}
	s.lsp.HandleAction(ui.EventContext{}, ui.Action{ID: "lsp.cancel"})
	if m.AppSettings().Analysis.Trust != types.AnalysisTrusted {
		t.Fatal("canceling server edit changed Trust")
	}
	m.Restore()
	if m.AppSettings().Analysis.Trust != types.AnalysisUndecided || m.hasUnsavedPreferences() || len(m.Edits()) != 0 {
		t.Fatal("discarding settings retained a pending grant")
	}
}

func TestInvalidAnalysisTrustBlocksSave(t *testing.T) {
	m := analysisModel(types.AppSettings{}, types.WorkspaceOverrides{})
	m.SetValueForTest(catLSP, "analysis.trust", "unrestricted")
	s := NewSurface(theme.DefaultDark(), m)
	if result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"}); result.Cmd != nil || m.Saving() || m.SaveError() == "" {
		t.Fatal("invalid trust escaped the native form validator")
	}
}

func TestAnalysisChoiceRemovalAndLaterChoice(t *testing.T) {
	stored := types.WorkspaceOverrides{Analysis: json.RawMessage(`{"trust":"trusted"}`)}
	m := analysisModel(types.AppSettings{}, stored)
	m.SetValueForTest(catLSP, fieldAnalysisTrust, "")
	edits := m.Edits()
	if len(edits) != 1 || !slices.Equal(edits[0].Path, []string{"analysis"}) || edits[0].After != nil {
		t.Fatalf("Ask must remove the analysis object: %+v", edits)
	}
	cleared := applyResetToOverrides(t, stored, edits)
	if len(cleared.Analysis) != 0 {
		t.Fatalf("choice remained stored: %s", cleared.Analysis)
	}
	for _, choice := range []string{"restricted", "trusted"} {
		m.SetValueForTest(catLSP, fieldAnalysisTrust, choice)
		updated := applyResetToOverrides(t, stored, m.Edits())
		if string(updated.Analysis) != `{"trust":"`+choice+`"}` {
			t.Fatalf("later %s choice not stored: %s", choice, updated.Analysis)
		}
	}
	unchanged := analysisModel(types.AppSettings{}, types.WorkspaceOverrides{})
	if len(unchanged.Edits()) != 0 {
		t.Fatalf("unchanged Ask produced edits: %+v", unchanged.Edits())
	}
}
