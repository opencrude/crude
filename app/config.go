package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"

	"crude/buildinfo"
	"crude/provider"
	"crude/session"
	ttypes "crude/tui/types"
)

type FileConfig struct {
	ActiveProvider  string                       `json:"active_provider"`
	Providers       map[string]provider.Template `json:"providers"`
	Accounts        map[string]provider.Account  `json:"accounts,omitempty"`
	TurnLogsEnabled *bool                        `json:"turn_logs_enabled,omitempty"`
	Agents          *session.AgentsConfig        `json:"agents,omitempty"`
	Theme           *ttypes.ThemeSettings        `json:"theme,omitempty"`
	Editor          *ttypes.EditorSettings       `json:"editor,omitempty"`
	LSP             *ttypes.LSPSettings          `json:"lsp,omitempty"`
	UI              *ttypes.UISettings           `json:"ui,omitempty"`
	Font            *ttypes.FontSettings         `json:"font,omitempty"`
	Memory          *ttypes.MemorySettings       `json:"memory,omitempty"`
	Safety          *ttypes.SafetySettings       `json:"safety,omitempty"`
	LocalHistory    *ttypes.LocalHistorySettings `json:"local_history,omitempty"`
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
		Providers:      provider.DefaultTemplates(),
	}
}

func ConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "crude_config.json"
	}
	return filepath.Join(home, buildinfo.DataDir(), "config.json")
}

func loadFileConfigFrom(path string) (FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, err
	}
	cfg := DefaultFileConfig()
	// Once a config exists, its template map is authoritative. Seed templates
	// belong to a new config only; otherwise deleting a built-in template would
	// recreate it on the next read.
	cfg.Providers = make(map[string]session.ProviderConfig)
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

func LoadFileConfig() FileConfig {
	cfg, err := loadFileConfigFrom(ConfigPath())
	if err != nil {
		return DefaultFileConfig()
	}
	return cfg
}

// updateFileConfig serializes read/modify/write with other app processes and
// writes through a same-directory temporary file. The lock file is permanent:
// locking config.json itself would cease to protect writes after rename.
func updateFileConfig(update func(*FileConfig) error) (FileConfig, error) {
	return updateFileConfigWithFallback(update, false)
}

