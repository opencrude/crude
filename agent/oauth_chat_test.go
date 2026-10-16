package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"crude/provider"
)

// TestCompleteStreamAttachesOAuth verifies a chat request to the codex backend
// carries the OAuth bearer token and the codex headers (originator,
// chatgpt-account-id, session-id, thread-id); this covers the path that produced
// "Authentication failed" when the token was missing.
func TestCompleteStreamAttachesOAuth(t *testing.T) {
	var gotAuth, gotAccountID, gotOriginator, gotSessionID, gotThreadID, gotUserAgent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccountID = r.Header.Get("chatgpt-account-id")
		gotOriginator = r.Header.Get("originator")
		gotSessionID = r.Header.Get("session-id")
		gotThreadID = r.Header.Get("thread-id")
		gotUserAgent = r.Header.Get("User-Agent")
		// Drain the body so the server can respond.
		_ = json.NewDecoder(r.Body).Decode(&map[string]any{})
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + `{"type":"response.output_text.delta","delta":"hi"}` + "\n\n"))
		_, _ = w.Write([]byte("data: " + `{"type":"response.completed","response":{"id":"r1","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	p := provider.FromPreset("chatgpt", "", srv.URL+"/codex")
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
