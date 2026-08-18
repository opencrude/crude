package app

import (
	"encoding/json"
	"os"
	"path/filepath"

	"crude/buildinfo"
	"crude/session"
	ttypes "crude/tui/types"
)

type FileConfig struct {
	ActiveProvider  string                            `json:"active_provider"`
	Providers       map[string]session.ProviderConfig `json:"providers"`
	TurnLogsEnabled *bool                             `json:"turn_logs_enabled,omitempty"`
	Agents          *session.AgentsConfig             `json:"agents,omitempty"`
	Theme           *ttypes.ThemeSettings             `json:"theme,omitempty"`
	Editor          *ttypes.EditorSettings            `json:"editor,omitempty"`
	LSP             *ttypes.LSPSettings               `json:"lsp,omitempty"`
	UI              *ttypes.UISettings                `json:"ui,omitempty"`
	Font            *ttypes.FontSettings              `json:"font,omitempty"`
	Memory          *ttypes.MemorySettings            `json:"memory,omitempty"`
}

func (c *FileConfig) IsTurnLogsEnabled() bool {
	if c.TurnLogsEnabled == nil {
		return true
	}
	return *c.TurnLogsEnabled
}

func DefaultFileConfig() FileConfig {
	return FileConfig{
		ActiveProvider: "openrouter",
		Providers: map[string]session.ProviderConfig{
			"openrouter": {},
		},
	}
}

func ConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "crude_config.json"
	}
	return filepath.Join(home, buildinfo.DataDir(), "config.json")
}

func HistoryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "crude_history.json"
	}
	return filepath.Join(home, buildinfo.DataDir(), "history.json")
}

func loadFileConfigFrom(path string) (FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, err
	}
	cfg := DefaultFileConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, err
	}
	session.StripStoredAgentAPIKeys(cfg.Agents)
	return cfg, nil
}

func LoadFileConfig() FileConfig {
	cfg, err := loadFileConfigFrom(ConfigPath())
	if err != nil {
		return DefaultFileConfig()
	}
	return cfg
}

func SaveFileConfig(cfg FileConfig) error {
	session.StripStoredAgentAPIKeys(cfg.Agents)
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func LoadHistory() []string {
	var history []string
	data, err := os.ReadFile(HistoryPath())
	if err != nil {
		return history
	}
	_ = json.Unmarshal(data, &history)
	return history
}

func SaveHistory(history []string) error {
	path := HistoryPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func FileConfigToAppSettings(fc FileConfig) ttypes.AppSettings {
	s := ttypes.AppSettings{ActiveProvider: fc.ActiveProvider}
	if fc.Providers != nil {
		s.Providers = make(map[string]ttypes.ProviderConfig, len(fc.Providers))
		for id, p := range fc.Providers {
			s.Providers[id] = ttypes.ProviderConfig{
				APIKey: p.APIKey, ProviderURL: p.ProviderURL,
				APIStandard: p.APIStandard, Model: p.Model,
				ModelStandards: cloneStringMap(p.ModelStandards),
				ThinkingEffort: p.ThinkingEffort,
			}
		}
	}
	if fc.Theme != nil {
		s.Theme = *fc.Theme
	}
	if fc.Editor != nil {
		s.Editor = *fc.Editor
	}
	if fc.LSP != nil {
		s.LSP = *fc.LSP
	}
	if fc.UI != nil {
		s.UI = *fc.UI
	}
	if fc.Font != nil {
		s.Font = *fc.Font
	}
	if fc.Agents != nil {
		s.Agents = session.ConfigToSettings(&session.Config{Agents: *fc.Agents}).Agents
	}
	if fc.Memory != nil {
		s.Memory = *fc.Memory
	}
	ttypes.ApplyAppDefaults(&s)
	return s
}

func ApplyAppSettingsToFileConfig(fc *FileConfig, s ttypes.AppSettings) {
	if fc == nil {
		return
	}
	theme, editor, lsp, ui, font, memorySettings := s.Theme, s.Editor, s.LSP, s.UI, s.Font, s.Memory
	fc.Theme = &theme
	fc.Editor = &editor
	fc.LSP = &lsp
	fc.UI = &ui
	fc.Font = &font
	fc.Memory = &memorySettings
	fc.ActiveProvider = s.ActiveProvider
	if s.Providers != nil {
		if fc.Providers == nil {
			fc.Providers = make(map[string]session.ProviderConfig, len(s.Providers))
		}
		for id, p := range s.Providers {
			prev := fc.Providers[id]
			if p.APIKey != "" {
				prev.APIKey = p.APIKey
			}
			if p.ProviderURL != "" {
				prev.ProviderURL = p.ProviderURL
			}
			if p.APIStandard != "" {
				prev.APIStandard = p.APIStandard
			}
			if p.Model != "" {
				prev.Model = p.Model
			}
			if p.ModelStandards != nil {
				prev.ModelStandards = cloneStringMap(p.ModelStandards)
			}
			if p.ThinkingEffort != "" {
				prev.ThinkingEffort = p.ThinkingEffort
			}
			fc.Providers[id] = prev
		}
	}
}

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func FileConfigToSession(fc FileConfig) *session.Config {
	cfg := &session.Config{
		ActiveProvider:  fc.ActiveProvider,
		Providers:       fc.Providers,
		TurnLogsEnabled: fc.IsTurnLogsEnabled(),
	}
	if fc.Agents != nil {
		cfg.Agents = *fc.Agents
		session.StripStoredAgentAPIKeys(&cfg.Agents)
		session.EnsureAgentsDefaults(&cfg.Agents)
	} else {
		cfg.Agents = session.DefaultAgentsConfig()
	}
	if fc.Memory != nil {
		cfg.Memory = *fc.Memory
	}
	return cfg
}

func SessionToFileConfig(sc *session.Config) FileConfig {
	// Start from existing file config so visual prefs are preserved.
	fc := LoadFileConfig()
	tl := sc.TurnLogsEnabled
	fc.ActiveProvider = sc.ActiveProvider
	fc.Providers = sc.Providers
	fc.TurnLogsEnabled = &tl
	fc.Agents = &sc.Agents
	session.StripStoredAgentAPIKeys(fc.Agents)
	return fc
}
