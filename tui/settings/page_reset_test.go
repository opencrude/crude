package settings

import (
	"encoding/json"
	"testing"

	"crude/tui/theme"
	"crude/types"
)

func applyResetToOverrides(t *testing.T, stored types.WorkspaceOverrides, edits []types.SettingEdit) types.WorkspaceOverrides {
	t.Helper()
	if err := types.ApplySettingsEdits(&stored, edits); err != nil {
		t.Fatal(err)
	}
	return stored
}

// A page reset removes exactly that page's choices. Pages sharing a stored
// section used to wipe each other (Skills cleared MCP, Memory cleared the
// Memory Agent, Code Intelligence cleared Language Servers).
func TestWorkspacePageResetRemovesOnlyThatPage(t *testing.T) {
	stored := types.WorkspaceOverrides{
		LSP:    json.RawMessage(`{"enabled":false,"languages":{"go":{"command":"gopls"}}}`),
		Memory: json.RawMessage(`{"recall_limit":3,"memory_category":{"model":"m"}}`),
		Editor: json.RawMessage(`{"word_wrap":false}`),
	}
	app := types.AppSettings{}
	types.ApplyAppDefaults(&app)
	m := NewScoped(theme.DefaultDark(), types.MergeAppWorkspace(app, stored), nil, types.SettingsScopeWorkspace, app, stored)

	m.SelectCategory(catLSP)
	edits, ok := m.PageResetEdits()
	if !ok {
		t.Fatal("Code Intelligence has a workspace reset")
	}
	got := applyResetToOverrides(t, stored, edits)
	if string(got.LSP) != `{"languages":{"go":{"command":"gopls"}}}` {
		t.Fatalf("Code Intelligence reset touched Language Servers: %s", got.LSP)
	}

	m.SelectCategory(catMemory)
	edits, _ = m.PageResetEdits()
	got = applyResetToOverrides(t, stored, edits)
	if string(got.Memory) != `{"memory_category":{"model":"m"}}` {
		t.Fatalf("Memory reset touched the Memory Agent: %s", got.Memory)
	}

	m.SelectCategory(catEditor)
	edits, _ = m.PageResetEdits()
	got = applyResetToOverrides(t, stored, edits)
	if len(got.Editor) != 0 || got.CategoryOverridden("editor") {
		t.Fatalf("explicit false survived the Editor reset: %s", got.Editor)
	}
}

func TestCollectionsOfferNoPageReset(t *testing.T) {
	for _, scope := range []types.SettingsScope{types.SettingsScopeApplication, types.SettingsScopeWorkspace} {
		m := NewScoped(theme.DefaultDark(), types.AppSettings{}, nil, scope, types.AppSettings{}, types.WorkspaceOverrides{})
		for _, category := range []int{catAccounts, catSkills, catMCP, catSources, catLanguageServers, catNetworkConnection} {
			m.selected = category
			if m.CanResetPage() {
				t.Fatalf("%s category %d offers a page reset", scope, category)
			}
		}
		m.selected = catAgents
		if m.CanResetPage() != (scope == types.SettingsScopeWorkspace) {
			t.Fatalf("%s Agents reset availability is wrong", scope)
		}
		m.selected = catEditor
		if !m.CanResetPage() {
			t.Fatalf("%s Editor lacks its page reset", scope)
		}
	}
}
