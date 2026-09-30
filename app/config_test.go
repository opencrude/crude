package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"crude/history"
	"crude/provider"
	"crude/session"
	ttypes "crude/tui/types"
	typesmodel "crude/types"
)

// setTestHome redirects the home directory to a temp dir for the duration of
// a test. Sets both HOME (Unix) and USERPROFILE (Windows) because
// os.UserHomeDir() reads USERPROFILE on Windows and HOME on Unix — setting
// only HOME leaves the real user's home reachable on Windows, which breaks
// tests that assert "config should not exist yet" or "should not invent
// Theme from disk". Returns a restore function.
func setTestHome(t *testing.T, tmpDir string) func() {
	t.Helper()
	origHome := os.Getenv("HOME")
	origProfile := os.Getenv("USERPROFILE")
	_ = os.Setenv("HOME", tmpDir)
	_ = os.Setenv("USERPROFILE", tmpDir)
	return func() {
		_ = os.Setenv("HOME", origHome)
		_ = os.Setenv("USERPROFILE", origProfile)
	}
}

func TestFileConfigRoundTrip(t *testing.T) {
	// Create a temporary directory for config.
	tmpDir := t.TempDir()
	restore := setTestHome(t, tmpDir)
	defer restore()

	// Build a FileConfig with all extended settings.
	providerHeaders := map[string]string{"X-Tenant": "alpha"}
	accountHeaders := map[string]string{"X-Tenant": "work"}
	accountEndpoint := "https://gateway.example/v1"
	fc := FileConfig{
		ActiveProvider: "openrouter",
		Providers: map[string]provider.Template{
			"openrouter": {
				Name: "OpenRouter", ProviderURL: "https://openrouter.ai/api/v1", APIStandard: "openai",
				AuthType: provider.AuthProtocol, Headers: providerHeaders,
				Dialer: &provider.DialSettings{
					MaxAttempts: 3, RequestTimeoutSeconds: 600, ConnectTimeoutSeconds: 60, MaxBackoffSeconds: 60,
					ProgressStartupTimeoutSeconds: 120, ProgressTimeoutFloorSeconds: 60, ProgressTimeoutCeilingSeconds: 600,
				},
				Protocol:  &provider.ProtocolSettings{Store: boolPtr(false)},
				ExtraBody: map[string]any{"include_reasoning": true},
			},
		},
		Accounts: map[string]provider.Account{
			"work": {
				ID: "work", ProviderID: "openrouter", APIKey: "sk-test",
				Overrides: provider.TemplateOverrides{
					ProviderURL: &accountEndpoint, Headers: &accountHeaders,
					ExtraBody: &map[string]any{},
					Dialer: &provider.DialSettings{
						RequestTimeoutSeconds: 300, ConnectTimeoutSeconds: 10, MaxAttempts: 2, MaxBackoffSeconds: 15,
						ProgressStartupTimeoutSeconds: 90, ProgressTimeoutFloorSeconds: 60, ProgressTimeoutCeilingSeconds: 900,
					},
				},
			},
		},
		TurnLogsEnabled: boolPtr(true),
		Agents:          &session.AgentsConfig{Enabled: true, ActiveProfile: "default"},
		Theme: &ttypes.ThemeSettings{
			Preset:    "dark",
			Bg:        "#0d1117",
			Fg:        "#e6edf3",
			Accent:    "#f0883e",
			SubPaneBg: "#1c2333",
		},
		Editor: &ttypes.EditorSettings{
			TabSize:       4,
			WordWrap:      true,
			LineNumbers:   true,
			SyntaxTheme:   "catppuccin",
			AutoSaveDelay: 1000,
		},
		LSP: &ttypes.LSPSettings{
			Enabled:     true,
			AutoDetect:  true,
			Diagnostics: true,
		},
		UI: &ttypes.UISettings{
			SidebarWidth:    30,
			ShowHiddenFiles: false,
			ShowIcons:       true,
			UseNerdFont:     true,
			ShowGitStatus:   true,
			ShowDiagnostics: true,
		},
		Font: &ttypes.FontSettings{
			Family:    "Fira Code",
			Size:      12,
			Ligatures: true,
		},
	}

	// Save
	if err := SaveFileConfig(fc); err != nil {
		t.Fatalf("SaveFileConfig failed: %v", err)
	}

	// Load back
	loaded := LoadFileConfig()

	// Verify loaded values match saved values.
	if loaded.ActiveProvider != fc.ActiveProvider {
		t.Errorf("ActiveProvider: expected %q, got %q", fc.ActiveProvider, loaded.ActiveProvider)
	}
	if loaded.Accounts["work"].APIKey != fc.Accounts["work"].APIKey {
		t.Errorf("account mismatch")
	}
	if !reflect.DeepEqual(loaded.Providers["openrouter"], fc.Providers["openrouter"]) {
		t.Fatalf("provider template mismatch: got %#v, want %#v", loaded.Providers["openrouter"], fc.Providers["openrouter"])
	}
	overrides := loaded.Accounts["work"].Overrides
	if overrides.ProviderURL == nil || *overrides.ProviderURL != accountEndpoint || overrides.Headers == nil ||
		!reflect.DeepEqual(*overrides.Headers, accountHeaders) || overrides.ExtraBody == nil || len(*overrides.ExtraBody) != 0 ||
		!reflect.DeepEqual(overrides.Dialer, fc.Accounts["work"].Overrides.Dialer) {
		t.Fatalf("account sparse overrides mismatch: %#v", overrides)
	}

	// Verify Theme settings.
	if loaded.Theme == nil {
		t.Fatal("Theme is nil after load")
	}
	if loaded.Theme.Preset != "dark" {
		t.Errorf("Theme.Preset: expected 'dark', got %q", loaded.Theme.Preset)
	}
	if loaded.Theme.Bg != "#0d1117" {
		t.Errorf("Theme.Bg: expected '#0d1117', got %q", loaded.Theme.Bg)
	}

	// Verify Editor settings.
	if loaded.Editor == nil {
		t.Fatal("Editor is nil after load")
	}
	if loaded.Editor.TabSize != 4 {
		t.Errorf("Editor.TabSize: expected 4, got %d", loaded.Editor.TabSize)
	}
	if loaded.Editor.SyntaxTheme != "catppuccin" {
		t.Errorf("Editor.SyntaxTheme: expected 'catppuccin', got %q", loaded.Editor.SyntaxTheme)
	}

	// Verify LSP settings.
	if loaded.LSP == nil {
		t.Fatal("LSP is nil after load")
	}
	if !loaded.LSP.Enabled {
		t.Error("LSP.Enabled: expected true")
	}

	// Verify UI settings.
	if loaded.UI == nil {
		t.Fatal("UI is nil after load")
	}
	if loaded.UI.SidebarWidth != 30 {
		t.Errorf("UI.SidebarWidth: expected 30, got %d", loaded.UI.SidebarWidth)
	}

	// Verify Font settings.
	if loaded.Font == nil {
		t.Fatal("Font is nil after load")
	}
	if loaded.Font.Family != "Fira Code" {
		t.Errorf("Font.Family: expected 'Fira Code', got %q", loaded.Font.Family)
	}
	if loaded.Font.Size != 12 {
		t.Errorf("Font.Size: expected 12, got %d", loaded.Font.Size)
	}
}

