package turnloop

import (
	"strings"
	"testing"

	"crude/agent/provider"
	"crude/internal/providersim"
)

// TestCompleteStreamAttachesOAuth verifies a chat request to the codex backend
// carries the OAuth bearer token and the codex headers (originator,
// chatgpt-account-id, session-id, thread-id); this covers the path that produced
// "Authentication failed" when the token was missing.
func TestCompleteStreamAttachesOAuth(t *testing.T) {
	var gotAuth, gotAccountID, gotOriginator, gotSessionID, gotThreadID, gotUserAgent string
	srv := providersim.New(t)
	srv.Respond(func(r providersim.Request) providersim.Turn {
		gotAuth = r.Header.Get("Authorization")
		gotAccountID = r.Header.Get("chatgpt-account-id")
		gotOriginator = r.Header.Get("originator")
		gotSessionID = r.Header.Get("session-id")
		gotThreadID = r.Header.Get("thread-id")
		gotUserAgent = r.Header.Get("User-Agent")
		return providersim.Text("hi").WithUsage(providersim.Usage{Input: 1, Output: 1})
	})

	p := provider.FromPreset("chatgpt", "", srv.BaseURL(providersim.Codex))
	p.SetOAuth(&provider.OAuthCredentials{
		AccessToken: "AT-oauth",
		AccountID:   "acct_1",
	})
	p.Model = "gpt-5.6"

	agent := newTestAgent(t, p, "gpt-5.6")
	_, err := completeForTest(t, agent)
	if err != nil {
		t.Fatalf("completeStream: %v", err)
	}

	if gotAuth != "Bearer AT-oauth" {
		t.Errorf("Authorization = %q, want Bearer AT-oauth (OAuth not attached)", gotAuth)
	}
	if gotAccountID != "acct_1" {
		t.Errorf("chatgpt-account-id = %q, want acct_1", gotAccountID)
	}
	if gotOriginator != "crude" {
		t.Errorf("originator = %q, want crude", gotOriginator)
	}
	if gotSessionID == "" || gotThreadID == "" {
		t.Errorf("session-id/thread-id missing: %q %q", gotSessionID, gotThreadID)
	}
	if !strings.Contains(gotUserAgent, "crude/") {
		t.Errorf("User-Agent = %q, want crude/...", gotUserAgent)
	}
}
