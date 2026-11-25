package session

import (
	"encoding/json"
	"strings"
	"testing"

	"crude/agent/provider"
	"crude/internal/providersim"
	"crude/types/setting"
)

func suggestionConfig(endpoint string) *Config {
	accounts := map[string]provider.Account{
		"errand-account": {ID: "errand-account", ProviderID: "openai", APIKey: "fixture-key", Label: "Errand",
			Connection: provider.Template{Name: "Local", ProviderURL: endpoint, APIStandard: "openai", AuthType: provider.AuthBearer}},
		"fast-account": {ID: "fast-account", ProviderID: "openai", APIKey: "fixture-key-2", Label: "Fast",
			Connection: provider.Template{Name: "Fast", ProviderURL: endpoint, APIStandard: "openai", AuthType: provider.AuthBearer}},
	}
	return &Config{Accounts: accounts, AccountManager: provider.NewAccountManager(accounts, nil),
		Agents: AgentsConfig{ActiveProfile: "p", Profiles: map[string]ProfileConfig{"p": {Categories: map[string]CategoryConfig{
			CategoryErrand: {AccountID: "errand-account", Model: "errand-model", ThinkingEffort: "high", ModelMaxOutput: 100},
		}}}}}
}

// Suggestions follow Errand's account and model by default, always send
// thinking "none", and a Suggestions account replaces Errand's completely.
func TestSuggestionCompleterUsesErrandAndSendsNoThinking(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.Text("<|editable_region_start|>\nx\n<|editable_region_end|>").WithUsage(providersim.Usage{Input: 10, Output: 5}))
	cfg := suggestionConfig(server.BaseURL(providersim.Chat))
	completer, err := cfg.SuggestionCompleter()
	if err != nil {
		t.Fatal(err)
	}
	if completer.Model != "errand-model" || completer.AccountID != "errand-account" || completer.MaxOutputTokens != 100 || completer.Format != setting.SuggestionFormatChat {
		t.Fatalf("completer = %+v", completer)
	}
	stream, err := completer.Stream(t.Context(), "system", "input")
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for {
		update, err := stream.Next()
		if err != nil {
			break
		}
		text.WriteString(update.ContentDelta)
	}
	_ = stream.Close()
	if !strings.Contains(text.String(), "editable_region_end") {
		t.Fatalf("answer = %q", text.String())
	}
	request, _ := server.Last()
	if request.Model != "errand-model" {
		t.Fatalf("model = %v", request.Model)
	}
	var body map[string]json.RawMessage
	_ = json.Unmarshal(request.Raw, &body)
	if effort, exists := body["reasoning_effort"]; exists && string(effort) != `"none"` {
		t.Fatalf("suggestions sent thinking %s, want none", effort)
	}
	if strings.Contains(string(request.Raw), "high") {
		t.Fatalf("Errand's own thinking level leaked into a suggestion: %s", request.Raw)
	}

	cfg.Agents.Suggestions = setting.Suggestion{AccountID: "fast-account", Model: "fast-model", TimeoutSeconds: 3}
	fast, err := cfg.SuggestionCompleter()
	if err != nil {
		t.Fatal(err)
	}
	if fast.AccountID != "fast-account" || fast.Model != "fast-model" || fast.MaxOutputTokens != 256 || fast.TimeoutSeconds != 3 {
		t.Fatalf("fast completer = %+v", fast)
	}
	if fast.ConnectionIdentity == completer.ConnectionIdentity {
		t.Fatal("a different destination kept the same consent identity")
	}
}

func TestSuggestionCompleterRefusalsAreUserText(t *testing.T) {
	cfg := suggestionConfig("http://127.0.0.1:1")
	cfg.Agents.Suggestions = setting.Suggestion{Format: setting.SuggestionFormatDeepSeek}
	if _, err := cfg.SuggestionCompleter(); err == nil || !strings.Contains(err.Error(), "endpoint path") {
		t.Fatalf("missing FIM endpoint: %v", err)
	}
	cfg.Agents.Suggestions = setting.Suggestion{AccountID: "missing", Model: "m"}
	if _, err := cfg.SuggestionCompleter(); err == nil || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("missing account: %v", err)
	}
	off := false
	cfg = suggestionConfig("http://127.0.0.1:1")
	role := cfg.Agents.Profiles["p"].Categories[CategoryErrand]
	role.Enabled = &off
	cfg.Agents.Profiles["p"].Categories[CategoryErrand] = role
	if _, err := cfg.SuggestionCompleter(); err == nil || !strings.Contains(err.Error(), "turned off") {
		t.Fatalf("disabled Errand: %v", err)
	}
	var none *Config
	if _, err := none.SuggestionCompleter(); err == nil {
		t.Fatal("nil config prepared a completer")
	}
}

// A rotated key keeps consent; a new URL or a removed account does not.
func TestSuggestionConnectionIdentityFollowsTheDestination(t *testing.T) {
	cfg := suggestionConfig("http://127.0.0.1:1")
	completer, err := cfg.SuggestionCompleter()
	if err != nil {
		t.Fatal(err)
	}
	account := cfg.Accounts["errand-account"]
	account.APIKey = "rotated"
	cfg.AccountManager.Replace(map[string]provider.Account{"errand-account": account, "fast-account": cfg.Accounts["fast-account"]})
	if err := completer.Current(); err != nil {
		t.Fatalf("a rotated key revoked consent: %v", err)
	}
	account.Connection.ProviderURL = "http://127.0.0.1:2"
	cfg.AccountManager.Replace(map[string]provider.Account{"errand-account": account})
	if err := completer.Current(); err == nil {
		t.Fatal("a new URL kept the old consent")
	}
	cfg.AccountManager.Replace(map[string]provider.Account{})
	if err := completer.Current(); err == nil {
		t.Fatal("a removed account kept the old consent")
	}
}
