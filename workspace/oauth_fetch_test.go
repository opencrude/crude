package workspace

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"crude/agent/provider"
	"crude/agent/session"
	"crude/internal/providersim"
)

// TestHandleFetchModelsAttachesOAuth verifies that fetching models for the
// ChatGPT subscription provider attaches the stored OAuth access token (and
// codex headers) instead of sending an unauthenticated request that fails with
// "requires an API key".
func TestHandleFetchModelsAttachesOAuth(t *testing.T) {
	srv := providersim.New(t, providersim.WithModelCatalog(providersim.Codex,
		providersim.Model{ID: "gpt-5.6", DisplayName: "GPT-5.6", ContextWindow: 1000000},
	))

	ws := &Workspace{
		sessionCfg: &session.Config{
			AccountManager: provider.NewAccountManager(map[string]provider.Account{
				"personal": {ID: "personal", ProviderID: "chatgpt", Label: "Personal",
					OAuth: &provider.OAuthCredentials{
						AccessToken: "AT-oauth",
						AccountID:   "acct_123",
					}, Connection: provider.DefaultTemplates()["chatgpt"],
				},
			}, nil),
		},
	}

	entries, err := ws.HandleFetchModels("chatgpt", "personal", srv.BaseURL(providersim.Codex), "codex")
	if err != nil {
		t.Fatalf("HandleFetchModels: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "gpt-5.6" {
		t.Fatalf("entries = %+v", entries)
	}
	request, observed := srv.Last()
	if !observed || request.Method != http.MethodGet || request.Path != "/backend-api/codex/models" {
		t.Fatalf("expected GET /backend-api/codex/models, got %s %s", request.Method, request.Path)
	}
	if gotAuth := request.Header.Get("Authorization"); gotAuth != "Bearer AT-oauth" {
		t.Errorf("Authorization = %q, want 'Bearer AT-oauth' (OAuth not attached)", gotAuth)
	}
	if gotAccountID := request.Header.Get("chatgpt-account-id"); gotAccountID != "acct_123" {
		t.Errorf("chatgpt-account-id = %q, want acct_123", gotAccountID)
	}
	if gotOriginator := request.Header.Get("originator"); gotOriginator != "crude" {
		t.Errorf("originator = %q, want crude", gotOriginator)
	}
	// The codex backend requires client_version on the /models request; without
	// it the real backend returns HTTP 400.
	_, gotQuery, _ := strings.Cut(request.Target, "?")
	if !strings.Contains(gotQuery, "client_version=") {
		t.Errorf("query = %q, want client_version parameter (backend returns 400 without it)", gotQuery)
	}
}

// TestCodexModelsEndpointHasClientVersion pins the URL shape the codex backend
// requires: /models?client_version=<version>. A missing client_version is the
// cause of the reported HTTP 400 on model fetch.
func TestCodexModelsEndpointHasClientVersion(t *testing.T) {
	std := &provider.CodexStandard{}
	base, err := url.Parse("https://chatgpt.com/backend-api/codex")
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}
	endpoint := std.ModelsURL(base, "")
	if !strings.Contains(endpoint.String(), "/models?client_version=") {
		t.Errorf("endpoint = %q, want /models?client_version=...", endpoint)
	}
}

// TestHandleFetchModelsPreservesPerModelContext verifies the full model-fetch
// flow keeps each model's context_window from the codex backend instead of
// collapsing every model to a single value. This pins the reported bug where
// every model showed 272k.
func TestHandleFetchModelsPreservesPerModelContext(t *testing.T) {
	srv := providersim.New(t, providersim.WithModelCatalog(providersim.Codex,
		providersim.Model{ID: "gpt-5.6-codex", DisplayName: "GPT-5.6 Codex", ContextWindow: 1000000},
		providersim.Model{ID: "gpt-5-codex", DisplayName: "GPT-5 Codex", ContextWindow: 400000},
		providersim.Model{ID: "gpt-5.4-codex", DisplayName: "GPT-5.4 Codex", ContextWindow: 272000},
	))

	ws := &Workspace{
		sessionCfg: &session.Config{
			AccountManager: provider.NewAccountManager(map[string]provider.Account{
				"personal": {ID: "personal", ProviderID: "chatgpt", Label: "Personal",
					OAuth: &provider.OAuthCredentials{AccessToken: "AT-oauth", AccountID: "acct_1"}, Connection: provider.DefaultTemplates()["chatgpt"],
				},
			}, nil),
		},
	}

	entries, err := ws.HandleFetchModels("chatgpt", "personal", srv.BaseURL(providersim.Codex), "codex")
	if err != nil {
		t.Fatalf("HandleFetchModels: %v", err)
	}
	got := map[string]int{}
	for _, e := range entries {
		got[e.ID] = e.ContextWindow
	}
	if got["gpt-5.6-codex"] != 1000000 {
		t.Errorf("gpt-5.6-codex ctx = %d, want 1000000", got["gpt-5.6-codex"])
	}
	if got["gpt-5-codex"] != 400000 {
		t.Errorf("gpt-5-codex ctx = %d, want 400000", got["gpt-5-codex"])
	}
	if got["gpt-5.4-codex"] != 272000 {
		t.Errorf("gpt-5.4-codex ctx = %d, want 272000", got["gpt-5.4-codex"])
	}
}

