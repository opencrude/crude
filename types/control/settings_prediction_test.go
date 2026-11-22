package control_test

import (
	"testing"

	"crude/types/control"
	"crude/types/setting"
)

func TestPredictionConsentIsHumanAuthorityAtEverySettingsBoundary(t *testing.T) {
	for _, path := range []string{
		"prediction", "prediction.mode", "Prediction.Mode", "value.prediction.hourly_token_limit",
		"prediction.protocol", "prediction.endpoint_path",
		"prediction_root_identity", "prediction_connection_identity", "prediction_grant_id",
		"value.Prediction_Grant_ID", "editor.prediction.mode",
	} {
		if !control.SettingsAuthority(path) {
			t.Fatalf("ordinary control operation could change prediction authority at %q", path)
		}
	}
	before := setting.App{Prediction: setting.DefaultWorkspacePrediction()}
	after := setting.CloneApp(before)
	after.Prediction = setting.WorkspacePrediction{Mode: setting.PredictionAsk, HourlyTokenLimit: 4200}
	if !control.SettingsChangesAuthority(before, after, "") || !control.SettingsChangesAuthority(after, before, "") {
		t.Fatal("container save/reset bypassed prediction's human-only boundary")
	}
	after = setting.CloneApp(before)
	after.Editor.TabSize = 7
	if control.SettingsChangesAuthority(before, after, "") {
		t.Fatal("ordinary editor preference acquired prediction authority classification")
	}
}
