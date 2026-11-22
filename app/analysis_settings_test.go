package app

import (
	"encoding/json"
	"reflect"
	"testing"

	"crude/types/setting"
)

func TestApplicationPreferenceOwnerRejectsWorkspaceAnalysisWrites(t *testing.T) {
	for _, edit := range []setting.Edit{
		{Path: []string{"analysis", "trust"}, After: json.RawMessage(`"trusted"`)},
		{Path: []string{"analysis"}, After: json.RawMessage(`{"trust":"trusted"}`)},
		{Path: []string{"analysis", "trust"}},
		{Path: []string{"analysis"}},
	} {
		current := FileConfig{}
		before := current
		desired := setting.App{Analysis: setting.WorkspaceAnalysis{Trust: setting.AnalysisTrusted}}
		if err := applyPreferenceEdits(&current, desired, []setting.Edit{edit}); err == nil {
			t.Fatal("application config owner accepted workspace trust", edit.Path)
		}
		if !reflect.DeepEqual(current, before) {
			t.Fatal("rejected workspace authority changed application config")
		}
	}
}
