package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"crude/provider"
	"crude/session"
)

// TestHandleFetchModelsAttachesOAuth verifies that fetching models for the
// ChatGPT subscription provider attaches the stored OAuth access token (and
// codex headers) instead of sending an unauthenticated request that fails with
// "requires an API key".
func TestHandleFetchModelsAttachesOAuth(t *testing.T) {
	var gotAuth, gotAccountID, gotOriginator, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccountID = r.Header.Get("chatgpt-account-id")
		gotOriginator = r.Header.Get("originator")
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"slug": "gpt-5.6", "display_name": "GPT-5.6", "context_window": 1000000},
			},
		})
	}))
	defer srv.Close()

	ws := &Workspace{
		sessionCfg: &session.Config{
			AccountManager: provider.NewAccountManager(map[string]provider.Account{
				"personal": {ID: "personal", ProviderID: "chatgpt", Label: "Personal",
					OAuth: &provider.OAuthCredentials{
						AccessToken: "AT-oauth",
						AccountID:   "acct_123",
					},
				},
			}, nil),
		},
	}

	entries, err := ws.HandleFetchModels("chatgpt", "personal", srv.URL, "codex")
	if err != nil {
		t.Fatalf("HandleFetchModels: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "gpt-5.6" {
		t.Fatalf("entries = %+v", entries)
	}
	if gotAuth != "Bearer AT-oauth" {
		t.Errorf("Authorization = %q, want 'Bearer AT-oauth' (OAuth not attached)", gotAuth)
	}
	if gotAccountID != "acct_123" {
		t.Errorf("chatgpt-account-id = %q, want acct_123", gotAccountID)
	}
	if gotOriginator != "crude" {
		t.Errorf("originator = %q, want crude", gotOriginator)
	}
	// The codex backend requires client_version on the /models request; without
	// it the real backend returns HTTP 400.
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"slug": "gpt-5.6-codex", "display_name": "GPT-5.6 Codex", "context_window": 1000000},
				{"slug": "gpt-5-codex", "display_name": "GPT-5 Codex", "context_window": 400000},
				{"slug": "gpt-5.4-codex", "display_name": "GPT-5.4 Codex", "context_window": 272000},
			},
		})
	}))
	defer srv.Close()

	ws := &Workspace{
		sessionCfg: &session.Config{
			AccountManager: provider.NewAccountManager(map[string]provider.Account{
				"personal": {ID: "personal", ProviderID: "chatgpt", Label: "Personal",
					OAuth: &provider.OAuthCredentials{AccessToken: "AT-oauth", AccountID: "acct_1"},
				},
			}, nil),
		},
	}

	entries, err := ws.HandleFetchModels("chatgpt", "personal", srv.URL+"/codex", "codex")
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
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var model string
		switch r.Header.Get("Authorization") {
		case "Bearer personal-key":
			model = "personal-model"
		case "Bearer work-key":
			model = "work-model"
		default:
			t.Errorf("unexpected credential on request")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": model}}})
	}))
	defer srv.Close()
	accounts := map[string]provider.Account{
		"personal": {ID: "personal", ProviderID: "openai", Label: "Personal", APIKey: "personal-key"},
		"work":     {ID: "work", ProviderID: "openai", Label: "Work", APIKey: "work-key"},
	}
	ws := &Workspace{sessionCfg: &session.Config{
		Accounts: accounts, AccountManager: provider.NewAccountManager(accounts, nil),
		Providers: map[string]session.ProviderConfig{"openai": provider.DefaultTemplates()["openai"]},
	}}
	var group sync.WaitGroup
	for _, id := range []string{"personal", "work"} {
		group.Go(func() {
			models, err := ws.HandleFetchModels("openai", id, srv.URL, "openai")
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
	before := requests.Load()
	for _, selection := range [][2]string{{"openai", "missing"}, {"anthropic", "personal"}, {"openai", ""}} {
		if _, err := ws.HandleFetchModels(selection[0], selection[1], srv.URL, "openai"); err == nil {
			t.Errorf("invalid connection %v silently accepted", selection)
		}
	}
	if requests.Load() != before {
		t.Fatal("invalid account selection reached the network")
	}
}

func TestDraftAccountCanFetchBeforeSaveWithoutChangingLiveCredentials(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"draft-model"}]}`))
	}))
	defer server.Close()
	accounts := map[string]provider.Account{"work": {ID: "work", ProviderID: "openai", Label: "Work", APIKey: "saved"}}
	ws := &Workspace{sessionCfg: &session.Config{Accounts: accounts, AccountManager: provider.NewAccountManager(accounts, nil)}}
	draft := accounts["work"]
	draft.APIKey = "draft"
	models, err := ws.PrepareDraftAccountModels(draft, server.URL, "openai")()
	if err != nil || len(models) != 1 || got != "Bearer draft" {
		t.Fatalf("draft discovery: models=%v err=%v auth=%q", models, err, got)
	}
	if ws.AllAccounts()["work"].APIKey != "saved" {
		t.Fatal("model discovery changed live credentials")
	}
	draft.ID = "new-account"
	if _, err := ws.PrepareDraftAccountModels(draft, server.URL, "openai")(); err != nil {
		t.Fatal(err)
	}
	if _, ok := ws.AllAccounts()["new-account"]; ok {
		t.Fatal("model discovery persisted unsaved account")
	}
}
