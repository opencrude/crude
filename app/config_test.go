package app

import (
	"os"
	"path/filepath"
	"testing"

	"crude/session"
	ttypes "crude/tui/types"
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
	fc := FileConfig{
		ActiveProvider: "openrouter",
		Providers: map[string]session.ProviderConfig{
			"openrouter": {APIKey: "sk-test", ProviderURL: "https://openrouter.ai/api/v1"},
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
			TabSize:      4,
			WordWrap:     true,
			LineNumbers:  true,
			SyntaxTheme:  "catppuccin",
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
	if loaded.Providers["openrouter"].APIKey != fc.Providers["openrouter"].APIKey {
		t.Errorf("APIKey mismatch")
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

	path := HistoryPath()
	if path == "" {
		t.Fatal("HistoryPath returned empty")
	}
}