func TestDefaultFileConfigHasNoExtendedSettings(t *testing.T) {
	fc := DefaultFileConfig()
	if len(fc.Providers) == 0 || fc.Providers["openrouter"].Name != "OpenRouter" {
		t.Fatalf("default config is missing seeded provider templates: %#v", fc.Providers)
	}
	if fc.Theme != nil {
		t.Error("Default FileConfig should have nil Theme")
	}
	if fc.Editor != nil {
		t.Error("Default FileConfig should have nil Editor")
	}
	if fc.LSP != nil {
		t.Error("Default FileConfig should have nil LSP")
	}
	if fc.UI != nil {
		t.Error("Default FileConfig should have nil UI")
	}
	if fc.Font != nil {
		t.Error("Default FileConfig should have nil Font")
	}
	fc.Providers["openrouter"] = provider.Template{}
	if DefaultFileConfig().Providers["openrouter"].Name != "OpenRouter" {
		t.Fatal("default provider templates share mutable state")
	}
}

func TestApplyAppSettingsPersistsMultiAgentOff(t *testing.T) {
	agents := session.DefaultAgentsConfig()
	fc := FileConfig{Agents: &agents}
	settings := FileConfigToAppSettings(fc)
	settings.Agents.Enabled = false

	ApplyAppSettingsToFileConfig(&fc, settings)
	if fc.Agents == nil {
		t.Fatal("agents config was dropped")
	}
	cfg := &session.Config{Agents: *fc.Agents}
	if cfg.IsMultiAgentEnabled() || fc.Agents.Enabled {
		t.Fatalf("multi-agent off was not persisted: %#v", fc.Agents)
	}
}

