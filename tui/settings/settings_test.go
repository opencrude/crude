package settings

import (
	"testing"

	"crude/tui/theme"
	"crude/tui/types"
)

func TestNewModelWithExtendedSettings(t *testing.T) {
	th := theme.DefaultDark()
	initial := types.Settings{
		APIKey:          "test-key",
		Model:           "gpt-4",
		ProviderID:      "openai",
		TurnLogsEnabled: "on",
		Agents: types.AgentsSettings{
			ActiveProfile: "test-profile",
			Categories: map[string]types.CategorySettings{
				"default": {Model: "gpt-4", Provider: "openai"},
			},
			Profiles: []types.ProfileSettings{
				{Name: "test-profile", Categories: map[string]types.CategorySettings{
					"default": {Model: "gpt-4", Provider: "openai"},
				}},
			},
		},
		Theme: types.ThemeSettings{
			Preset:    "dark",
			Bg:        "#000000",
			Fg:        "#ffffff",
			Accent:    "#ff6600",
			SubPaneBg: "#111111",
		},
		Editor: types.EditorSettings{
			TabSize:     8,
			WordWrap:    true,
			LineNumbers: true,
			SyntaxTheme: "monokai",
		},
		LSP: types.LSPSettings{
			Enabled: true,
			AutoDetect: true,
			Diagnostics: true,
		},
		UI: types.UISettings{
			SidebarWidth: 40,
			ShowHiddenFiles: true,
			ShowIcons: true,
			UseNerdFont: true,
		},
		Font: types.FontSettings{
			Family:    "Fira Code",
			Size:      14,
			Ligatures: true,
		},
	}

	m := NewModel(th, initial, nil, nil, nil)

	// Verify agents settings were loaded.
	if m.agentsSettings.ActiveProfile != "test-profile" {
		t.Errorf("expected ActiveProfile 'test-profile', got %q", m.agentsSettings.ActiveProfile)
	}

	// Verify theme settings were loaded.
	if m.themeSettings.Preset != "dark" {
		t.Errorf("expected Theme.Preset 'dark', got %q", m.themeSettings.Preset)
	}
	if m.themeSettings.Bg != "#000000" {
		t.Errorf("expected Theme.Bg '#000000', got %q", m.themeSettings.Bg)
	}

	// Verify editor settings.
	if m.editorSettings.TabSize != 8 {
		t.Errorf("expected Editor.TabSize 8, got %d", m.editorSettings.TabSize)
	}
	if m.editorSettings.SyntaxTheme != "monokai" {
		t.Errorf("expected Editor.SyntaxTheme 'monokai', got %q", m.editorSettings.SyntaxTheme)
	}

	// Verify LSP settings.
	if !m.lspSettings.Enabled {
		t.Error("expected LSP.Enabled true")
	}

	// Verify UI settings.
	if m.uiSettings.SidebarWidth != 40 {
		t.Errorf("expected UI.SidebarWidth 40, got %d", m.uiSettings.SidebarWidth)
	}

	// Verify font settings.
	if m.fontSettings.Family != "Fira Code" {
		t.Errorf("expected Font.Family 'Fira Code', got %q", m.fontSettings.Family)
	}
	if m.fontSettings.Size != 14 {
		t.Errorf("expected Font.Size 14, got %d", m.fontSettings.Size)
	}
}

func TestToSettingsIncludesExtended(t *testing.T) {
	th := theme.DefaultDark()
	initial := types.Settings{
		APIKey:          "test-key",
		Model:           "gpt-4",
		ProviderID:      "openai",
		TurnLogsEnabled: "on",
		Agents: types.AgentsSettings{
			ActiveProfile: "default",
			Categories: map[string]types.CategorySettings{
				"default": {Model: "gpt-4", Provider: "openai"},
			},
		},
	}

	m := NewModel(th, initial, nil, nil, nil)

	// Set extended settings.
	m.themeSettings = types.ThemeSettings{
		Preset: "light",
		Bg:     "#ffffff",
		Fg:     "#000000",
	}
	m.editorSettings = types.EditorSettings{TabSize: 4, SyntaxTheme: "dracula"}
	m.lspSettings = types.LSPSettings{Enabled: false}
	m.uiSettings = types.UISettings{SidebarWidth: 50, ShowIcons: false}
	m.fontSettings = types.FontSettings{Family: "JetBrains Mono", Size: 13}

	s := m.toSettings()

	// Verify they come back in the result.
	if s.Theme.Preset != "light" {
		t.Errorf("expected Theme.Preset 'light', got %q", s.Theme.Preset)
	}
	if s.Editor.TabSize != 4 {
		t.Errorf("expected Editor.TabSize 4, got %d", s.Editor.TabSize)
	}
	if s.LSP.Enabled {
		t.Error("expected LSP.Enabled false")
	}
	if s.UI.SidebarWidth != 50 {
		t.Errorf("expected UI.SidebarWidth 50, got %d", s.UI.SidebarWidth)
	}
	if s.Font.Family != "JetBrains Mono" {
		t.Errorf("expected Font.Family 'JetBrains Mono', got %q", s.Font.Family)
	}
}

