package settings

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"crude/provider"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestWorkspaceCodeIntelligenceEditDoesNotWriteAgents(t *testing.T) {
	base := types.AppSettings{Accounts: map[string]provider.Account{"one": {ID: "one", ProviderID: "openai"}},
		Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {AccountID: "one", Model: "main-model"}}}}
	m := NewScoped(theme.DefaultDark(), base, nil, types.SettingsScopeWorkspace, base, types.WorkspaceOverrides{})
	if len(m.Edits()) != 0 {
		t.Fatal("opening settings created edits")
	}
	m.SetValueForTest(catLSP, "enabled", "off")
	overrides := m.Overrides()
	if len(overrides.Agents) != 0 || string(overrides.LSP) != `{"enabled":false}` {
		t.Fatalf("LSP-only edit wrote unrelated settings: %+v", overrides)
	}
}

func TestApplicationWorkspaceMarkerIsQuietAndDoesNotChangeValue(t *testing.T) {
	for _, size := range [][2]int{{130, 32}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			base := types.AppSettings{Editor: types.EditorSettings{TabSize: 4}}
			overrides := types.WorkspaceOverrides{Editor: json.RawMessage(`{"tab_size":8}`)}
			m := NewScoped(theme.DefaultDark(), base, nil, types.SettingsScopeApplication, base, overrides)
			m.SelectCategory(catEditor)
			s := NewSurface(theme.DefaultDark(), m)
			h := ui.NewHost(theme.DefaultDark(), s)
			h.Resize(size[0], size[1])
			h.Layout()
			buf := surface.NewCellBuffer(size[0], size[1])
			h.Paint(buf)
			if !strings.Contains(bufferPlain(buf), "◇ Workspace") {
				t.Fatal(bufferPlain(buf))
			}
			if m.AppSettings().Editor.TabSize != 4 || len(m.Edits()) != 0 {
				t.Fatal("marker replaced Application values or dirtied draft")
			}
			for _, item := range m.CategoryItems() {
				if item.Label == "Editor" && item.Detail != railWorkspaceMarker {
					t.Fatal("missing quiet rail marker")
				}
			}
			for _, scope := range []types.SettingsScope{types.SettingsScopeApplication, types.SettingsScopeWorkspace} {
				stored := types.WorkspaceOverrides{}
				if scope == types.SettingsScopeWorkspace {
					stored = overrides
				}
				clean := NewScoped(theme.DefaultDark(), base, nil, scope, base, stored)
				for _, item := range clean.CategoryItems() {
					if item.Detail == railWorkspaceMarker {
						t.Fatal("marker on unoverridden Application or Workspace view")
					}
				}
			}
		})
	}
}

func TestWorkspaceSectionResetPreservesUnrelatedDraft(t *testing.T) {
	app := types.AppSettings{}
	types.ApplyAppDefaults(&app)
	stored := types.WorkspaceOverrides{Editor: json.RawMessage(`{"tab_size":8}`), LSP: json.RawMessage(`{"enabled":false}`)}
	m := NewScoped(theme.DefaultDark(), types.MergeAppWorkspace(app, stored), nil, types.SettingsScopeWorkspace, app, stored)
	m.SetValueForTest(catUI, "sidebar-width", "45")
	stored.Editor = nil
	next := m.SectionResetModel(app, types.MergeAppWorkspace(app, stored), stored, "editor")
	if next.AppSettings().Editor.TabSize != 4 || next.AppSettings().UI.SidebarWidth != 45 {
		t.Fatal("section reset lost parent or unrelated draft")
	}
	got := next.Overrides()
	if len(got.Editor) != 0 || string(got.LSP) != `{"enabled":false}` || string(got.UI) != `{"sidebar_width":45}` {
		t.Fatalf("bad reset write set: %+v", got)
	}
}

func TestWorkspaceMCPMarkerDoesNotMarkOtherCapabilityPages(t *testing.T) {
	base := types.AppSettings{}
	m := NewScoped(theme.DefaultDark(), base, nil, types.SettingsScopeApplication, base, types.WorkspaceOverrides{Capabilities: json.RawMessage(`{"connections":{"one":{"disabled":true}}}`)})
	if !m.workspaceOverridesPage(catMCP) || m.workspaceOverridesPage(catSkills) || m.workspaceOverridesPage(catSources) || m.workspaceOverridesPage(catAcquisition) {
		t.Fatal("MCP workspace choice marked unrelated capability pages")
	}
}

func TestPreferenceCollectorsWriteOnlyEditedLeafAcrossPages(t *testing.T) {
	for _, scope := range []types.SettingsScope{types.SettingsScopeApplication, types.SettingsScopeWorkspace} {
		for _, tc := range []struct {
			name               string
			page               int
			field, value, path string
		}{
			{"Appearance", catAppearance, "preset", "light", "theme.preset"},
			{"Editor", catEditor, "tab-size", "8", "editor.tab_size"},
			{"Interface", catUI, "sidebar-width", "45", "ui.sidebar_width"},
			{"Code Intelligence", catLSP, "enabled", "off", "lsp.enabled"},
			{"Memory", catMemory, "recall-limit", "14", "memory.recall_limit"},
			{"Local History", catHistory, fieldHistoryBudget, "512", "local_history.storage_budget_mb"},
			{"Terminal", catTerminal, fieldTerminalScrollback, "4000", "terminal.scrollback_lines"},
			{"Security", catSafety, fieldSafetyNotify, "off", "safety.notify_dangerous"},
			{"Network", catNetworkConnection, fieldNetworkConnectTimeout, "45", "network.connect_timeout_seconds"},
		} {
			t.Run(string(scope)+"/"+tc.name, func(t *testing.T) {
				if scope == types.SettingsScopeWorkspace && tc.page == catNetworkConnection {
					return
				}
				base := types.AppSettings{}
				types.ApplyAppDefaults(&base)
				stored := types.WorkspaceOverrides{Editor: json.RawMessage(`{"word_wrap":true}`)}
				current := base
				if scope == types.SettingsScopeWorkspace {
					current = types.MergeAppWorkspace(base, stored)
				}
				m := NewScoped(theme.DefaultDark(), current, nil, scope, base, stored)
				if len(m.Edits()) != 0 {
					t.Fatal("opening created changes")
				}
				m.SetValueForTest(tc.page, tc.field, tc.value)
				edits := m.Edits()
				if len(edits) != 1 || strings.Join(edits[0].Path, ".") != tc.path {
					t.Fatalf("one field wrote neighbors: %+v", edits)
				}
				if !m.BeginSave() {
					t.Fatal(m.SaveError())
				}
				m.FinishSave(fmt.Errorf("fixture disk failure"))
				if len(m.Edits()) != 1 || m.SaveError() == "" {
					t.Fatal("failed save consumed draft")
				}
			})
		}
	}
}