func TestSafetySettingsRoundTripThroughFileConfig(t *testing.T) {
	settings := ttypes.AppSettings{Safety: ttypes.SafetySettings{
		ConfirmAt: "network", UnknownCommandRisk: "project-write", ScriptExecutionRisk: "safe",
		NotifyDangerous: "off", CommandRules: []ttypes.CommandSeverityRule{{Pattern: "scripts/*.sh", Risk: "safe"}},
		AutoPermission: typesmodel.AutoPermissionSettings{
			Enabled: "on", Provider: "openai", Model: "gpt-5-mini", DenyMode: "deny",
			Instructions: "Never approve deployment commands.", TimeoutSeconds: 11,
			MaxOutputTokens: 80, MaxInputBytes: 2048, MaxReviewsPerTurn: 7,
		},
	}}
	fc := DefaultFileConfig()
	ApplyAppSettingsToFileConfig(&fc, settings)
	got := FileConfigToAppSettings(fc).Safety
	if got.ConfirmAt != "network" || got.UnknownCommandRisk != "project-write" || got.ScriptExecutionRisk != "safe" ||
		got.NotifyDangerous != "off" || len(got.CommandRules) != 1 || got.CommandRules[0].Pattern != "scripts/*.sh" {
		t.Fatalf("safety round-trip = %#v", got)
	}
	auto := got.AutoPermission
	if auto.Enabled != "on" || auto.Provider != "openai" || auto.Model != "gpt-5-mini" || auto.DenyMode != "deny" ||
		auto.Instructions != "Never approve deployment commands." || auto.TimeoutSeconds != 11 ||
		auto.MaxOutputTokens != 80 || auto.MaxInputBytes != 2048 || auto.MaxReviewsPerTurn != 7 {
		t.Fatalf("auto permission round-trip = %#v", auto)
	}
}

func TestFileConfigLoadsMissingFileGracefully(t *testing.T) {
	tmpDir := t.TempDir()
	restore := setTestHome(t, tmpDir)
	defer restore()

	// Config path should not exist.
	cfgPath := ConfigPath()
	if _, err := os.Stat(cfgPath); err == nil {
		t.Fatal("config should not exist yet")
	}

	// Loading a missing config should return defaults.
	fc := LoadFileConfig()
	if fc.ActiveProvider != "openrouter" {
		t.Errorf("expected default provider 'openrouter', got %q", fc.ActiveProvider)
	}
}

func TestSessionToFileConfigPreservesNewSettings(t *testing.T) {
	tmpDir := t.TempDir()
	restore := setTestHome(t, tmpDir)
	defer restore()

	sc := &session.Config{
		ActiveProvider:  "openai",
		TurnLogsEnabled: true,
		Agents:          session.DefaultAgentsConfig(),
	}

	fc := SessionToFileConfig(sc)
	if fc.ActiveProvider != "openai" {
		t.Errorf("expected 'openai', got %q", fc.ActiveProvider)
	}
	if fc.Theme != nil {
		t.Error("SessionToFileConfig should not invent Theme when none on disk")
	}
}

func boolPtr(b bool) *bool { return &b }

