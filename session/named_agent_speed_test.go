package session

import (
	"path/filepath"
	"testing"

	"crude/agent"
	"crude/provider"
	"crude/types"
)

func TestNamedAgentSpeedFollowsProfileAndSessionOverrides(t *testing.T) {
	home := t.TempDir()
	cfg := testConfig()
	profile := cfg.Agents.Profiles["default"]
	profile.Categories[CategoryResearcher] = CategoryConfig{AccountID: "account", Model: "role", ServiceTier: "standard"}
	cfg.Agents.Profiles["default"] = profile
	cfg.Agents.AgentProfiles["reviewer"] = AgentProfileConfig{
		Category: CategoryResearcher, Model: "reviewer", ServiceTier: "fast",
	}
	cfg.Agents.AgentProfiles["other"] = AgentProfileConfig{Category: CategoryResearcher, Model: "other"}

	s, err := NewWithSessionDir("", cfg, home, home)
	if err != nil {
		t.Fatal(err)
	}
	assertTier := func(s *Session, want string, wantFast types.FastMode) {
		t.Helper()
		current := s.RuntimeProfile().GetEffectiveConfig("root")
		root := current.GetActiveDefaultCategory()
		p, err := current.providerForCategory(root)
		if err != nil {
			t.Fatal(err)
		}
		if got := p.ProtocolSettings().ServiceTier; got != want {
			t.Fatalf("selected root request tier = %q, want %q", got, want)
		}
		if got := s.SettingsSnapshot().FastMode; got != wantFast {
			t.Fatalf("fast indicator = %q, want %q", got, wantFast)
		}
		runner := &SubAgentRunner{effectiveCfg: current}
		child := agent.SubAgentSpec{}
		runner.resolveProfile("reviewer", CategoryResearcher, &child)
		if child.ServiceTier != root.ServiceTier {
			t.Fatalf("named child tier = %q, root tier = %q", child.ServiceTier, root.ServiceTier)
		}
		childProvider, err := current.providerForCategory(childProviderConfig(child))
		if err != nil {
			t.Fatal(err)
		}
		if got := childProvider.ProtocolSettings().ServiceTier; got != want {
			t.Fatalf("named child request tier = %q, want %q", got, want)
		}
		if other := current.Agents.AgentProfiles["other"]; other.ServiceTier != "" {
			t.Fatalf("another named agent inherited the override: %+v", other)
		}
	}

	settings := s.SettingsSnapshot()
	settings.SessionMainAgent = "profile/reviewer"
	if err := s.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	assertTier(s, "fast", types.FastModeOn)

	settings = s.SettingsSnapshot()
	settings.SessionOverrides["profile/reviewer"] = types.CategorySettings{ServiceTier: "standard"}
	if err := s.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	assertTier(s, "default", types.FastModeOff)
	if cfg.Agents.AgentProfiles["reviewer"].ServiceTier != "fast" {
		t.Fatal("session edit mutated the workspace profile")
	}
	id := s.SessionID()
	s.msgs = []provider.ChatMessage{provider.NewTextMessage("user", "Keep this session")}
	s.Close()

	resumed, err := ResumeSessionWithDir(filepath.Join(home, id+".db"), cfg, home, home)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	assertTier(resumed, "default", types.FastModeOff)

	settings = resumed.SettingsSnapshot()
	delete(settings.SessionOverrides, "profile/reviewer")
	if err := resumed.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	assertTier(resumed, "fast", types.FastModeOn)

	settings = resumed.SettingsSnapshot()
	settings.SessionOverrides["profile/reviewer"] = types.CategorySettings{AccountID: "missing"}
	if err := resumed.HandleSettingsSave(settings); err == nil {
		t.Fatal("named-agent edit accepted an unavailable account")
	}
	assertTier(resumed, "fast", types.FastModeOn)
}

