package types

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAnalysisTrustNeverInheritsApplicationGrant(t *testing.T) {
	var app AppSettings
	if err := json.Unmarshal([]byte(`{"analysis":{"trust":"trusted"},"lsp":{"diagnostics":false}}`), &app); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", `{}`, `null`, `{"trust":null}`, `{"trust":"unknown"}`, `{"trust":true}`,
		`{"trust":"trusted","unknown":true}`, `{"trust":"trusted"}{}`,
		`{"trust":"static_only","trust":"trusted"}`, `{"trust":"static_only","\u0074rust":"trusted"}`} {
		t.Run(raw, func(t *testing.T) {
			effective := MergeAppWorkspace(app, WorkspaceOverrides{Analysis: json.RawMessage(raw)})
			if effective.Analysis.Trust != AnalysisUndecided {
				t.Fatalf("invalid/absent workspace choice inherited execution: %+v", effective.Analysis)
			}
		})
	}
	trusted := WorkspaceOverrides{Analysis: json.RawMessage(`{"trust":"trusted"}`)}
	effective := MergeAppWorkspace(app, trusted)
	if effective.Analysis.Trust != AnalysisTrusted {
		t.Fatal("explicit workspace choice was lost")
	}
	// An application update cannot relabel either workspace's own choice.
	app.Analysis.Trust = AnalysisUndecided
	if MergeAppWorkspace(app, trusted).Analysis.Trust != AnalysisTrusted ||
		MergeAppWorkspace(app, WorkspaceOverrides{}).Analysis.Trust != AnalysisUndecided {
		t.Fatal("application changes leaked across workspace trust boundaries")
	}
	if got := CloneAppSettings(effective); !reflect.DeepEqual(got.Analysis, effective.Analysis) {
		t.Fatal("cloning an already-effective observation lost the workspace choice")
	}
}

func TestAnalysisTrustEditsAreWorkspaceOnlyAndStrict(t *testing.T) {
	for _, path := range [][]string{{"analysis"}, {"analysis", "trust"}} {
		for _, remove := range []bool{false, true} {
			value := json.RawMessage(`"trusted"`)
			if len(path) == 1 {
				value = json.RawMessage(`{"trust":"trusted"}`)
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
	for _, value := range []string{`null`, `{}`, `{"trust":"unknown"}`, `{"trust":"static_only","root_identity":"forged"}`, `{"trust":"trusted"}{}`,
		`{"trust":"static_only","trust":"trusted"}`, `{"trust":"static_only","\u0074rust":"trusted"}`} {
		edit := SettingEdit{Path: []string{"analysis"}, After: json.RawMessage(value)}
		if err := ValidateAnalysisSettingEdits(SettingsScopeWorkspace, []SettingEdit{edit}); err == nil {
			t.Fatalf("invalid trust container %s was accepted", value)
		}
	}
}

func TestAnalysisTrustOverlayTracksOnlyItsOwnChoice(t *testing.T) {
	app := AppSettings{Analysis: WorkspaceAnalysisSettings{Trust: AnalysisTrusted}}
	working := MergeAppWorkspace(app, WorkspaceOverrides{})
	if diff := OverridesFromDiff(app, working); len(diff.Analysis) != 0 {
		t.Fatal("a forged app grant made Undecided a workspace override")
	}
	working.Analysis.Trust = AnalysisTrusted
	diff := OverridesFromDiff(app, working)
	if diff.IsEmpty() || string(diff.Analysis) != `{"trust":"trusted"}` {
		t.Fatalf("explicit workspace grant lost: %s", diff.Analysis)
	}
	if !diff.CategoryOverridden("analysis") {
		t.Fatal("workspace trust choice lost the shared category marker")
	}
	if got := MergeAppWorkspace(AppSettings{}, diff); got.Analysis.Trust != AnalysisTrusted {
		t.Fatal("workspace-only sparse choice failed round trip")
	}
	if err := ApplySettingsEdits(&diff, []SettingEdit{{Path: []string{"analysis", "trust"}}}); err != nil {
		t.Fatal(err)
	}
	if got := MergeAppWorkspace(app, diff); got.Analysis.Trust != AnalysisUndecided {
		t.Fatal("Reset inherited an application grant instead of Undecided")
	}
	diff.Analysis = json.RawMessage(`{"trust":"trusted"}`)
	diff.AnalysisRootIdentity = "owner-binding"
	diff.ClearCategory("analysis")
	if diff.CategoryOverridden("analysis") || diff.AnalysisRootIdentity != "" {
		t.Fatal("shared category reset retained a trust choice")
	}
}

func TestAnalysisDecisionAndRestrictedDiff(t *testing.T) {
	for _, tc := range []struct {
		trust           WorkspaceAnalysisTrust
		trusted, needed bool
	}{
		{AnalysisUndecided, false, true},
		{AnalysisRestricted, false, false},
		{AnalysisTrusted, true, false},
		{"invalid", false, true},
	} {
		s := WorkspaceAnalysisSettings{Trust: tc.trust}
		if s.Trusted() != tc.trusted || s.DecisionNeeded() != tc.needed {
			t.Fatalf("%q: trusted=%v needed=%v", tc.trust, s.Trusted(), s.DecisionNeeded())
		}
	}
	app := AppSettings{}
	working := MergeAppWorkspace(app, WorkspaceOverrides{})
	working.Analysis.Trust = AnalysisRestricted
	diff := OverridesFromDiff(app, working)
	if string(diff.Analysis) != `{"trust":"restricted"}` || MergeAppWorkspace(app, diff).Analysis.Trust != AnalysisRestricted {
		t.Fatalf("restricted choice did not survive diff and merge: %s", diff.Analysis)
	}
	working.Analysis.Trust = AnalysisUndecided
	if diff := OverridesFromDiff(app, working); len(diff.Analysis) != 0 {
		t.Fatalf("undecided was stored: %s", diff.Analysis)
	}
}
