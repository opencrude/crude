package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"crude/provider"
)

func TestNewSessionWithoutModelCreatesNoArtifacts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	for _, model := range []string{"", "  "} {
		cfg := &Config{Accounts: map[string]provider.Account{"local": {ID: "local", ProviderID: "openai", Connection: provider.Template{Name: "Local", ProviderURL: "http://127.0.0.1:1/v1", APIStandard: "openai", AuthType: provider.AuthNone}}}, Agents: DefaultAgentsConfig()}
		profile := cfg.Agents.Profiles["default"]
		profile.Categories[CategoryDefault] = CategoryConfig{AccountID: "local", Model: model}
		cfg.Agents.Profiles["default"] = profile
		created, err := NewWithSessionDir("", cfg, t.TempDir(), root)
		if !errors.Is(err, ErrModelRequired) || created != nil {
			t.Fatalf("missing model: session=%v err=%v", created, err)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatalf("missing model created session artifacts: %v", err)
		}
	}
}
