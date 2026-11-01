package types

import (
	"encoding/json"
	"testing"
)

func TestPredictionStoredOuterAuthorityCannotBeRepairedByJSON(t *testing.T) {
	for _, raw := range []string{
		`{"prediction":{"mode":"off"},"prediction":{"mode":"light","hourly_token_limit":4200}}`,
		`{"Prediction":{"mode":"light","hourly_token_limit":4200}}`,
		`{"prediction_root_identity":"old","prediction_root_identity":"new"}`,
		`{"prediction_connection_identity":"old","PREDICTION_CONNECTION_IDENTITY":"new"}`,
		`{"prediction_grant_id":"old","prediction_grant_id":"new"}`,
	} {
		var overrides WorkspaceOverrides
		if err := json.Unmarshal([]byte(raw), &overrides); err == nil {
			t.Fatal("stored outer prediction authority was silently repaired", raw)
		}
	}
	// Ordinary unknown neighbors retain their existing permissive semantics;
	// invalid INNER authority remains byte-exact for explicit reset/repair.
	raw := `{"editor":{"tab_size":7},"unrelated":true,"prediction":{"mode":"off","mode":"light","hourly_token_limit":4200}}`
	var overrides WorkspaceOverrides
	if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
		t.Fatal(err)
	}
	if string(overrides.Editor) != `{"tab_size":7}` ||
		string(overrides.Prediction) != `{"mode":"off","mode":"light","hourly_token_limit":4200}` ||
		MergeAppWorkspace(AppSettings{}, overrides).Prediction.Mode != PredictionOff {
		t.Fatal("stored ambiguity was normalized, granted, or consumed its ordinary neighbor")
	}
}
