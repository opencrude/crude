package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const defaultModel = "google/gemini-2.0-flash-exp:free"

type SSHConf struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
	KeyFile  string `json:"key_file,omitempty"`
}

type ProviderConfig struct {
	APIKey      string `json:"api_key,omitempty"`
	Model       string `json:"model,omitempty"`
	EmbedModel  string `json:"embed_model,omitempty"`
	ProviderURL string `json:"provider_url,omitempty"`
}

type Config struct {
	APIKey     string  `json:"api_key"`
	Model      string  `json:"model"`
	EmbedModel string  `json:"embed_model"`
	Backend    string  `json:"backend"`
	SSH        SSHConf `json:"ssh"`

	ProviderID     string `json:"provider_id,omitempty"`
	ProviderURL    string `json:"provider_url,omitempty"`
	ProviderAPIKey string `json:"provider_api_key,omitempty"`

	ProviderConfigs map[string]ProviderConfig `json:"provider_configs,omitempty"`

	APIBaseURL      string `json:"api_base_url,omitempty"`
	APIAuthHeader   string `json:"api_auth_header,omitempty"`
	APIAuthValue    string `json:"api_auth_value,omitempty"`
	CustomExecCmd   string `json:"custom_exec_cmd,omitempty"`
	CustomReadCmd   string `json:"custom_read_cmd,omitempty"`
	CustomWriteCmd  string `json:"custom_write_cmd,omitempty"`
	CustomDeleteCmd string `json:"custom_delete_cmd,omitempty"`
	CustomListCmd   string `json:"custom_list_cmd,omitempty"`
}

const defaultEmbedModel = "openai/text-embedding-3-small"

func defaultConfig() Config {
	return Config{
		Model:      defaultModel,
		EmbedModel: defaultEmbedModel,
		Backend:    "local",
		SSH:        SSHConf{Port: 22},
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

func loadConfig() Config {
	cfg := defaultConfig()
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
	if cfg.Backend == "" {
		cfg.Backend = "local"
	}
	if cfg.SSH.Port == 0 {
		cfg.SSH.Port = 22
	}
	return cfg
}

func saveConfig(cfg Config) error {
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
