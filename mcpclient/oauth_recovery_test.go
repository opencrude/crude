package mcpclient

import (
	"errors"
	"net"
	"net/http"
	"syscall"
	"testing"

	"golang.org/x/oauth2"
)

type recoveryTokenSource struct {
	calls int
	err   error
}

func (source *recoveryTokenSource) Token() (*oauth2.Token, error) {
	source.calls++
	if source.calls == 1 {
		return nil, source.err
	}
	return &oauth2.Token{AccessToken: "replacement", RefreshToken: "retained"}, nil
}

func TestTransientOAuthRefreshDoesNotPoisonTheConnection(t *testing.T) {
	manager := New(t.TempDir())
	manager.SetCredentialDirectory(t.TempDir())
	upstream := &recoveryTokenSource{err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}}
	source := &durableTokenSource{source: upstream, manager: manager, credential: credential{ID: "server", Endpoint: "https://mcp.example"}}
	if _, err := source.Token(); err == nil || errors.Is(err, errNeedsSignIn) || source.err != nil {
		t.Fatalf("temporary outage was cached as expired authentication: %v", err)
	}
	token, err := source.Token()
	if err != nil || token.AccessToken != "replacement" || upstream.calls != 2 {
		t.Fatalf("retained source did not recover: token=%v err=%v calls=%d", token, err, upstream.calls)
	}
}

func TestPermanentOAuthFailureRequiresSignInWithoutRepeatedRequests(t *testing.T) {
	for _, rejected := range []*oauth2.RetrieveError{
		{ErrorCode: "invalid_grant"},
		{Response: &http.Response{StatusCode: http.StatusUnauthorized}},
	} {
		upstream := &recoveryTokenSource{err: rejected}
		source := &durableTokenSource{source: upstream}
		for range 2 {
			if _, err := source.Token(); !errors.Is(err, errNeedsSignIn) {
				t.Fatalf("permanent authentication did not require sign-in: %v", err)
			}
		}
		if upstream.calls != 1 {
			t.Fatal("permanent rejection was repeatedly retried")
		}
	}
}
