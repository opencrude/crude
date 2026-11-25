package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	captypes "crude/types/capability"
)

const clientSecretCanary = "synthetic-oauth-secret-canary"
const metadataTestID = "https://client.example.test/crude.json"

type conformanceOAuthFixture struct {
	issuer, resource, unrelated *httptest.Server
	mu                          sync.Mutex
	codes, tokens               map[string]string
	tokenTarget                 atomic.Int32
	challengeMode               atomic.Int32
	auths, exchanges, refreshes atomic.Int32
	toolRequests, effects       atomic.Int32
	forbidden, secretLeaks      atomic.Int32
}

func newConformanceOAuthFixture(t *testing.T, secret bool) *conformanceOAuthFixture {
	t.Helper()
	f := &conformanceOAuthFixture{codes: map[string]string{}, tokens: map[string]string{}}
	server := workflowServer(t.TempDir())
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
	f.unrelated = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.forbidden.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	f.resource = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			workflowWriteJSON(w, map[string]any{"resource": f.resource.URL + "/mcp", "authorization_servers": []string{f.issuer.URL}, "scopes_supported": []string{"unrelated"}})
		case "/mcp":
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			var message struct{ Method string }
			_ = json.Unmarshal(body, &message)
			if _, _, hasBasic := r.BasicAuth(); hasBasic || bytes.Contains(body, []byte(clientSecretCanary)) {
				f.secretLeaks.Add(1)
			}
			f.mu.Lock()
			scopes, valid := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
			f.mu.Unlock()
			hint := fmt.Sprintf("resource_metadata=%q", f.resource.URL+"/.well-known/oauth-protected-resource/mcp")
			if !valid {
				w.Header().Set("WWW-Authenticate", "Bearer "+hint+`, scope="files:read"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if message.Method == "tools/call" {
				f.toolRequests.Add(1)
				if !slices.Contains(strings.Fields(scopes), "files:write") {
					if f.challengeMode.Load() == 0 {
						w.Header().Set("WWW-Authenticate", `Basic realm="other", Bearer error="insufficient_scope", scope="files:write", `+hint)
					}
					w.WriteHeader(http.StatusForbidden)
					return
				}
				f.effects.Add(1)
			}
			transport.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	f.issuer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientID, authMethod := metadataTestID, "none"
		if secret {
			clientID, authMethod = "registered-client", "client_secret_basic"
		}
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			token := f.issuer.URL + "/token"
			switch f.tokenTarget.Load() {
			case 1:
				token = f.unrelated.URL + "/token"
			case 2:
				token = f.resource.URL + "/mcp"
			}
			workflowWriteJSON(w, map[string]any{
				"issuer": f.issuer.URL, "authorization_endpoint": f.issuer.URL + "/authorize", "token_endpoint": token,
				"token_endpoint_auth_methods_supported": []string{authMethod}, "code_challenge_methods_supported": []string{"S256"},
				"client_id_metadata_document_supported": true, "authorization_response_iss_parameter_supported": true,
			})
		case "/authorize":
			query := r.URL.Query()
			if query.Get("client_id") != clientID || query.Get("code_challenge_method") != "S256" || query.Get("state") == "" ||
				query.Get("resource") != f.resource.URL+"/mcp" || strings.Contains(r.URL.String(), clientSecretCanary) {
				http.Error(w, "unexpected authorization request", http.StatusBadRequest)
				return
			}
			redirect, err := url.Parse(query.Get("redirect_uri"))
			if err != nil || redirect.Hostname() != "127.0.0.1" || redirect.Path != "/callback" {
				http.Error(w, "unexpected callback", http.StatusBadRequest)
				return
			}
			code := fmt.Sprintf("code-%d", f.auths.Add(1))
			f.mu.Lock()
			f.codes[code] = query.Get("scope")
			f.mu.Unlock()
			returned := redirect.Query()
			returned.Set("code", code)
			returned.Set("state", query.Get("state"))
			returned.Set("iss", f.issuer.URL)
			redirect.RawQuery = returned.Encode()
			http.Redirect(w, r, redirect.String(), http.StatusFound)
		case "/token":
			u, p, basic := r.BasicAuth()
			_ = r.ParseForm()
			if secret && (!basic || u != clientID || p != clientSecretCanary) || !secret && (basic || r.Form.Get("client_id") != clientID) {
				http.Error(w, "incorrect client authentication", http.StatusBadRequest)
				return
			}
			if f.tokenTarget.Load() == 3 {
				http.Redirect(w, r, f.unrelated.URL+"/token", http.StatusTemporaryRedirect)
				return
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			var scope string
			if r.Form.Get("grant_type") == "refresh_token" {
				f.refreshes.Add(1)
				scope = f.tokens[r.Form.Get("refresh_token")]
			} else {
				scope = f.codes[r.Form.Get("code")]
			}
			if scope == "" {
				http.Error(w, "unknown token grant", http.StatusBadRequest)
				return
			}
			token := fmt.Sprintf("access-%d", f.exchanges.Add(1))
			f.tokens[token] = scope
			workflowWriteJSON(w, map[string]any{"access_token": token, "refresh_token": token, "token_type": "Bearer", "expires_in": 3600, "scope": scope})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.issuer.Close)
	t.Cleanup(f.resource.Close)
	t.Cleanup(f.unrelated.Close)
	return f
}

func (f *conformanceOAuthFixture) config(secret bool) captypes.ConnectionConfig {
	config := captypes.ConnectionConfig{ID: "oauth", Name: "Fixture", Auth: "oauth", Transport: "http", URL: f.resource.URL + "/mcp", TimeoutSeconds: 3}
	if secret {
		config.OAuthClientID, config.OAuthIssuer, config.CredentialRef = "registered-client", f.issuer.URL, "secret:OAUTH_TEST"
	} else {
		config.OAuthMetadataURL = metadataTestID
	}
	return config
}

func completeOAuth(t *testing.T, manager *Manager, id string) {
	t.Helper()
	browser, done, _ := workflowStartSignIn(t, manager, id)
	if err := manager.SubmitAuthorization(id, workflowAuthorizationReturn(t, browser)); err != nil {
		t.Fatal(err)
	}
	if err := workflowAwaitError(t, "explicit Sign in", done); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthStepUpRequiresSignInAndDoesNotReplay(t *testing.T) {
	f := newConformanceOAuthFixture(t, false)
	config := f.config(false)
	config.OAuthScopes = []string{"files:read"} // A baseline setting cannot suppress the later challenge.
	manager := workflowOAuthManager(t, t.TempDir(), config)
	if err := manager.Connect(t.Context(), config.ID); !errors.Is(err, errNeedsSignIn) || f.auths.Load() != 0 || f.exchanges.Load() != 0 {
		t.Fatalf("ordinary Connect started sign-in or returned the wrong status: %v", err)
	}
	completeOAuth(t, manager, config.ID)
	call := func() error {
		tool := workflowTool(t, manager, config.ID, "inspect")
		_, err := manager.Call(t.Context(), tool, map[string]any{"message": "oauth test"})
		return err
	}
	f.challengeMode.Store(1)
	if err := call(); err == nil || workflowState(t, manager, config.ID).State != "connected" {
		t.Fatalf("plain 403 became an authorization prompt: %v", err)
	}
	f.challengeMode.Store(0)
	if err := call(); !errors.Is(err, errNeedsSignIn) {
		t.Fatalf("scope rejection did not request sign-in: %v", err)
	}
	state := workflowState(t, manager, config.ID)
	if state.State != "needs sign-in" || state.AuthPending || !strings.Contains(state.Error, "files:write") ||
		f.auths.Load() != 1 || f.exchanges.Load() != 1 || f.toolRequests.Load() != 2 {
		t.Fatal("step-up was not retained visibly, or an unapproved request was sent")
	}
	if err := manager.Connect(t.Context(), config.ID); !errors.Is(err, errNeedsSignIn) || f.auths.Load() != 1 {
		t.Fatalf("ordinary Connect consumed the step-up challenge: %v", err)
	}
	browser, done, cancel := workflowStartSignIn(t, manager, config.ID)
	auth, _ := url.Parse(browser)
	// OAuth scopes form a set (RFC 6749 §3.3). Check every member, not the SDK's map order.
	got := strings.Fields(auth.Query().Get("scope"))
	slices.Sort(got)
	if !slices.Equal(got, []string{"files:read", "files:write"}) {
		t.Fatalf("reviewed scope request = %q", got)
	}
	cancel()
	if err := workflowAwaitError(t, "cancel step-up", done); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation result = %v", err)
	}
	browser, done, _ = workflowStartSignIn(t, manager, config.ID)
	auth, _ = url.Parse(browser)
	redirect, _ := url.Parse(auth.Query().Get("redirect_uri"))
	declined := url.Values{"error": {"access_denied"}, "state": {auth.Query().Get("state")}}
	redirect.RawQuery = declined.Encode()
	if err := manager.SubmitAuthorization(config.ID, redirect.String()); err != nil {
		t.Fatal(err)
	}
	if err := workflowAwaitError(t, "refuse step-up", done); err == nil || !strings.Contains(err.Error(), "declined") || f.exchanges.Load() != 1 {
		t.Fatalf("refused consent changed credentials: %v", err)
	}
	completeOAuth(t, manager, config.ID)
	if f.toolRequests.Load() != 2 || f.effects.Load() != 0 {
		t.Fatal("successful sign-in replayed an earlier tool request")
	}
	if err := call(); err != nil || f.effects.Load() != 1 {
		t.Fatalf("a new call did not use the reviewed scope: %v", err)
	}
}

func TestOAuthPreregisteredSecretOnlyReachesIssuerTokenEndpoint(t *testing.T) {
	f := newConformanceOAuthFixture(t, true)
	config := f.config(true)
	vault := t.TempDir()
	manager := workflowOAuthManager(t, vault, config)
	if err := manager.SetSecrets(map[string]string{"OAUTH_TEST": clientSecretCanary}); err != nil {
		t.Fatal(err)
	}
	completeOAuth(t, manager, config.ID)
	path := workflowCredentialPath(vault, config)
	data, err := os.ReadFile(path)
	if err != nil || bytes.Contains(data, []byte(clientSecretCanary)) {
		t.Fatal("client secret was duplicated into the token credential")
	}
	var saved credential
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	saved.Token.Expiry = time.Now().Add(-time.Hour)
	manager.Close()
	data, _ = json.Marshal(saved)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	restored := workflowOAuthManager(t, vault, config)
	if err := restored.Connect(t.Context(), config.ID); err != nil || f.refreshes.Load() != 1 || f.secretLeaks.Load() != 0 {
		t.Fatalf("saved credential did not refresh only at its issuer: %v", err)
	}
	other := newConformanceOAuthFixture(t, true)
	config.OAuthIssuer = other.issuer.URL
	restored.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if err := restored.Connect(t.Context(), config.ID); !errors.Is(err, errNeedsSignIn) || f.refreshes.Load() != 1 {
		t.Fatalf("saved credential was not bound to the original issuer: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := restored.SignIn(ctx, config.ID); err == nil || !strings.Contains(err.Error(), "issuer") || f.exchanges.Load() != 2 || other.exchanges.Load() != 0 {
		t.Fatalf("wrong discovered issuer reached authorization or token exchange: %v", err)
	}
}

func TestOAuthPreregisteredSecretRejectsWrongTokenDestination(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode int32
	}{{"unrelated issuer", 1}, {"resource endpoint", 2}, {"redirect", 3}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newConformanceOAuthFixture(t, true)
			f.tokenTarget.Store(tc.mode)
			config := f.config(true)
			manager := workflowOAuthManager(t, t.TempDir(), config)
			if err := manager.SetSecrets(map[string]string{"OAUTH_TEST": clientSecretCanary}); err != nil {
				t.Fatal(err)
			}
			var err error
			if tc.mode == 3 {
				browser, done, _ := workflowStartSignIn(t, manager, config.ID)
				if err := manager.SubmitAuthorization(config.ID, workflowAuthorizationReturn(t, browser)); err != nil {
					t.Fatal(err)
				}
				err = workflowAwaitError(t, "reject redirected token exchange", done)
			} else {
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				err = manager.SignIn(ctx, config.ID)
			}
			if err == nil || strings.Contains(err.Error(), clientSecretCanary) || f.exchanges.Load() != 0 ||
				f.secretLeaks.Load() != 0 || f.forbidden.Load() != 0 || workflowState(t, manager, config.ID).AuthPending {
				t.Fatalf("invalid token destination was not rejected cleanly: %v", err)
			}
		})
	}
}

func TestOAuthReplacedRegistrationCannotCompleteOldSignIn(t *testing.T) {
	f := newConformanceOAuthFixture(t, false)
	config := f.config(false)
	vault := t.TempDir()
	manager := workflowOAuthManager(t, vault, config)
	browser, done, _ := workflowStartSignIn(t, manager, config.ID)
	stale := workflowAuthorizationReturn(t, browser)
	manager.Configure(nil)
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if err := manager.SubmitAuthorization(config.ID, stale); err == nil {
		t.Fatal("a stale browser return was accepted by a replacement registration")
	}
	if err := workflowAwaitError(t, "superseded sign-in", done); err == nil {
		t.Fatal("superseded sign-in completed")
	}
	if _, err := os.Stat(workflowCredentialPath(vault, config)); !errors.Is(err, os.ErrNotExist) || f.exchanges.Load() != 0 {
		t.Fatal("a stale sign-in exchanged or published credentials")
	}
	completeOAuth(t, manager, config.ID)
}

func TestOAuthInvalidClientRegistrationFailsBeforeSignIn(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*captypes.ConnectionConfig)
	}{
		{"client secret without issuer", func(c *captypes.ConnectionConfig) { c.OAuthClientID, c.CredentialRef = "native", "secret:TEST" }},
		{"raw client secret", func(c *captypes.ConnectionConfig) {
			c.OAuthClientID, c.CredentialRef, c.OAuthIssuer = "native", clientSecretCanary, "https://issuer.example.test"
		}},
		{"insecure issuer", func(c *captypes.ConnectionConfig) {
			c.OAuthClientID, c.CredentialRef, c.OAuthIssuer = "native", "secret:TEST", "http://issuer.example.test"
		}},
		{"metadata ID uses query data", func(c *captypes.ConnectionConfig) { c.OAuthMetadataURL = metadataTestID + "?secret=opaque" }},
		{"metadata ID uses credentials", func(c *captypes.ConnectionConfig) {
			c.OAuthMetadataURL = "https://secret@client.example.test/crude.json"
		}},
		{"both registration methods", func(c *captypes.ConnectionConfig) { c.OAuthMetadataURL, c.OAuthClientID = metadataTestID, "native" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := captypes.ConnectionConfig{ID: "oauth", Name: "Fixture", Transport: "http", URL: "http://127.0.0.1:1/mcp", Auth: "oauth"}
			tc.apply(&config)
			if err := Validate(config); err == nil || strings.Contains(err.Error(), clientSecretCanary) || strings.Contains(err.Error(), "opaque") {
				t.Fatalf("invalid or unredacted settings: %v", err)
			}
		})
	}
}
