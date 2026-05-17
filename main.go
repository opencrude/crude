package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"crude/provider"
	"crude/session"
	"crude/tui"
	"crude/tui/app"
	"crude/tui/platform"
)

const defaultModel = ""

type fileConfig struct {
	ActiveProvider  string                            `json:"active_provider"`
	Providers       map[string]session.ProviderConfig `json:"providers"`
	TurnLogsEnabled *bool                             `json:"turn_logs_enabled,omitempty"`
}

func (c *fileConfig) IsTurnLogsEnabled() bool {
	if c.TurnLogsEnabled == nil {
		return true
	}
	return *c.TurnLogsEnabled
}

func defaultFileConfig() fileConfig {
	return fileConfig{
		ActiveProvider: "openrouter",
		Providers: map[string]session.ProviderConfig{
			"openrouter": {
				Model: defaultModel,
			},
		},
	}
}

func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "crude_config.json"
	}
	return filepath.Join(home, ".crude", "config.json")
}

func historyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "crude_history.json"
	}
	return filepath.Join(home, ".crude", "history.json")
}

func loadFileConfig() fileConfig {
	cfg := defaultFileConfig()
	data, err := os.ReadFile(configPath())
	if err != nil {
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg
	}
	return cfg
}

func saveFileConfig(cfg fileConfig) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func loadHistory() []string {
	var history []string
	data, err := os.ReadFile(historyPath())
	if err != nil {
		return history
	}
	_ = json.Unmarshal(data, &history)
	return history
}

func saveHistory(history []string) error {
	path := historyPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func fileConfigToSession(fc fileConfig) *session.Config {
	return &session.Config{
		ActiveProvider:  fc.ActiveProvider,
		Providers:       fc.Providers,
		TurnLogsEnabled: fc.IsTurnLogsEnabled(),
	}
}

func sessionToFileConfig(sc *session.Config) fileConfig {
	tl := sc.TurnLogsEnabled
	return fileConfig{
		ActiveProvider:  sc.ActiveProvider,
		Providers:       sc.Providers,
		TurnLogsEnabled: &tl,
	}
}

func main() {
	platform.InitConsole()

	apiKeyFlag := flag.String("key", "", "API key (or set CRUDE_API_KEY)")
	modelFlag := flag.String("model", "", "Model to use")
	providerFlag := flag.String("provider", "", "Provider ID (openrouter, openai, anthropic, etc.)")
	sysPromptFlag := flag.String("system", "", "Override system prompt")
	settingsFlag := flag.Bool("settings", false, "Open settings on startup")
	flag.Parse()

	fc := loadFileConfig()

	if fc.Providers == nil {
		fc.Providers = make(map[string]session.ProviderConfig)
	}

	providerID := fc.ActiveProvider
	if providerID == "" {
		providerID = "openrouter"
		fc.ActiveProvider = providerID
	}

	if *providerFlag != "" {
		providerID = *providerFlag
		fc.ActiveProvider = providerID
	}

	pc := fc.Providers[providerID]
	if pc.APIKey == "" {
		if *apiKeyFlag != "" {
			pc.APIKey = *apiKeyFlag
		} else {
			pc.APIKey = os.Getenv("CRUDE_API_KEY")
			if pc.APIKey == "" {
				pc.APIKey = os.Getenv("WAFER_API_KEY")
			}
			if pc.APIKey == "" {
				pc.APIKey = os.Getenv("OPENROUTER_API_KEY")
			}
		}
	}
	if *modelFlag != "" {
		pc.Model = *modelFlag
	}
	fc.Providers[providerID] = pc

	_ = sysPromptFlag

	sessCfg := fileConfigToSession(fc)
	home, _ := os.UserHomeDir()

	sess, err := session.New(sessCfg, home)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create session: %v\n", err)
		os.Exit(1)
	}

	username := session.ResolveUsername()
	initial := session.ConfigToSettings(sessCfg)

	var providerPresets []tui.ProviderPreset
	for _, p := range provider.Presets {
		providerPresets = append(providerPresets, tui.ProviderPreset{
			ID:       p.ID,
			Name:     p.Name,
			HelpText: p.HelpText,
			BaseURL:  p.BaseURL,
		})
	}

	tuiInst, err := tui.Start(sessCfg.GetActiveProviderConfig().Model, username, initial, providerPresets, loadHistory(), sess.Logger())
	if err != nil {
		sess.Logger().Error("SESSION", "tui_start_failed", "error", err.Error())
	fmt.Fprintf(os.Stderr, "Failed to start TUI: %v\n", err)
		os.Exit(1)
	}

	tuiInst.GetProviderConfigFn = func(providerID string) tui.ProviderConfig {
		if cfg, ok := sess.Config().Providers[providerID]; ok {
			return tui.ProviderConfig{
				APIKey:         cfg.APIKey,
				Model:          cfg.Model,
				ProviderURL:    cfg.ProviderURL,
				ThinkingEffort: cfg.ThinkingEffort,
			}
		}
		return tui.ProviderConfig{}
	}

	app.OnHistorySave = func(history []string) {
		if err := saveHistory(history); err != nil {
			sess.Logger().Warn("HISTORY", "save_failed", "error", err.Error())
		}
	}

	tuiInst.OnQuit = func() {
		sess.Close()
	}

	var exitMessage string
	tuiInst.OnExitMessage = func() {
		if sess.HasMessages() {
			exitMessage = fmt.Sprintf("\n✓ Session saved to: %s\n", sess.SessionDir())
		}
	}

	tuiInst.OnSettingsSave = func(s tui.Settings) error {
		if err := sess.HandleSettingsSave(s); err != nil {
			return err
		}
		newFC := sessionToFileConfig(sess.Config())
		if err := saveFileConfig(newFC); err != nil {
			return fmt.Errorf("Failed to save config: %w", err)
		}
		tuiInst.UpdateModelName(sess.ModelName())
		return nil
	}

	tuiInst.FetchModelsFn = func(providerID, baseURL, apiKey string) ([]tui.ModelEntry, error) {
		return sess.HandleFetchModels(providerID, baseURL, apiKey)
	}

	tuiInst.OnYoloCmd = func(enabled bool) {
		sess.SetYolo(enabled)
	}

	tuiInst.OnUndoCmd = func(cmd string) {
		result := sess.HandleUndo(cmd)
		tuiInst.Program.Send(result)
	}

	if *settingsFlag || fc.Providers[fc.ActiveProvider].APIKey == "" {
		tuiInst.OpenSettings()
	}

	blockResolveCh := make(chan string, 1)

	tuiInst.OnConfirmResult = func(result tui.ConfirmResult) {
		switch result.Choice {
		case tui.ConfirmAllow:
			blockResolveCh <- "allow"
		case tui.ConfirmAlways:
			blockResolveCh <- "always"
		case tui.ConfirmDeny:
			blockResolveCh <- "deny"
		default:
			blockResolveCh <- "deny"
		}
	}

	go sess.Run(tuiInst, blockResolveCh, tuiInst.BtwCh)

	tuiInst.Wait()
	if exitMessage != "" {
		fmt.Fprint(os.Stderr, exitMessage)
	}
}
