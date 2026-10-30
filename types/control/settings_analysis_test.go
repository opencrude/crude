package control_test

import (
	"testing"

	"crude/types"
	"crude/types/control"
)

func TestAnalysisTrustAndContainerWritesAreHumanOwned(t *testing.T) {
	for _, path := range []string{"analysis", "analysis.trust", "Analysis.Trust", "value.analysis.trust"} {
		if !control.SettingsAuthority(path) {
			t.Fatalf("trust authority path not protected: %s", path)
		}
	}
	before := types.AppSettings{Analysis: types.DefaultWorkspaceAnalysisSettings()}
	after := types.CloneAppSettings(before)
	after.Analysis.Trust = types.AnalysisProjectExecutionAllowed
	if !control.SettingsChangesAuthority(before, after, "") {
		t.Fatal("a pending trust draft escaped whole-form authority admission")
	}
	if control.SettingsChangesAuthority(after, types.CloneAppSettings(after), "") {
		t.Fatal("unchanged observation fabricated an authority write")
	}
}
