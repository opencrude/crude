package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// codexBackend is a mock of the ChatGPT codex backend that enforces the real
// request shape: /models requires client_version, and all endpoints require the
// OAuth bearer + codex headers.
type codexBackend struct {
	t              *testing.T
	modelsCalls    int
	chatCalls      int
	refreshCalls   int
	gotAuth        string
	gotAccountID   string
	gotOriginator  string
	gotClientVer   string
	gotUserAgent   string
	gotSessionID   string
	gotThreadID    string
	gotChatBody    map[string]any
	refreshToken   string
	accessToken    string
	expiresIn      int64
	rejectChatAuth bool
}

func (b *codexBackend) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		b.refreshCalls++
		body, _ := io.ReadAll(r.Body)
		form, _ := parseForm(string(body))
		if form["grant_type"] == "refresh_token" {
			if form["refresh_token"] != b.refreshToken {
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  b.accessToken,
			"refresh_token": b.refreshToken,
			"id_token":      jwtWithClaims(b.t, map[string]any{"chatgpt_account_id": "acct_lifecycle"}),
			"expires_in":    b.expiresIn,
			"email":         "u@example.com",
		})
	})
	mux.HandleFunc("/codex/models", func(w http.ResponseWriter, r *http.Request) {
		b.modelsCalls++
		b.gotAuth = r.Header.Get("Authorization")
		b.gotAccountID = r.Header.Get("chatgpt-account-id")
		b.gotOriginator = r.Header.Get("originator")
		b.gotClientVer = r.URL.Query().Get("client_version")
		b.gotUserAgent = r.Header.Get("User-Agent")
		b.gotSessionID = r.Header.Get("session-id")
		b.gotThreadID = r.Header.Get("thread-id")
		if b.gotClientVer == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "missing client_version"})
			return
		}
		if b.gotAuth != "Bearer "+b.accessToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"slug": "gpt-5.6", "display_name": "GPT-5.6", "context_window": 1000000},
				{"slug": "gpt-5.4", "display_name": "GPT-5.4", "context_window": 800000},
			},
		})
	})
	mux.HandleFunc("/codex/responses", func(w http.ResponseWriter, r *http.Request) {
		b.chatCalls++
		if b.rejectChatAuth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+b.accessToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &b.gotChatBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "resp_1", "model": "gpt-5.6", "status": "completed",
			"output": []map[string]any{
				{"type": "message", "content": []map[string]any{{"type": "output_text", "text": "hello"}}},
			},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
		})
	})
	return mux
}

func parseForm(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, pair := range strings.Split(s, "&") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			out[kv[0]] = kv[1]
		}
	}
	return out, nil
}

// TestOAuthLifecycleEndToEnd exercises the full provider lifecycle against a
// mock codex backend: token refresh, model fetch (with client_version), chat
// request, and refresh-on-expiry.
func TestOAuthLifecycleEndToEnd(t *testing.T) {
	backend := &codexBackend{
		t:            t,
		refreshToken: "RT-1",
		accessToken:  "AT-1",
		expiresIn:    3600,
	}
	srv := httptest.NewServer(backend.handler())
	defer srv.Close()

	origTokenURL := openAITokenURL
	openAITokenURL = srv.URL + "/oauth/token"
	defer func() { openAITokenURL = origTokenURL }()

	// 1. Refresh the access token from a refresh token (as EnsureAuth does).
	creds, err := RefreshOpenAIToken(context.Background(), "RT-1")
	if err != nil {
		t.Fatalf("RefreshOpenAIToken: %v", err)
	}
	if creds.AccessToken != "AT-1" || creds.AccountID != "acct_lifecycle" {
		t.Fatalf("creds = %+v", creds)
	}

	// 2. Build a codex provider with OAuth and fetch models.
	p := FromPreset("chatgpt", "", srv.URL+"/codex")
	p.SetOAuth(creds)
	models, err := FetchModels(p)
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %+v", models)
	}
	seen := map[string]bool{}
	for _, m := range models {
		seen[m.ID] = true
	}
	if !seen["gpt-5.6"] || !seen["gpt-5.4"] {
		t.Fatalf("models = %+v, want gpt-5.6 and gpt-5.4", models)
	}
	if backend.gotClientVer == "" {
		t.Errorf("client_version missing on /models request (backend returns 400)")
	}
	if backend.gotAuth != "Bearer AT-1" {
		t.Errorf("auth = %q, want Bearer AT-1", backend.gotAuth)
	}
	if backend.gotAccountID != "acct_lifecycle" {
		t.Errorf("chatgpt-account-id = %q", backend.gotAccountID)
	}
	if backend.gotOriginator != "crude" {
		t.Errorf("originator = %q", backend.gotOriginator)
	}
	if backend.gotSessionID == "" || backend.gotThreadID == "" {
		t.Errorf("session-id/thread-id missing: %q %q", backend.gotSessionID, backend.gotThreadID)
	}
	if !strings.Contains(backend.gotUserAgent, "crude/") {
		t.Errorf("User-Agent = %q, want crude/...", backend.gotUserAgent)
	}

	// 3. Make a chat request with the same provider.
	req := &ChatRequest{
		Model: "gpt-5.6",
		Messages: []ChatMessage{
			{Role: "user", Content: TextContent("hi")},
		},
		Stream: false,
	}
	body, err := p.EncodeRequest(req)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	httpReq, err := http.NewRequest(http.MethodPost, p.ChatEndpoint(), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for k, v := range p.Headers() {
		httpReq.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("chat request: %v", err)
	}
	defer resp.Body.Close()
	chatBody, _ := io.ReadAll(resp.Body)
	chatResp, err := p.DecodeResponse(chatBody)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if len(chatResp.Choices) == 0 || chatResp.Choices[0].Message.ContentString() != "hello" {
		t.Fatalf("chat response = %+v", chatResp)
	}
	if backend.gotChatBody["model"] != "gpt-5.6" {
		t.Errorf("chat body model = %v", backend.gotChatBody["model"])
	}
	// The codex backend requires store:false and stream:true; without them it
	// returns HTTP 400 (verified against the live backend).
	if store, _ := backend.gotChatBody["store"].(bool); store {
		t.Errorf("chat body store = %v, want false", store)
	}
	if stream, _ := backend.gotChatBody["stream"].(bool); !stream {
		t.Errorf("chat body stream = %v, want true", stream)
	}

	// 4. Expire the token and verify EnsureAuth refreshes it.
	p.oauth.ExpiresAt = time.Now().Unix() - 10
	backend.accessToken = "AT-2"
	if err := p.EnsureAuth(context.Background()); err != nil {
		t.Fatalf("EnsureAuth: %v", err)
	}
	if p.oauth.AccessToken != "AT-2" {
		t.Errorf("access token after refresh = %q, want AT-2", p.oauth.AccessToken)
	}
	if backend.refreshCalls == 0 {
		t.Errorf("refresh endpoint never called")
	}
}
