package types

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPredictionApplicationAuthorityNeverFlowsIntoWorkspace(t *testing.T) {
	var application AppSettings
	if err := json.Unmarshal([]byte(`{"prediction":{"mode":"type","hourly_token_limit":9000}}`), &application); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"", `null`, `{"mode":"type"}`,
		`{"mode":"ask","hourly_token_limit":4000,"hourly_token_limit":9000}`,
		`{"mode":"type","hourly_token_limit":9000,"unknown":true}`,
		`{"mode":"off","hourly_token_limit":0}`,
	} {
		resolved := MergeAppWorkspace(application, WorkspaceOverrides{Prediction: json.RawMessage(raw)})
		if resolved.Prediction.Mode != PredictionOff || resolved.Prediction.HourlyTokenLimit != 0 {
			t.Fatalf("workspace %q borrowed application prediction authority: %+v", raw, resolved.Prediction)
		}
	}
	stored := WorkspaceOverrides{Prediction: json.RawMessage(`{"mode":"ask","hourly_token_limit":4200}`)}
	choice := MergeAppWorkspace(application, stored)
	if choice.Prediction.Mode != PredictionAsk || choice.Prediction.HourlyTokenLimit != 4200 {
		t.Fatal("workspace choice was not resolved independently", choice.Prediction)
	}
	application.Prediction.Mode = PredictionOff
	if got := MergeAppWorkspace(application, stored); got.Prediction != choice.Prediction {
		t.Fatal("application edits changed the workspace-only choice", got.Prediction)
	}
	if clone := CloneAppSettings(choice); clone.Prediction != choice.Prediction {
		t.Fatal("detached settings snapshot lost the resolved choice")
	}
}

func TestPredictionOverlayResetDropsOnlyItsOwnAuthority(t *testing.T) {
	var application AppSettings
	ApplyAppDefaults(&application)
	working := CloneAppSettings(application)
	working.Prediction = WorkspacePredictionSettings{Mode: PredictionAsk, HourlyTokenLimit: 4200}
	working.Editor.TabSize = 7
	overlay := OverridesFromDiff(application, working)
	if !overlay.CategoryOverridden("prediction") || overlay.IsEmpty() ||
		string(overlay.Prediction) != `{"hourly_token_limit":4200,"mode":"ask"}` {
		t.Fatal("explicit workspace prediction choice was not retained", string(overlay.Prediction))
	}
	overlay.PredictionRootIdentity, overlay.PredictionConnectionIdentity, overlay.PredictionGrantID = "native", "connection", "grant"
	overlay.Analysis = json.RawMessage(`{"trust":"restricted"}`)
	overlay.AnalysisRootIdentity = "foreign-analysis"
	neighbor := append([]byte(nil), overlay.Editor...)
	overlay.ClearCategory("prediction")
	if overlay.CategoryOverridden("prediction") || overlay.Prediction != nil ||
		overlay.PredictionRootIdentity != "" || overlay.PredictionConnectionIdentity != "" || overlay.PredictionGrantID != "" {
		t.Fatal("prediction reset retained grant material", overlay)
	}
	if !reflect.DeepEqual([]byte(overlay.Editor), neighbor) || overlay.AnalysisRootIdentity != "foreign-analysis" ||
		string(overlay.Analysis) != `{"trust":"restricted"}` {
		t.Fatal("prediction reset modified unrelated preferences or execution Trust")
	}
}
