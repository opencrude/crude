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

func LoadFileConfig() FileConfig {
	cfg := DefaultFileConfig()
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg
	}
	return cfg
}

func SaveFileConfig(cfg FileConfig) error {
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

func FileConfigToSession(fc FileConfig) *session.Config {
	cfg := &session.Config{
		ActiveProvider:  fc.ActiveProvider,
		Providers:       fc.Providers,
		TurnLogsEnabled: fc.IsTurnLogsEnabled(),
	}
	if fc.Agents != nil {
		cfg.Agents = *fc.Agents
	}
	return cfg
}

func SessionToFileConfig(sc *session.Config) FileConfig {
	tl := sc.TurnLogsEnabled
	fc := FileConfig{
		ActiveProvider:  sc.ActiveProvider,
		Providers:       sc.Providers,
		TurnLogsEnabled: &tl,
	}
	fc.Agents = &sc.Agents
	return fc
}
