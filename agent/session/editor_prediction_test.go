package session

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"crude/agent/provider"
	ttypes "crude/types"
)

func suggestionConfig(endpoint string) *Config {
	accounts := map[string]provider.Account{
		"aeron-account": {ID: "aeron-account", ProviderID: "openai", APIKey: "fixture-key", Label: "Errand",
			Connection: provider.Template{Name: "Local", ProviderURL: endpoint, APIStandard: "openai", AuthType: provider.AuthBearer}},
		"fast-account": {ID: "fast-account", ProviderID: "openai", APIKey: "fixture-key-2", Label: "Fast",
			Connection: provider.Template{Name: "Fast", ProviderURL: endpoint, APIStandard: "openai", AuthType: provider.AuthBearer}},
	}
	return &Config{Accounts: accounts, AccountManager: provider.NewAccountManager(accounts, nil),
		Agents: AgentsConfig{ActiveProfile: "p", Profiles: map[string]ProfileConfig{"p": {Categories: map[string]CategoryConfig{
			CategoryAeron: {AccountID: "aeron-account", Model: "aeron-model", ThinkingEffort: "high", ModelMaxOutput: 100},
		}}}}}
}

// Suggestions follow Errand's account and model by default, always send
// thinking "none", and a Suggestions account replaces Errand's completely.
func TestSuggestionCompleterUsesAeronAndSendsNoThinking(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"<|editable_region_start|>\\nx\\n<|editable_region_end|>\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	cfg := suggestionConfig(server.URL)
	completer, err := cfg.SuggestionCompleter()
	if err != nil {
		t.Fatal(err)
	}
	if completer.Model != "aeron-model" || completer.AccountID != "aeron-account" || completer.MaxOutputTokens != 100 || completer.Format != ttypes.SuggestionFormatChat {
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
	mu.Lock()
	body := bodies[0]
	mu.Unlock()
	if body["model"] != "aeron-model" {
		t.Fatalf("model = %v", body["model"])
	}
	if effort, ok := body["reasoning_effort"]; ok && effort != "none" {
		t.Fatalf("suggestions sent thinking %v, want none", effort)
	}
	if strings.Contains(string(suggestionJSON(body)), "high") {
		t.Fatalf("Errand's own thinking level leaked into a suggestion: %s", suggestionJSON(body))
	}

	cfg.Agents.Suggestions = ttypes.SuggestionSettings{AccountID: "fast-account", Model: "fast-model", TimeoutSeconds: 3}
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
	cfg.Agents.Suggestions = ttypes.SuggestionSettings{Format: ttypes.SuggestionFormatDeepSeek}
	if _, err := cfg.SuggestionCompleter(); err == nil || !strings.Contains(err.Error(), "endpoint path") {
		t.Fatalf("missing FIM endpoint: %v", err)
	}
	cfg.Agents.Suggestions = ttypes.SuggestionSettings{AccountID: "missing", Model: "m"}
	if _, err := cfg.SuggestionCompleter(); err == nil || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("missing account: %v", err)
	}
	off := false
	cfg = suggestionConfig("http://127.0.0.1:1")
	role := cfg.Agents.Profiles["p"].Categories[CategoryAeron]
	role.Enabled = &off
	cfg.Agents.Profiles["p"].Categories[CategoryAeron] = role
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
	account := cfg.Accounts["aeron-account"]
	account.APIKey = "rotated"
	cfg.AccountManager.Replace(map[string]provider.Account{"aeron-account": account, "fast-account": cfg.Accounts["fast-account"]})
	if err := completer.Current(); err != nil {
		t.Fatalf("a rotated key revoked consent: %v", err)
	}
	account.Connection.ProviderURL = "http://127.0.0.1:2"
	cfg.AccountManager.Replace(map[string]provider.Account{"aeron-account": account})
	if err := completer.Current(); err == nil {
		t.Fatal("a new URL kept the old consent")
	}
	cfg.AccountManager.Replace(map[string]provider.Account{})
	if err := completer.Current(); err == nil {
		t.Fatal("a removed account kept the old consent")
	}
}

func suggestionJSON(v any) []byte { raw, _ := json.Marshal(v); return raw }
