package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"crude/provider"
	"crude/session"
	"crude/tui"
)

const defaultModel = "google/gemini-2.0-flash-exp:free"
const defaultEmbedModel = "openai/text-embedding-3-small"

type fileConfig struct {
	APIKey          string                            `json:"api_key"`
	Model           string                            `json:"model"`
	EmbedModel      string                            `json:"embed_model"`
	ProviderID      string                            `json:"provider_id,omitempty"`
	ProviderURL     string                            `json:"provider_url,omitempty"`
	ProviderAPIKey  string                            `json:"provider_api_key,omitempty"`
	ProviderConfigs map[string]session.ProviderConfig `json:"provider_configs,omitempty"`
}

func defaultFileConfig() fileConfig {
	return fileConfig{
		Model:      defaultModel,
		EmbedModel: defaultEmbedModel,
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
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	if cfg.EmbedModel == "" {
		cfg.EmbedModel = defaultEmbedModel
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
		APIKey:          fc.APIKey,
		Model:           fc.Model,
		EmbedModel:      fc.EmbedModel,
		ProviderID:      fc.ProviderID,
		ProviderURL:     fc.ProviderURL,
		ProviderAPIKey:  fc.ProviderAPIKey,
		ProviderConfigs: fc.ProviderConfigs,
	}
}

func sessionToFileConfig(sc *session.Config) fileConfig {
	var pc map[string]session.ProviderConfig
	if sc.ProviderConfigs != nil {
		pcfgs := make(map[string]session.ProviderConfig, len(sc.ProviderConfigs))
		for k, v := range sc.ProviderConfigs {
			pcfgs[k] = v
		}
		pc = pcfgs
	}
	return fileConfig{
		APIKey:          sc.APIKey,
		Model:           sc.Model,
		EmbedModel:      sc.EmbedModel,
		ProviderID:      sc.ProviderID,
		ProviderURL:     sc.ProviderURL,
		ProviderAPIKey:  sc.ProviderAPIKey,
		ProviderConfigs: pc,
	}
}

func main() {
	apiKeyFlag := flag.String("key", "", "API key (or set CRUDE_API_KEY)")
	modelFlag := flag.String("model", "", "Model to use")
	providerFlag := flag.String("provider", "", "Provider ID (openrouter, openai, anthropic, etc.)")
	sysPromptFlag := flag.String("system", "", "Override system prompt")
	settingsFlag := flag.Bool("settings", false, "Open settings on startup")
	flag.Parse()

	log.SetOutput(os.Stderr)
	log.SetFlags(0)

	fc := loadFileConfig()

	if *apiKeyFlag != "" {
		fc.APIKey = *apiKeyFlag
		fc.ProviderAPIKey = *apiKeyFlag
	}
	if fc.APIKey == "" {
		fc.APIKey = os.Getenv("CRUDE_API_KEY")
		if fc.APIKey == "" {
			fc.APIKey = os.Getenv("OPENROUTER_API_KEY")
		}
		fc.ProviderAPIKey = fc.APIKey
	}
	if *providerFlag != "" {
		fc.ProviderID = *providerFlag
	}
	if fc.ProviderID == "" {
		fc.ProviderID = "openrouter"
	}
	if *modelFlag != "" {
		fc.Model = *modelFlag
	}

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

	tuiInst, err := tui.Start(sessCfg.Model, username, initial, providerPresets, loadHistory())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start TUI: %v\n", err)
		os.Exit(1)
	}

	tui.OnHistorySave = func(history []string) {
		if err := saveHistory(history); err != nil {
			log.Printf("[history] save error: %v", err)
		}
	}

	logDir := filepath.Join(home, ".crude")
	os.MkdirAll(logDir, 0o755)
	if logFile, err := os.OpenFile(filepath.Join(logDir, "crude.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		log.SetOutput(logFile)
	}

	tuiInst.OnQuit = func() {
		sess.Close()
		os.Exit(0)
	}

	tuiInst.OnSettingsSave = func(s tui.Settings) {
		if err := sess.HandleSettingsSave(s); err != nil {
			tuiInst.SetError(err.Error())
			return
		}
		newFC := sessionToFileConfig(sessCfg)
		if err := saveFileConfig(newFC); err != nil {
			tuiInst.SetError("Failed to save config: " + err.Error())
		}
		tuiInst.UpdateModelName(sess.ModelName())
	}

	tuiInst.FetchModelsFn = func(providerID, baseURL, apiKey string) ([]tui.ModelEntry, error) {
		return sess.HandleFetchModels(providerID, baseURL, apiKey)
	}

	tuiInst.OnUndoCmd = func(cmd string) {
		result := sess.HandleUndo(cmd)
		tuiInst.Program.Send(result)
	}

	if *settingsFlag || fc.APIKey == "" {
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

	go sess.Run(tuiInst, blockResolveCh)

	tuiInst.Wait()
}