func TestSaveSnapshotRestoreExtended(t *testing.T) {
	th := theme.DefaultDark()
	initial := types.Settings{
		APIKey:          "test-key",
		Model:           "gpt-4",
		ProviderID:      "openai",
		TurnLogsEnabled: "on",
		Agents: types.AgentsSettings{
			ActiveProfile: "default",
			Categories: map[string]types.CategorySettings{
				"default": {Model: "gpt-4", Provider: "openai"},
			},
		},
	}

	m := NewModel(th, initial, nil, nil, nil)

	// Set some values.
	m.themeSettings.Preset = "custom"
	m.themeSettings.Bg = "#123456"
	m.editorSettings.TabSize = 6

	// Take snapshot.
	m.SaveSnapshot()

	// Change values again.
	m.themeSettings.Preset = "dark"
	m.themeSettings.Bg = "#000000"
	m.editorSettings.TabSize = 2

	// Restore snapshot.
	m.RestoreSnapshot()

	// Verify restored.
	if m.themeSettings.Preset != "custom" {
		t.Errorf("expected Preset 'custom' after restore, got %q", m.themeSettings.Preset)
	}
	if m.themeSettings.Bg != "#123456" {
		t.Errorf("expected Bg '#123456' after restore, got %q", m.themeSettings.Bg)
	}
	if m.editorSettings.TabSize != 6 {
		t.Errorf("expected TabSize 6 after restore, got %d", m.editorSettings.TabSize)
	}
}

func TestThemeFormBuildAndSave(t *testing.T) {
	th := theme.DefaultDark()
	cfg := types.ThemeSettings{
		Preset:    "dark",
		Bg:        "#0d1117",
		Fg:        "#e6edf3",
		Accent:    "#f0883e",
		Accent2:   "#a5d6ff",
		SubPaneBg: "#1c2333",
		Border:    "#30363d",
	}

	form := buildThemeForm(th, cfg)
	if form.ItemCount() != 7 {
		t.Errorf("expected 7 items in theme form, got %d", form.ItemCount())
	}
}

func TestEditorFormBuildAndSave(t *testing.T) {
	th := theme.DefaultDark()
	cfg := types.EditorSettings{
		TabSize:      4,
		WordWrap:     true,
		LineNumbers:  true,
		SyntaxTheme:  "catppuccin",
		RenderWhitespace: "none",
		AutoSaveDelay: 2000,
	}

	form := buildEditorForm(th, cfg)
	_ = form
}

func TestLSPFormBuildAndSave(t *testing.T) {
	th := theme.DefaultDark()
	cfg := types.LSPSettings{
		Enabled:     true,
		AutoDetect:  true,
		Diagnostics: true,
	}

	form := buildLSPForm(th, cfg)
	_ = form
}

func TestUIFormBuildAndSave(t *testing.T) {
	th := theme.DefaultDark()
	cfg := types.UISettings{
		SidebarWidth:    30,
		ShowHiddenFiles: false,
		ShowIcons:       true,
		UseNerdFont:     true,
		ShowGitStatus:   true,
		ShowDiagnostics: true,
	}

	form := buildUIForm(th, cfg)
	_ = form
}

func TestFontFormBuildAndSave(t *testing.T) {
	th := theme.DefaultDark()
	cfg := types.FontSettings{
		Family:    "Fira Code",
		Size:      12,
		Ligatures: true,
	}

	form := buildFontForm(th, cfg)
	_ = form
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		input       string
		defaultVal  int
		expected    int
	}{
		{"42", 0, 42},
		{"0", 10, 0},
		{"", 5, 5},
		{"not-a-number", 3, 3},
		{"-1", 10, -1},
	}

	for _, tt := range tests {
		result := parseInt(tt.input, tt.defaultVal)
		if result != tt.expected {
			t.Errorf("parseInt(%q, %d) = %d, want %d", tt.input, tt.defaultVal, result, tt.expected)
		}
	}
}