func TestInheritedRoleSessionSpeedCanBeOverridden(t *testing.T) {
	cfg := testConfig()
	profile := cfg.Agents.Profiles["default"]
	main := profile.Categories[CategoryDefault]
	main.ServiceTier = "standard"
	profile.Categories[CategoryDefault] = main
	cfg.Agents.Profiles["default"] = profile
	s, err := NewWithSessionDir("", cfg, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	settings := s.SettingsSnapshot()
	settings.SessionOverrides[CategoryResearcher] = types.CategorySettings{Inherit: "main", ServiceTier: "fast"}
	if err := s.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	resolved := s.RuntimeProfile().GetEffectiveConfig("root").GetCategoryConfig(CategoryResearcher)
	if resolved.ServiceTier != "fast" || resolved.Model != main.Model {
		t.Fatalf("speed override replaced main inheritance or was ignored: %+v", resolved)
	}
}

func TestSelectedRootInheritsRoleSpeedOverride(t *testing.T) {
	for _, selected := range []string{"researcher", "profile/reviewer"} {
		t.Run(selected, func(t *testing.T) {
			cfg := testConfig()
			profile := cfg.Agents.Profiles["default"]
			main := profile.Categories[CategoryDefault]
			main.ServiceTier = "standard"
			profile.Categories[CategoryDefault] = main
			cfg.Agents.Profiles["default"] = profile
			cfg.Agents.AgentProfiles["reviewer"] = AgentProfileConfig{Category: CategoryResearcher}
			s, err := NewWithSessionDir("", cfg, t.TempDir(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			settings := s.SettingsSnapshot()
			settings.SessionMainAgent = selected
			settings.SessionOverrides[CategoryResearcher] = types.CategorySettings{Inherit: "main", ServiceTier: "fast"}
			if err := s.HandleSettingsSave(settings); err != nil {
				t.Fatal(err)
			}
			current := s.RuntimeProfile().GetEffectiveConfig("root")
			root := current.GetActiveDefaultCategory()
			provider, err := current.providerForCategory(root)
			if err != nil {
				t.Fatal(err)
			}
			if root.Model != main.Model || provider.ProtocolSettings().ServiceTier != "fast" ||
				s.SettingsSnapshot().FastMode != types.FastModeOn {
				t.Fatalf("selected %s lost its role's tier: root=%+v tier=%q status=%q",
					selected, root, provider.ProtocolSettings().ServiceTier, s.SettingsSnapshot().FastMode)
			}
		})
	}
}

func TestRunningSessionTracksNamedAgentCatalogChanges(t *testing.T) {
	cfg := testConfig()
	s, err := NewWithSessionDir("", cfg, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	app := types.AppSettings{Accounts: cfg.Accounts, Agents: s.SettingsSnapshots().Base.Agents}
	app.Agents.AgentProfiles = map[string]types.AgentProfileSettings{
		"manual": {Category: CategoryResearcher, Model: "named-model", ServiceTier: "fast"},
	}
	s.UpdateWorkspaceDefaults(app)
	if got := s.SettingsSnapshots().Effective.Agents.AgentProfiles["manual"]; got.Model != "named-model" || got.ServiceTier != "fast" {
		t.Fatalf("existing session did not receive the added agent: %+v", got)
	}
	settings := s.SettingsSnapshot()
	settings.SessionOverrides["profile/manual"] = types.CategorySettings{ServiceTier: "standard"}
	if err := s.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	app.Agents.AgentProfiles["manual"] = types.AgentProfileSettings{Category: CategoryResearcher, Model: "updated-model", ServiceTier: "fast"}
	s.UpdateWorkspaceDefaults(app)
	got := s.SettingsSnapshots().Effective.Agents.AgentProfiles["manual"]
	if got.Model != "updated-model" || got.ServiceTier != "standard" {
		t.Fatalf("updated catalog lost the explicit session speed or model: %+v", got)
	}
	app.Agents.AgentProfiles = nil
	s.UpdateWorkspaceDefaults(app)
	if _, exists := s.SettingsSnapshots().Effective.Agents.AgentProfiles["manual"]; exists {
		t.Fatal("removed final named agent remained in the running session's catalog")
	}
}

func TestMemoryAgentSpeedAlonePreservesItsInheritedModel(t *testing.T) {
	cfg := testConfig()
	cfg.Memory.MemoryCategory.ServiceTier = "fast"
	memory := cfg.GetMemoryCategory()
	if memory.ServiceTier != "fast" || memory.Model != cfg.GetActiveDefaultCategory().Model || memory.AccountID != cfg.GetActiveDefaultCategory().AccountID {
		t.Fatalf("memory speed-only setting lost its inheritance: %+v", memory)
	}
	p, err := cfg.providerForCategory(memory)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.ProtocolSettings().ServiceTier; got != "fast" {
		t.Fatalf("memory request tier = %q, want fast", got)
	}
	s, err := NewWithSessionDir("", testConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	settings := s.SettingsSnapshot()
	settings.SessionOverrides[CategoryMemory] = types.CategorySettings{ServiceTier: "fast"}
	if err := s.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	current := s.RuntimeProfile().GetEffectiveConfig("root")
	memory = current.GetMemoryCategory()
	if memory.ServiceTier != "fast" || memory.Model != current.GetActiveDefaultCategory().Model || memory.AccountID != current.GetActiveDefaultCategory().AccountID {
		t.Fatalf("memory session override lost its inherited connection: %+v", memory)
	}
}
