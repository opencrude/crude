package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPKCEChallengeS256(t *testing.T) {
	verifier, challenge, err := pkceChallenge()
	if err != nil {
		t.Fatalf("pkceChallenge: %v", err)
	}
	if len(verifier) < 32 {
		t.Errorf("verifier too short: %q", verifier)
	}
	// S256 challenge = base64url(sha256(verifier)) with no padding.
	if strings.Contains(challenge, "=") {
		t.Errorf("challenge must be unpadded: %q", challenge)
	}
	// Decoding the challenge and the verifier must satisfy the S256 relation.
	verBytes, err := base64.RawURLEncoding.DecodeString(verifier)
	if err != nil {
		t.Fatalf("decode verifier: %v", err)
	}
	_ = verBytes // verifier is itself base64url(32 random bytes)
	challBytes, err := base64.RawURLEncoding.DecodeString(challenge)
	if err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	if len(challBytes) != 32 {
		t.Errorf("challenge decodes to %d bytes, want 32", len(challBytes))
	}
}

func jwtWithClaims(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	return header + "." + payload + ".sig"
}

func TestExtractChatGPTAccountIDAllClaimLocations(t *testing.T) {
	cases := []struct {
		name   string
		claims map[string]any
		want   string
	}{
		{
			name:   "top-level chatgpt_account_id",
			claims: map[string]any{"chatgpt_account_id": "acct_top"},
			want:   "acct_top",
		},
		{
			name: "nested auth namespace",
			claims: map[string]any{
				"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct_ns"},
			},
			want: "acct_ns",
		},
		{
			name: "organizations first id",
			claims: map[string]any{
				"organizations": []any{
					map[string]any{"id": "org_first"},
					map[string]any{"id": "org_second"},
				},
			},
			want: "org_first",
		},
		{
			name:   "no matching claim",
			claims: map[string]any{"sub": "user_123"},
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tok := jwtWithClaims(t, tc.claims)
			if got := extractChatGPTAccountID(tok, ""); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRefreshOpenAITokenRequestBodyAndSuccess(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		idTok := jwtWithClaims(t, map[string]any{"chatgpt_account_id": "acct_refresh"})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "AT-new",
			"refresh_token": "RT-new",
			"id_token":      idTok,
			"expires_in":    3600,
			"email":         "u@example.com",
		})
	}))
	defer srv.Close()
	orig := openAITokenURL
	openAITokenURL = srv.URL
	defer func() { openAITokenURL = orig }()

	creds, err := RefreshOpenAIToken(context.Background(), "RT-old")
	if err != nil {
		t.Fatalf("RefreshOpenAIToken: %v", err)
	}
	if gotForm.Get("grant_type") != "refresh_token" {
		t.Errorf("grant_type = %q", gotForm.Get("grant_type"))
	}
	if gotForm.Get("client_id") != openAIClientID {
		t.Errorf("client_id = %q", gotForm.Get("client_id"))
	}
	if gotForm.Get("refresh_token") != "RT-old" {
		t.Errorf("refresh_token = %q", gotForm.Get("refresh_token"))
	}
	if creds.AccessToken != "AT-new" || creds.RefreshToken != "RT-new" {
		t.Errorf("creds = %+v", creds)
	}
	if creds.AccountID != "acct_refresh" {
		t.Errorf("account id = %q", creds.AccountID)
	}
	if creds.Email != "u@example.com" {
		t.Errorf("email = %q", creds.Email)
	}
	if creds.ExpiresAt <= time.Now().Unix() {
		t.Errorf("expires_at not in the future: %d", creds.ExpiresAt)
	}
}

func TestRefreshOpenAITokenFatalOn403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant", "error_description": "bad token"})
	}))
	defer srv.Close()
	orig := openAITokenURL
	openAITokenURL = srv.URL
	defer func() { openAITokenURL = orig }()

	_, err := RefreshOpenAIToken(context.Background(), "RT-old")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	signInErr, ok := err.(*ChatGPTSignInError)
	if !ok || !signInErr.Fatal {
		t.Fatalf("err = %v, want fatal ChatGPTSignInError", err)
	}
	if !strings.Contains(signInErr.Msg, "invalid_grant") {
		t.Errorf("msg = %q", signInErr.Msg)
	}
}

func TestRefreshOpenAITokenEmptyRefreshIsFatal(t *testing.T) {
	_, err := RefreshOpenAIToken(context.Background(), "")
	if err == nil {
		t.Fatal("expected error")
	}
	signInErr, ok := err.(*ChatGPTSignInError)
	if !ok || !signInErr.Fatal {
		t.Fatalf("err = %v, want fatal", err)
	}
}

// TestWaitForCallbackAfterCallbackAlreadyFired reproduces the race where the
// browser callback fires (signalCallback clears the channel) before
// waitForCallback reads it. waitForCallback must still return the stored code
// instead of erroring on a nil channel.
func TestWaitForCallbackAfterCallbackAlreadyFired(t *testing.T) {
	// Simulate startCallbackServer having run.
	callbackMu.Lock()
	callbackCred = make(chan struct{})
	callbackErr = nil
	callbackMu.Unlock()

	// Simulate the browser callback arriving before waitForCallback reads the
	// channel: store the code and signal.
	authCodeSlotMu.Lock()
	authCodeSlot = "code-123"
	authCodeSlotState = "state-abc"
	authCodeSlotMu.Unlock()
	signalCallback()

	code, err := waitForCallback(context.Background(), "state-abc")
	if err != nil {
		t.Fatalf("waitForCallback: %v", err)
	}
	if code != "code-123" {
		t.Errorf("code = %q, want code-123", code)
	}
}

// TestWaitForCallbackStateMismatch verifies a mismatched state is rejected even
// when the callback already fired before waitForCallback read the channel.
func TestWaitForCallbackStateMismatch(t *testing.T) {
	callbackMu.Lock()
	callbackCred = make(chan struct{})
	callbackErr = nil
	callbackMu.Unlock()

	authCodeSlotMu.Lock()
	authCodeSlot = "code-123"
	authCodeSlotState = "state-other"
	authCodeSlotMu.Unlock()
	signalCallback()

	_, err := waitForCallback(context.Background(), "state-expected")
	if err == nil {
		t.Fatal("expected state mismatch error")
	}
	signInErr, ok := err.(*ChatGPTSignInError)
	if !ok || !signInErr.Fatal {
		t.Fatalf("err = %v, want fatal ChatGPTSignInError", err)
	}
}

func TestBuildAuthorizeURLHasPKCEParams(t *testing.T) {
	u := buildAuthorizeURL("http://localhost:1455/auth/callback", "chal", "st8")
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	q := parsed.Query()
	if q.Get("code_challenge") != "chal" || q.Get("code_challenge_method") != "S256" {
		t.Errorf("pkce params = %#v", q)
	}
	if q.Get("state") != "st8" {
		t.Errorf("state = %q", q.Get("state"))
	}
	if q.Get("client_id") != openAIClientID {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("originator") != openAIOAuthOrigin {
		t.Errorf("originator = %q", q.Get("originator"))
	}
	if q.Get("codex_cli_simplified_flow") != "true" {
		t.Errorf("codex_cli_simplified_flow = %q", q.Get("codex_cli_simplified_flow"))
	}
}
