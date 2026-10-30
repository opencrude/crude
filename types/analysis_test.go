package types

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAnalysisTrustNeverInheritsApplicationGrant(t *testing.T) {
	var app AppSettings
	if err := json.Unmarshal([]byte(`{"analysis":{"trust":"project_execution_allowed"},"lsp":{"diagnostics":false}}`), &app); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", `{}`, `null`, `{"trust":null}`, `{"trust":"unknown"}`, `{"trust":true}`,
		`{"trust":"project_execution_allowed","unknown":true}`, `{"trust":"project_execution_allowed"}{}`,
		`{"trust":"static_only","trust":"project_execution_allowed"}`, `{"trust":"static_only","\u0074rust":"project_execution_allowed"}`} {
		t.Run(raw, func(t *testing.T) {
			effective := MergeAppWorkspace(app, WorkspaceOverrides{Analysis: json.RawMessage(raw)})
			if effective.Analysis.Trust != AnalysisStaticOnly {
				t.Fatalf("invalid/absent workspace choice inherited execution: %+v", effective.Analysis)
			}
		})
	}
	trusted := WorkspaceOverrides{Analysis: json.RawMessage(`{"trust":"project_execution_allowed"}`)}
	effective := MergeAppWorkspace(app, trusted)
	if effective.Analysis.Trust != AnalysisProjectExecutionAllowed {
		t.Fatal("explicit workspace choice was lost")
	}
	// An application update cannot relabel either workspace's own choice.
	app.Analysis.Trust = AnalysisStaticOnly
	if MergeAppWorkspace(app, trusted).Analysis.Trust != AnalysisProjectExecutionAllowed ||
		MergeAppWorkspace(app, WorkspaceOverrides{}).Analysis.Trust != AnalysisStaticOnly {
		t.Fatal("application changes leaked across workspace trust boundaries")
	}
	if got := CloneAppSettings(effective); !reflect.DeepEqual(got.Analysis, effective.Analysis) {
		t.Fatal("cloning an already-effective observation lost the workspace choice")
	}
}

func TestAnalysisTrustEditsAreWorkspaceOnlyAndStrict(t *testing.T) {
	for _, path := range [][]string{{"analysis"}, {"analysis", "trust"}} {
		for _, remove := range []bool{false, true} {
			value := json.RawMessage(`"project_execution_allowed"`)
			if len(path) == 1 {
				value = json.RawMessage(`{"trust":"project_execution_allowed"}`)
			}
			edit := SettingEdit{Path: path, After: value}
			if remove {
				edit.After = nil
			}
			if err := ValidateAnalysisSettingEdits(SettingsScopeWorkspace, []SettingEdit{edit}); err != nil {
				t.Fatal(err)
			}
			if err := ValidateAnalysisSettingEdits(SettingsScopeApplication, []SettingEdit{edit}); err == nil {
				t.Fatal("application field or container write/reset authored workspace trust")
			}
		}
	}
	for _, value := range []string{`null`, `""`, `"allowed"`, `"PROJECT_EXECUTION_ALLOWED"`, `true`, `12`, `{}`, `[]`} {
		edit := SettingEdit{Path: []string{"analysis", "trust"}, After: json.RawMessage(value)}
		if err := ValidateAnalysisSettingEdits(SettingsScopeWorkspace, []SettingEdit{edit}); err == nil {
			t.Fatalf("invalid trust write %s was accepted", value)
		}
	}
	for _, value := range []string{`null`, `{}`, `{"trust":"unknown"}`, `{"trust":"static_only","root_identity":"forged"}`, `{"trust":"project_execution_allowed"}{}`,
		`{"trust":"static_only","trust":"project_execution_allowed"}`, `{"trust":"static_only","\u0074rust":"project_execution_allowed"}`} {
		edit := SettingEdit{Path: []string{"analysis"}, After: json.RawMessage(value)}
		if err := ValidateAnalysisSettingEdits(SettingsScopeWorkspace, []SettingEdit{edit}); err == nil {
			t.Fatalf("invalid trust container %s was accepted", value)
		}
	}
}

func TestAnalysisTrustOverlayTracksOnlyItsOwnChoice(t *testing.T) {
	app := AppSettings{Analysis: WorkspaceAnalysisSettings{Trust: AnalysisProjectExecutionAllowed}}
	working := MergeAppWorkspace(app, WorkspaceOverrides{})
	if diff := OverridesFromDiff(app, working); len(diff.Analysis) != 0 {
		t.Fatal("a forged app grant made Static only a workspace override")
	}
	working.Analysis.Trust = AnalysisProjectExecutionAllowed
	diff := OverridesFromDiff(app, working)
	if diff.IsEmpty() || string(diff.Analysis) != `{"trust":"project_execution_allowed"}` {
		t.Fatalf("explicit workspace grant lost: %s", diff.Analysis)
	}
	if !diff.CategoryOverridden("analysis") {
		t.Fatal("workspace trust choice lost the shared category marker")
	}
	if got := MergeAppWorkspace(AppSettings{}, diff); got.Analysis.Trust != AnalysisProjectExecutionAllowed {
		t.Fatal("workspace-only sparse choice failed round trip")
	}
	if err := ApplySettingsEdits(&diff, []SettingEdit{{Path: []string{"analysis", "trust"}}}); err != nil {
		t.Fatal(err)
	}
	if got := MergeAppWorkspace(app, diff); got.Analysis.Trust != AnalysisStaticOnly {
		t.Fatal("Reset inherited an application grant instead of Static only")
	}
	diff.Analysis = json.RawMessage(`{"trust":"project_execution_allowed"}`)
	diff.AnalysisRootIdentity = "owner-binding"
	diff.ClearCategory("analysis")
	if diff.CategoryOverridden("analysis") || diff.AnalysisRootIdentity != "" {
		t.Fatal("shared category reset retained a trust choice")
	}
}
