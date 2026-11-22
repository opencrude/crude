package session

import (
	"path/filepath"
	"testing"

	"crude/agent/provider"
	"crude/types/setting"
)

// The snapshot describes what the configured connection can request, not what
// a provider may subsequently report as the served tier.
func TestFastModeSnapshotFollowsEffectiveConnection(t *testing.T) {
	tests := []struct {
		name, protocol, accountTier, roleTier string
		want                                  setting.FastMode
		wantWireTier                          string
	}{
		{"default", "openai", "", "", setting.FastModeOff, ""},
		{"role fast", "openai", "", "fast", setting.FastModeOn, "fast"},
		{"account fast", "openai-responses", "fast", "", setting.FastModeOn, "fast"},
		{"role overrides account", "openai", "fast", "standard", setting.FastModeOff, "default"},
		{"priority is not fast", "openai", "priority", "", setting.FastModeOff, "priority"},
		{"anthropic fast mode", "anthropic", "", "fast", setting.FastModeOn, "fast"},
		{"codex unsupported", "codex", "", "fast", setting.FastModeUnsupported, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := modelAttributionConfig("https://example.invalid/v1", "m")
			account := cfg.Accounts["account"]
			account.Connection.APIStandard = test.protocol
			account.Connection.Protocol = &provider.ProtocolSettings{ServiceTier: test.accountTier}
			cfg.Accounts["account"] = account
			profile := cfg.Agents.Profiles["default"]
			main := profile.Categories[CategoryDefault]
			main.ServiceTier = test.roleTier
			profile.Categories[CategoryDefault] = main
			cfg.Agents.Profiles["default"] = profile

			if got := ConfigToSettings(cfg).FastMode; got != test.want {
				t.Fatalf("fast projection = %q, want %q", got, test.want)
			}
			p, err := cfg.providerForCategory(cfg.ActiveDefaultCategory())
			if err != nil {
				t.Fatal(err)
			}
			if got := p.ProtocolSettings().ServiceTier; got != test.wantWireTier {
				t.Fatalf("next request tier = %q, want %q", got, test.wantWireTier)
			}
		})
	}
}

func TestFastModeSnapshotUsesSelectedRootAndMissingConnection(t *testing.T) {
	cfg := modelAttributionConfig("https://example.invalid/v1", "main")
	profile := cfg.Agents.Profiles["default"]
	profile.Categories[CategoryCrafter] = CategoryConfig{AccountID: "account", Model: "other", ServiceTier: "fast"}
	cfg.Agents.Profiles["default"] = profile
	cfg.SelectedAgent = CategoryCrafter
	if got := ConfigToSettings(cfg); got.Model != "other" || got.FastMode != setting.FastModeOn {
		t.Fatalf("selected root projection = %+v", got)
	}
	account := cfg.Accounts["account"]
	account.Connection.ProviderURL = "not-an-endpoint"
	cfg.Accounts["account"] = account
	if got := ConfigToSettings(cfg).FastMode; got != setting.FastModeUnavailable {
		t.Fatalf("invalid connection was represented as fast: %q", got)
	}
	delete(cfg.Accounts, "account")
	if got := ConfigToSettings(cfg).FastMode; got != setting.FastModeUnavailable {
		t.Fatalf("missing connection was represented as fast: %q", got)
	}
}

func TestInvalidAccountFastConnectionIsUnavailable(t *testing.T) {
	cfg := modelAttributionConfig("https://example.invalid/v1", "main")
	account := cfg.Accounts["account"]
	account.Connection.Protocol = &provider.ProtocolSettings{ServiceTier: "fast"}
	account.Connection.ProviderURL = "not-an-endpoint"
	cfg.Accounts["account"] = account
	if got := ConfigToSettings(cfg).FastMode; got != setting.FastModeUnavailable {
		t.Fatalf("invalid fast account connection = %q", got)
	}
}

func TestFastModeOverrideSurvivesSessionResume(t *testing.T) {
	home := t.TempDir()
	cfg := testConfig()
	s, err := NewWithSessionDir("", cfg, home, home)
	if err != nil {
		t.Fatal(err)
	}
	settings := s.SettingsSnapshot()
	settings.SessionOverrides = map[string]setting.Category{CategoryDefault: {ServiceTier: "fast"}}
	if err := s.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	if got := s.SettingsSnapshot().FastMode; got != setting.FastModeOn {
		t.Fatalf("saved fast mode = %q", got)
	}
	id := s.SessionID()
	s.msgs = []provider.ChatMessage{provider.NewTextMessage("user", "Keep this session")}
	s.Close()
	resumed, err := ResumeSessionWithDir(filepath.Join(home, id+".db"), cfg, home, home)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if got := resumed.SettingsSnapshot(); got.FastMode != setting.FastModeOn || got.SessionOverrides[CategoryDefault].ServiceTier != "fast" {
		t.Fatalf("resumed settings lost fast mode: %+v", got)
	}
}
