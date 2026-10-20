package settings

import (
	"strings"
	"testing"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestExecutionHygieneSettingsAreOrdinarySparseChoices(t *testing.T) {
	for _, scope := range []types.SettingsScope{types.SettingsScopeApplication, types.SettingsScopeWorkspace} {
		base := types.AppSettings{}
		types.ApplyAppDefaults(&base)
		m := NewScoped(theme.DefaultDark(), base, nil, scope, base, types.WorkspaceOverrides{})
		s := NewSurface(theme.DefaultDark(), m)
		m.SelectCategory(catAgents)
		c := s.agents
		c.begin("role/default", false)
		if len(m.Edits()) != 0 {
			t.Fatal("opening settings made edits")
		}
		for _, id := range []string{fieldExecutionWarnings, fieldExecutionCount, fieldExecutionAge, fieldExecutionStorage} {
			if c.draft.Field(id) == nil || executionHygienePath(id) == "" {
				t.Fatal("missing discoverable hygiene field", id)
			}
		}
		c.draft.SetValue(fieldExecutionCount, ui.StringValue("0"))
		if s.agentDraftAuthority() {
			t.Fatal("warning threshold treated as authority")
		}
		if !c.apply() {
			t.Fatal("hygiene preferences did not apply")
		}
		edits := m.Edits()
		if len(edits) != 1 || strings.Join(edits[0].Path, ".") != "agents.execution_hygiene.retained_count" || string(edits[0].After) != "0" {
			t.Fatalf("threshold wrote neighbors: %+v", edits)
		}
		_, count, age, storage := m.AppSettings().Agents.ExecutionHygiene.Values()
		if count != 0 || age != 24 || storage != 1024 {
			t.Fatal("explicit off or sibling defaults lost")
		}
		c.begin("role/default", false)
		if c.draft.Field(fieldExecutionCount).Value.String() != "0" {
			t.Fatal("reopened explicit zero lost")
		}
	}
}
