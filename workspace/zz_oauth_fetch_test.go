package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
			Providers: map[string]session.ProviderConfig{
				"chatgpt": {
					OAuth: &provider.OAuthCredentials{
						AccessToken: "AT-oauth",
						AccountID:   "acct_123",
					},
				},
			},
		},
	}

	entries, err := ws.HandleFetchModels("chatgpt", srv.URL, "", "codex")
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
	endpoint := std.ModelsEndpoint("https://chatgpt.com/backend-api/codex")
	if !strings.Contains(endpoint, "/models?client_version=") {
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
			Providers: map[string]session.ProviderConfig{
				"chatgpt": {
					OAuth: &provider.OAuthCredentials{AccessToken: "AT-oauth", AccountID: "acct_1"},
				},
			},
		},
	}

	entries, err := ws.HandleFetchModels("chatgpt", srv.URL+"/codex", "", "codex")
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