func TestSaveFileConfigOmitsAgentProfileAPIKeys(t *testing.T) {
	tmpDir := t.TempDir()
	restore := setTestHome(t, tmpDir)
	defer restore()

	fc := FileConfig{
		ActiveProvider: "opencode-go",
		Providers: map[string]provider.Template{
			"opencode-go": {Name: "OpenCode Go", ProviderURL: "https://opencode.ai/zen/go/v1", APIStandard: "openai", AuthType: provider.AuthProtocol},
		},
		Accounts: map[string]provider.Account{"go-account": {ID: "go-account", ProviderID: "opencode-go", APIKey: "provider-key"}},
		Agents: &session.AgentsConfig{
			ActiveProfile: "test",
			Profiles: map[string]session.ProfileConfig{
				"test": {Categories: map[string]session.CategoryConfig{
					"default": {AccountID: "go-account", Model: "deepseek-v4-flash"},
				}},
			},
		},
	}
	if err := SaveFileConfig(fc); err != nil {
		t.Fatalf("SaveFileConfig: %v", err)
	}
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), "should-not-persist") {
		t.Fatalf("agent profile API key leaked into config.json: %s", data)
	}
	if !strings.Contains(string(data), `"accounts"`) || !strings.Contains(string(data), "provider-key") {
		t.Fatal("account credential should persist in accounts only")
	}
	if strings.Contains(string(data), "should-not-persist") {
		t.Fatal("profile credential leaked")
	}
}

func TestRemovedSeedTemplateStaysRemovedAfterReload(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	cfg := DefaultFileConfig()
	if _, exists := cfg.Providers["chatgpt"]; !exists {
		t.Fatal("Codex seed missing from a new config")
	}
	delete(cfg.Providers, "chatgpt")
	if err := SaveFileConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if _, exists := LoadFileConfig().Providers["chatgpt"]; exists {
		t.Fatal("deleted built-in template was reseeded over saved user configuration")
	}
}

func TestLoadFileConfigIgnoresAgentProfileAPIKeys(t *testing.T) {
	tmpDir := t.TempDir()
	restore := setTestHome(t, tmpDir)
	defer restore()
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0700); err != nil {
		t.Fatal(err)
	}
	raw := `{
  "active_provider": "opencode-go",
  "providers": {
    "opencode-go": {"api_key": "provider-key"}
  },
  "agents": {
    "active_profile": "test",
    "profiles": {
      "test": {
        "categories": {
          "default": {
            "provider": "opencode-go",
            "api_key": "stale-profile-key",
            "model": "deepseek-v4-flash"
          }
        }
      }
    }
  }
}`
	if err := os.WriteFile(ConfigPath(), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	loaded := LoadFileConfig()
	if len(loaded.Accounts) != 0 {
		t.Fatal("legacy provider credential must not be imported automatically")
	}
	cat := loaded.Agents.Profiles["test"].Categories["default"]
	if cat.AccountID != "" || cat.Model != "deepseek-v4-flash" {
		t.Fatalf("agent profile reference lost: %#v", cat)
	}
	if sess := FileConfigToSession(loaded); len(sess.Accounts) != 0 || sess.GetActiveDefaultCategory().AccountID != "" {
		t.Fatal("legacy credential entered session configuration")
	}
}

func TestConfigPath(t *testing.T) {
	tmpDir := t.TempDir()
	restore := setTestHome(t, tmpDir)
	defer restore()

	path := ConfigPath()
	if path == "" {
		t.Fatal("ConfigPath returned empty")
	}
	if !filepath.IsAbs(path) {
		t.Errorf("ConfigPath should return absolute path, got %q", path)
	}
}

func TestHistoryPath(t *testing.T) {
	tmpDir := t.TempDir()
	restore := setTestHome(t, tmpDir)
	defer restore()

	// HistoryPath moved to the history package (history.DefaultPath).
	path := history.DefaultPath()
	if path == "" {
		t.Fatal("DefaultPath returned empty")
	}
}

func TestLocalHistorySettingsFileConfigRoundTrip(t *testing.T) {
	want := ttypes.LocalHistorySettings{Enabled: "off", UseGitIgnore: "on", ExtraIgnorePatterns: []string{"vendor/"}, MaxFileSizeMB: 50, StorageBudgetMB: 512}
	var fc FileConfig
	ApplyAppSettingsToFileConfig(&fc, ttypes.AppSettings{LocalHistory: want})
	got := FileConfigToAppSettings(fc).LocalHistory
	if got.Enabled != want.Enabled || got.MaxFileSizeMB != want.MaxFileSizeMB || len(got.ExtraIgnorePatterns) != 1 || got.StorageBudgetMB != want.StorageBudgetMB {
		t.Fatalf("local history round trip=%+v", got)
	}
}
