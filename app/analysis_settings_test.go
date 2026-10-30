package app

import (
	"encoding/json"
	"reflect"
	"testing"

	"crude/types"
)

func TestApplicationPreferenceOwnerRejectsWorkspaceAnalysisWrites(t *testing.T) {
	for _, edit := range []types.SettingEdit{
		{Path: []string{"analysis", "trust"}, After: json.RawMessage(`"project_execution_allowed"`)},
		{Path: []string{"analysis"}, After: json.RawMessage(`{"trust":"project_execution_allowed"}`)},
		{Path: []string{"analysis", "trust"}},
		{Path: []string{"analysis"}},
	} {
		current := FileConfig{}
		before := current
		desired := types.AppSettings{Analysis: types.WorkspaceAnalysisSettings{Trust: types.AnalysisProjectExecutionAllowed}}
		if err := applyPreferenceEdits(&current, desired, []types.SettingEdit{edit}); err == nil {
			t.Fatal("application config owner accepted workspace trust", edit.Path)
		}
		if !reflect.DeepEqual(current, before) {
			t.Fatal("rejected workspace authority changed application config")
		}
	}
}
