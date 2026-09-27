package sessionsettings

import (
	"testing"

	"crude/provider"
	"crude/session"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

// memoryAgentCfg is the shape a real config carries: an active profile whose
// main agent differs from the memory agent, plus a memory agent override stored
// in Memory.MemoryCategory (the workspace-level source of truth for that role).
func memoryAgentCfg() *session.Config {
	comp := 85
	return &session.Config{
		ActiveProvider: "commandcode",
		Providers: map[string]session.ProviderConfig{
			"commandcode":  {ProviderURL: "https://cc"},
			"ollama-cloud": {ProviderURL: "https://oc"},
		},
		Agents: session.AgentsConfig{
			Enabled:       true,
			ActiveProfile: "test",
			Profiles: map[string]session.ProfileConfig{
				"test": {Categories: map[string]session.CategoryConfig{
					"default":    {AccountID: "main", Model: "main-agent-model", ThinkingEffort: "high"},
					"researcher": {AccountID: "memory", Model: "glm-5.3-flash", ThinkingEffort: "high"},
				}},
			},
		},
		Memory: types.MemorySettings{
			AutoRecall: "on", AutoExtract: "on", Consolidation: "on",
			MemoryCategory: types.CategorySettings{
				Provider: "ollama-cloud", AccountID: "memory",
				Model:                    "memory-agent-model",
				ThinkingEffort:           "high",
				ContextCompactionPercent: &comp,
			},
		},
	}
}

// applyOverrides mirrors the manual-override branch of
// session.configWithSessionOverrides so a test can resolve what the backend
// would use after a deck save.
func applyOverrides(base *session.Config, overrides map[string]types.CategorySettings) *session.Config {
	out := *base
	out.Agents.Profiles = make(map[string]session.ProfileConfig, len(base.Agents.Profiles))
	for name, prof := range base.Agents.Profiles {
		pc := session.ProfileConfig{Categories: make(map[string]session.CategoryConfig, len(prof.Categories))}
		for k, v := range prof.Categories {
			pc.Categories[k] = v
		}
		out.Agents.Profiles[name] = pc
	}
	profileName := out.Agents.ActiveProfile
	if profileName == "" {
		profileName = "default"
	}
	profile := out.Agents.Profiles[profileName]
	if profile.Categories == nil {
		profile.Categories = make(map[string]session.CategoryConfig)
	}
	for category, ov := range overrides {
		current := profile.Categories[category]
		if ov.Inherit != "" {
			profile.Categories[category] = session.CategoryConfig{Inherit: ov.Inherit}
			continue
		}
		if ov.AccountID != "" {
			current.AccountID = ov.AccountID
		}
		if ov.Model != "" {
			current.Model = ov.Model
		}
		if ov.ThinkingEffort != "" {
			current.ThinkingEffort = ov.ThinkingEffort
		}
		current.Inherit = ""
		profile.Categories[category] = current
	}
	out.Agents.Profiles[profileName] = profile
	return &out
}

func openSettingsDeck(t *testing.T, st types.Settings) *Screen {
	t.Helper()
	screen := New(theme.DefaultDark(), cloneSettings(st), st, nil)
	screen.ConfigureAccounts(map[string]provider.Account{"main": {ID: "main", ProviderID: "commandcode", Label: "Main"}, "memory": {ID: "memory", ProviderID: "ollama-cloud", Label: "Memory"}})
	deck := ui.NewPanelDeck("deck", ui.NewTokenSet(theme.DefaultDark()))
	deck.Open(ui.PanelEntry{ID: "settings", Title: "Session Settings", Content: screen, Closable: true, Policy: ui.PanelInteractive, PreferredHeight: 14})
	host := ui.NewHost(theme.DefaultDark(), deck)
	host.Resize(100, 22)
	host.Layout()
	host.Focus(FormID)
	return screen
}

// TestMemoryAgentRoleShowsItsOwnModel guards the memory role's projection. The
// role's workspace-level source is Memory.MemoryCategory, not
// Agents.Categories["memory"], so without this the row and the role form
// prefill the MAIN agent's model. That display is what Apply writes back, which
// is how an explicitly configured memory agent silently became the main model.
func TestMemoryAgentRoleShowsItsOwnModel(t *testing.T) {
	cfg := memoryAgentCfg()
	st := session.ConfigToSettings(cfg)

	if got := cfg.GetMemoryCategory().Model; got != "memory-agent-model" {
		t.Fatalf("backend memory category = %q, want memory-agent-model", got)
	}

	screen := openSettingsDeck(t, st)
	screen.ConfigureAccounts(map[string]provider.Account{"main": {ID: "main", ProviderID: "commandcode", Label: "Main"}, "memory": {ID: "memory", ProviderID: "ollama-cloud", Label: "Memory"}})
	screen.openRoot()
	row := screen.formModel.Field(memoryRole)
	if row == nil {
		t.Fatal("root form missing Memory Agent row")
	}
	if len(row.Columns) < 2 || row.Columns[0] != "memory-agent-model" || row.Columns[1] != "Memory" {
		t.Errorf("Memory Agent row shows %v, want model=memory-agent-model account=Memory", row.Columns)
	}

	screen.openRole(memoryRole)
	if got := screen.formModel.Field(fieldModel).Value.String(); got != "memory-agent-model" {
		t.Errorf("memory role form prefills model %q, want memory-agent-model", got)
	}
	if got := screen.formModel.Field(fieldProvider).Value.String(); got != "memory" {
		t.Errorf("memory role form prefills account %q, want ollama-cloud", got)
	}
}

// TestMemoryAgentRoleApplyPreservesModel is the regression itself: entering the
// memory role, changing an unrelated field, and pressing Apply must not replace
// the configured model with the main agent's.
func TestMemoryAgentRoleApplyPreservesModel(t *testing.T) {
	cfg := memoryAgentCfg()
	st := session.ConfigToSettings(cfg)

	screen := openSettingsDeck(t, st)
	screen.ConfigureAccounts(map[string]provider.Account{"main": {ID: "main", ProviderID: "commandcode", Label: "Main"}, "memory": {ID: "memory", ProviderID: "ollama-cloud", Label: "Memory"}})
	screen.openRole(memoryRole)
	screen.formModel.Field(fieldThinking).Value = ui.StringValue("medium")
	screen.applyRoleForm()

	out := screen.ToSettings()
	if got := out.Memory.MemoryCategory.Model; got != "memory-agent-model" {
		t.Fatalf("ToSettings Memory.MemoryCategory.Model = %q, want memory-agent-model", got)
	}

	resolved := applyOverrides(cfg, out.SessionOverrides).GetMemoryCategory()
	if resolved.Model != "memory-agent-model" || resolved.AccountID != "memory" {
		t.Fatalf("after save backend resolves account=%q model=%q, want memory/memory-agent-model",
			resolved.AccountID, resolved.Model)
	}
}

// TestMemoryAgentRoleExplicitModelOverrides verifies a deliberate change in the
// memory role still reaches the backend.
func TestMemoryAgentRoleExplicitModelOverrides(t *testing.T) {
	cfg := memoryAgentCfg()
	st := session.ConfigToSettings(cfg)

	screen := openSettingsDeck(t, st)
	screen.ConfigureAccounts(map[string]provider.Account{"main": {ID: "main", ProviderID: "commandcode", Label: "Main"}, "memory": {ID: "memory", ProviderID: "ollama-cloud", Label: "Memory"}})
	screen.openRole(memoryRole)
	screen.formModel.Field(fieldModel).Value = ui.StringValue("chosen-memory-model")
	screen.applyRoleForm()

	out := screen.ToSettings()
	if got := out.SessionOverrides[memoryRole].Model; got != "chosen-memory-model" {
		t.Fatalf("SessionOverrides[memory].Model = %q, want chosen-memory-model", got)
	}
	if got := out.Memory.MemoryCategory.Model; got != "chosen-memory-model" {
		t.Fatalf("Memory.MemoryCategory.Model = %q, want chosen-memory-model", got)
	}

	resolved := applyOverrides(cfg, out.SessionOverrides).GetMemoryCategory()
	if resolved.Model != "chosen-memory-model" {
		t.Fatalf("after save backend resolves %q, want chosen-memory-model", resolved.Model)
	}
}

// TestMemoryAgentRoleResetRestoresWorkspaceSetting verifies the recovery paths
// re-inherit Memory.MemoryCategory rather than the main agent.
func TestMemoryAgentRoleResetRestoresWorkspaceSetting(t *testing.T) {
	for _, tc := range []struct {
		name string
		act  func(*Screen)
	}{
		{"reset", func(s *Screen) { s.openRole(memoryRole); s.resetRole(memoryRole) }},
		{"inherit-workspace", func(s *Screen) { s.openRole(memoryRole); s.inheritWorkspace(memoryRole) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := memoryAgentCfg()
			st := session.ConfigToSettings(cfg)
			screen := openSettingsDeck(t, st)
			screen.ConfigureAccounts(map[string]provider.Account{"main": {ID: "main", ProviderID: "commandcode", Label: "Main"}, "memory": {ID: "memory", ProviderID: "ollama-cloud", Label: "Memory"}})
			tc.act(screen)

			out := screen.ToSettings()
			if _, ok := out.Agents.Categories[memoryRole]; ok {
				t.Fatalf("%s left a session memory category: %+v", tc.name, out.Agents.Categories[memoryRole])
			}
			if got := out.Memory.MemoryCategory.Model; got != "memory-agent-model" {
				t.Fatalf("%s Memory.MemoryCategory.Model = %q, want memory-agent-model", tc.name, got)
			}
			resolved := applyOverrides(cfg, out.SessionOverrides).GetMemoryCategory()
			if resolved.Model != "memory-agent-model" {
				t.Fatalf("%s: backend resolves %q, want memory-agent-model", tc.name, resolved.Model)
			}
		})
	}
}

// TestMemoryAgentRoleInheritMainStillWorks keeps the explicit "inherit from
// main" choice meaningful for the memory role.
func TestMemoryAgentRoleInheritMainStillWorks(t *testing.T) {
	cfg := memoryAgentCfg()
	st := session.ConfigToSettings(cfg)

	screen := openSettingsDeck(t, st)
	screen.ConfigureAccounts(map[string]provider.Account{"main": {ID: "main", ProviderID: "commandcode", Label: "Main"}, "memory": {ID: "memory", ProviderID: "ollama-cloud", Label: "Memory"}})
	screen.inheritMain(memoryRole)
	out := screen.ToSettings()
	if got := out.SessionOverrides[memoryRole].Inherit; got != InheritMainMarker {
		t.Fatalf("SessionOverrides[memory].Inherit = %q, want %q", got, InheritMainMarker)
	}

	resolved := applyOverrides(cfg, out.SessionOverrides).GetMemoryCategory()
	if resolved.Model != "main-agent-model" {
		t.Fatalf("inherit-main resolved memory model = %q, want main-agent-model", resolved.Model)
	}
}
