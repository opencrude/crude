package session

import (
	"path/filepath"
	"testing"

	"crude/agent/provider"
	"crude/agent/turnloop"
	"crude/types/setting"
)

func TestNamedAgentSpeedFollowsProfileAndSessionOverrides(t *testing.T) {
	home := t.TempDir()
	cfg := testConfig()
	profile := cfg.Agents.Profiles["default"]
	profile.Categories[CategoryResearcher] = CategoryConfig{AccountID: "account", Model: "role", ServiceTier: "standard"}
	cfg.Agents.Profiles["default"] = profile
	cfg.Agents.AgentProfiles["reviewer"] = setting.AgentProfile{
		Category: CategoryResearcher, Model: "reviewer", ServiceTier: "fast",
	}
	cfg.Agents.AgentProfiles["other"] = setting.AgentProfile{Category: CategoryResearcher, Model: "other"}

	s, err := NewWithSessionDir("", cfg, home, home)
	if err != nil {
		t.Fatal(err)
	}
	assertTier := func(s *Session, want string, wantFast setting.FastMode) {
		t.Helper()
		current := s.RuntimeProfile().EffectiveConfig("root")
		root := current.ActiveDefaultCategory()
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
		child := turnloop.SubAgentSpec{}
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
	assertTier(s, "fast", setting.FastModeOn)

	settings = s.SettingsSnapshot()
	settings.SessionOverrides["profile/reviewer"] = setting.Category{ServiceTier: "standard"}
	if err := s.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	assertTier(s, "default", setting.FastModeOff)
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
	assertTier(resumed, "default", setting.FastModeOff)

	settings = resumed.SettingsSnapshot()
	delete(settings.SessionOverrides, "profile/reviewer")
	if err := resumed.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	assertTier(resumed, "fast", setting.FastModeOn)

	settings = resumed.SettingsSnapshot()
	settings.SessionOverrides["profile/reviewer"] = setting.Category{AccountID: "missing"}
	if err := resumed.HandleSettingsSave(settings); err == nil {
		t.Fatal("named-agent edit accepted an unavailable account")
	}
	assertTier(resumed, "fast", setting.FastModeOn)
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
	settings.SessionOverrides[CategoryResearcher] = setting.Category{Inherit: "main", ServiceTier: "fast"}
	if err := s.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	resolved := s.RuntimeProfile().EffectiveConfig("root").CategoryConfig(CategoryResearcher)
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
			cfg.Agents.AgentProfiles["reviewer"] = setting.AgentProfile{Category: CategoryResearcher}
			s, err := NewWithSessionDir("", cfg, t.TempDir(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			settings := s.SettingsSnapshot()
			settings.SessionMainAgent = selected
			settings.SessionOverrides[CategoryResearcher] = setting.Category{Inherit: "main", ServiceTier: "fast"}
			if err := s.HandleSettingsSave(settings); err != nil {
				t.Fatal(err)
			}
			current := s.RuntimeProfile().EffectiveConfig("root")
			root := current.ActiveDefaultCategory()
			provider, err := current.providerForCategory(root)
			if err != nil {
				t.Fatal(err)
			}
			if root.Model != main.Model || provider.ProtocolSettings().ServiceTier != "fast" ||
				s.SettingsSnapshot().FastMode != setting.FastModeOn {
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
	app := setting.App{Accounts: cfg.Accounts, Agents: s.SettingsSnapshots().Base.Agents}
	app.Agents.AgentProfiles = map[string]setting.AgentProfile{
		"manual": {Category: CategoryResearcher, Model: "named-model", ServiceTier: "fast"},
	}
	s.UpdateWorkspaceDefaults(app)
	if got := s.SettingsSnapshots().Effective.Agents.AgentProfiles["manual"]; got.Model != "named-model" || got.ServiceTier != "fast" {
		t.Fatalf("existing session did not receive the added agent: %+v", got)
	}
	settings := s.SettingsSnapshot()
	settings.SessionOverrides["profile/manual"] = setting.Category{ServiceTier: "standard"}
	if err := s.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	app.Agents.AgentProfiles["manual"] = setting.AgentProfile{Category: CategoryResearcher, Model: "updated-model", ServiceTier: "fast"}
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
	memory := cfg.MemoryCategory()
	if memory.ServiceTier != "fast" || memory.Model != cfg.ActiveDefaultCategory().Model || memory.AccountID != cfg.ActiveDefaultCategory().AccountID {
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
	settings.SessionOverrides[CategoryMemory] = setting.Category{ServiceTier: "fast"}
	if err := s.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	current := s.RuntimeProfile().EffectiveConfig("root")
	memory = current.MemoryCategory()
	if memory.ServiceTier != "fast" || memory.Model != current.ActiveDefaultCategory().Model || memory.AccountID != current.ActiveDefaultCategory().AccountID {
		t.Fatalf("memory session override lost its inherited connection: %+v", memory)
	}
}