// Exercise the workspace entry point concurrently: model access belongs to an
// account, not the provider or whichever session happens to be active.
func TestAccountModelDiscoveryIsolation(t *testing.T) {
	srv := providersim.New(t,
		providersim.WithCredentialModels(providersim.Chat, "personal-key", providersim.Model{ID: "personal-model"}),
		providersim.WithCredentialModels(providersim.Chat, "work-key", providersim.Model{ID: "work-model"}),
	)
	baseURL := srv.BaseURL(providersim.Chat)
	accounts := map[string]provider.Account{
		"personal": {ID: "personal", ProviderID: "openai", Label: "Personal", APIKey: "personal-key", Connection: provider.DefaultTemplates()["openai"]},
		"work":     {ID: "work", ProviderID: "openai", Label: "Work", APIKey: "work-key", Connection: provider.DefaultTemplates()["openai"]},
	}
	ws := &Workspace{sessionCfg: &session.Config{
		Accounts: accounts, AccountManager: provider.NewAccountManager(accounts, nil),
	}}
	var group sync.WaitGroup
	for _, id := range []string{"personal", "work"} {
		group.Go(func() {
			models, err := ws.HandleFetchModels("openai", id, baseURL, "openai")
			if err != nil {
				t.Errorf("%s: %v", id, err)
				return
			}
			if len(models) != 1 || models[0].ID != id+"-model" {
				t.Errorf("%s received wrong models: %+v", id, models)
			}
		})
	}
	group.Wait()
	before := srv.Requests()
	for _, request := range before {
		switch request.Header.Get("Authorization") {
		case "Bearer personal-key", "Bearer work-key":
		default:
			t.Errorf("unexpected credential on request")
		}
	}
	for _, selection := range [][2]string{{"openai", "missing"}, {"anthropic", "personal"}, {"openai", ""}} {
		if _, err := ws.HandleFetchModels(selection[0], selection[1], baseURL, "openai"); err == nil {
			t.Errorf("invalid connection %v silently accepted", selection)
		}
	}
	if len(srv.Requests()) != len(before) {
		t.Fatal("invalid account selection reached the network")
	}
}

func TestDraftAccountCanFetchBeforeSaveWithoutChangingLiveCredentials(t *testing.T) {
	server := providersim.New(t, providersim.WithModels(providersim.Chat, "draft-model"))
	baseURL := server.BaseURL(providersim.Chat)
	accounts := map[string]provider.Account{"work": {ID: "work", ProviderID: "openai", Label: "Work", APIKey: "saved", Connection: provider.DefaultTemplates()["openai"]}}
	ws := &Workspace{sessionCfg: &session.Config{Accounts: accounts, AccountManager: provider.NewAccountManager(accounts, nil)}}
	draft := accounts["work"]
	draft.APIKey = "draft"
	models, err := ws.PrepareDraftAccountModels(draft, baseURL, "openai")()
	request, _ := server.Last()
	got := request.Header.Get("Authorization")
	if err != nil || len(models) != 1 || got != "Bearer draft" {
		t.Fatalf("draft discovery: models=%v err=%v auth=%q", models, err, got)
	}
	if ws.AllAccounts()["work"].APIKey != "saved" {
		t.Fatal("model discovery changed live credentials")
	}
	draft.ID = "new-account"
	if _, err := ws.PrepareDraftAccountModels(draft, baseURL, "openai")(); err != nil {
		t.Fatal(err)
	}
	if _, ok := ws.AllAccounts()["new-account"]; ok {
		t.Fatal("model discovery persisted unsaved account")
	}
}