func updateFileConfigWithFallback(update func(*FileConfig) error, overwriteInvalid bool) (FileConfig, error) {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return FileConfig{}, err
	}
	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return FileConfig{}, err
	}
	defer func() { _ = lock.Unlock() }()

	cfg, err := loadFileConfigFrom(path)
	if os.IsNotExist(err) || (overwriteInvalid && err != nil) {
		cfg = DefaultFileConfig()
	} else if err != nil {
		return FileConfig{}, err
	}
	if err := update(&cfg); err != nil {
		return FileConfig{}, err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return FileConfig{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return FileConfig{}, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := tmp.Chmod(0600); err != nil {
		return FileConfig{}, err
	}
	if _, err := tmp.Write(data); err != nil {
		return FileConfig{}, err
	}
	if err := tmp.Sync(); err != nil {
		return FileConfig{}, err
	}
	if err := tmp.Close(); err != nil {
		return FileConfig{}, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

func SaveFileConfig(cfg FileConfig) error {
	_, err := updateFileConfigWithFallback(func(current *FileConfig) error {
		*current = cfg
		return nil
	}, true)
	return err
}

// persistRotatedAccount writes only a refreshed credential, never a stale
// full-config snapshot. A removed or reconnected account cannot be revived.
func persistRotatedAccount(account provider.Account, expected provider.Account) error {
	_, err := updateFileConfig(func(cfg *FileConfig) error {
		current, ok := cfg.Accounts[account.ID]
		if !ok || !sameAccountCredentials(current, expected) || current.ProviderID != account.ProviderID {
			return fmt.Errorf("account %q was removed or reconnected", account.ID)
		}
		account.Label = current.Label
		cfg.Accounts[account.ID] = cloneAccount(account)
		return nil
	})
	return err
}

func sameAccountCredentials(a, b provider.Account) bool {
	if a.ProviderID != b.ProviderID || a.APIKey != b.APIKey || (a.OAuth == nil) != (b.OAuth == nil) {
		return false
	}
	return a.OAuth == nil || *a.OAuth == *b.OAuth
}

func cloneAccount(a provider.Account) provider.Account {
	return provider.CloneAccount(a)
}

func cloneAccounts(accounts map[string]provider.Account) map[string]provider.Account {
	return provider.CloneAccounts(accounts)
}

func FileConfigToAppSettings(fc FileConfig) ttypes.AppSettings {
	s := ttypes.AppSettings{
		ActiveProvider: fc.ActiveProvider,
		Providers:      cloneProviderConfigs(fc.Providers),
		Accounts:       cloneAccounts(fc.Accounts),
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
	if fc.Safety != nil {
		s.Safety = *fc.Safety
		s.Safety.CommandRules = append([]ttypes.CommandSeverityRule(nil), fc.Safety.CommandRules...)
	}
	if fc.LocalHistory != nil {
		s.LocalHistory = *fc.LocalHistory
		s.LocalHistory.ExtraIgnorePatterns = append([]string(nil), fc.LocalHistory.ExtraIgnorePatterns...)
	}
	ttypes.ApplyAppDefaults(&s)
	return s
}

func ApplyAppSettingsToFileConfig(fc *FileConfig, s ttypes.AppSettings) {
	if fc == nil {
		return
	}
	theme, editor, lsp, ui, font, memorySettings, safetySettings, historySettings := s.Theme, s.Editor, s.LSP, s.UI, s.Font, s.Memory, s.Safety, s.LocalHistory
	safetySettings.CommandRules = append([]ttypes.CommandSeverityRule(nil), s.Safety.CommandRules...)
	fc.Theme = &theme
	fc.Editor = &editor
	fc.LSP = &lsp
	fc.UI = &ui
	fc.Font = &font
	fc.Memory = &memorySettings
	fc.Safety = &safetySettings
	fc.LocalHistory = &historySettings
	fc.ActiveProvider = s.ActiveProvider
	if s.Accounts != nil {
		fc.Accounts = cloneAccounts(s.Accounts)
	}
	if len(s.Agents.Categories) > 0 || s.Agents.ActiveProfile != "" ||
		s.Agents.MaxConcurrent > 0 || len(s.Agents.Profiles) > 0 {
		agents := session.DefaultAgentsConfig()
		if fc.Agents != nil {
			agents = *fc.Agents
		}
		if s.Agents.ActiveProfile == "" {
			s.Agents.ActiveProfile = agents.ActiveProfile
		}
		agentCfg := &session.Config{Agents: agents}
		session.ApplyWorkspaceAgentDefaults(agentCfg, s.Agents)
		fc.Agents = &agentCfg.Agents
	}
	if s.Providers != nil {
		fc.Providers = cloneProviderConfigs(s.Providers)
	}
}

func cloneProviderConfigs(src map[string]provider.Template) map[string]provider.Template {
	return provider.CloneTemplates(src)
}

func FileConfigToSession(fc FileConfig) *session.Config {
	cfg := &session.Config{
		ActiveProvider:  fc.ActiveProvider,
		Providers:       cloneProviderConfigs(fc.Providers),
		Accounts:        cloneAccounts(fc.Accounts),
		TurnLogsEnabled: fc.IsTurnLogsEnabled(),
	}
	if fc.Agents != nil {
		cfg.Agents = *fc.Agents
		session.EnsureAgentsDefaults(&cfg.Agents)
	} else {
		cfg.Agents = session.DefaultAgentsConfig()
	}
	if fc.Memory != nil {
		cfg.Memory = *fc.Memory
	}
	if fc.Safety != nil {
		cfg.Safety = *fc.Safety
		cfg.Safety.CommandRules = append([]ttypes.CommandSeverityRule(nil), fc.Safety.CommandRules...)
	}
	return cfg
}

func SessionToFileConfig(sc *session.Config) FileConfig {
	// Start from existing file config so visual prefs are preserved.
	fc := LoadFileConfig()
	tl := sc.TurnLogsEnabled
	fc.ActiveProvider = sc.ActiveProvider
	fc.Providers = cloneProviderConfigs(sc.Providers)
	fc.Accounts = cloneAccounts(sc.Accounts)
	fc.TurnLogsEnabled = &tl
	fc.Agents = &sc.Agents
	safety := sc.Safety
	safety.CommandRules = append([]ttypes.CommandSeverityRule(nil), sc.Safety.CommandRules...)
	fc.Safety = &safety
	return fc
}
