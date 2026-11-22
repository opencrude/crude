package control_test

import (
	"testing"

	"crude/types/control"
	"crude/types/setting"
)

func TestAnalysisTrustAndContainerWritesAreHumanOwned(t *testing.T) {
	for _, path := range []string{"analysis", "analysis.trust", "Analysis.Trust", "value.analysis.trust"} {
		if !control.SettingsAuthority(path) {
			t.Fatalf("trust authority path not protected: %s", path)
		}
	}
	before := setting.App{Analysis: setting.DefaultWorkspaceAnalysis()}
	after := setting.CloneApp(before)
	after.Analysis.Trust = setting.AnalysisTrusted
	if !control.SettingsChangesAuthority(before, after, "") {
		t.Fatal("a pending trust draft escaped whole-form authority admission")
	}
	if control.SettingsChangesAuthority(after, setting.CloneApp(after), "") {
		t.Fatal("unchanged observation fabricated an authority write")
	}
}
