package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"

	"crude/network"
	captypes "crude/types/capability"
)

type credential struct {
	Endpoint string
	ID       string
	Config   oauth2.Config
	Token    oauth2.Token
}

var errNeedsSignIn = errors.New("MCP needs sign-in; use explicit Sign in")

type authorizationFlow struct {
	callback chan string
	redirect string
	epoch    uint64
}

type durableTokenSource struct {
	mu         sync.Mutex
	source     oauth2.TokenSource
	manager    *Manager
	credential credential
	err        error
}

type tokenRefreshError struct{ cause error }

func (err *tokenRefreshError) Error() string {
	return "MCP token refresh is temporarily unavailable; the connection remains recoverable"
}

func (err *tokenRefreshError) Unwrap() error { return err.cause }

func permanentTokenFailure(err error) bool {
	var rejected *oauth2.RetrieveError
	if !errors.As(err, &rejected) {
		return false
	}
	if rejected.Response != nil && (rejected.Response.StatusCode == http.StatusUnauthorized || rejected.Response.StatusCode == http.StatusForbidden) {
		return true
	}
	switch rejected.ErrorCode {
	case "invalid_grant", "invalid_client", "unauthorized_client", "access_denied":
		return true
	default:
		return false
	}
}

func (source *durableTokenSource) Token() (*oauth2.Token, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.err != nil {
		return nil, source.err
	}
	token, err := source.source.Token()
	if err != nil {
		if permanentTokenFailure(err) {
			source.err = errNeedsSignIn
			return nil, source.err
		}
		// A timeout/outage must not poison the retained credential source.
		// Do not expose token endpoint response bodies or retry a remote write.
		return nil, &tokenRefreshError{cause: err}
	}
	source.credential.Token = *token
	if err := source.manager.saveCredential(source.credential); err != nil {
		return nil, err
	}
	return token, nil
}

func (manager *Manager) SetCredentialDirectory(directory string) { manager.credentialDir = directory }

func (manager *Manager) credentialPath(config captypes.ConnectionConfig) string {
	return filepath.Join(manager.credentialDir, digest([]byte(config.ID+"\n"+config.URL))+".json")
}

func (manager *Manager) saveCredential(saved credential) error {
	manager.credentialMu.Lock()
	defer manager.credentialMu.Unlock()
	if manager.credentialDir == "" {
		return errors.New("application credential vault is unavailable")
	}
	if err := os.MkdirAll(manager.credentialDir, 0700); err != nil {
		return err
	}
	encoded, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(manager.credentialDir, ".credential-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), manager.credentialPath(captypes.ConnectionConfig{ID: saved.ID, URL: saved.Endpoint}))
}

func (manager *Manager) loadCredential(config captypes.ConnectionConfig) (oauth2.TokenSource, error) {
	encoded, err := os.ReadFile(manager.credentialPath(config))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var saved credential
	if err := json.Unmarshal(encoded, &saved); err != nil {
		return nil, errors.New("MCP credential is invalid; sign in again")
	}
	if saved.Endpoint != config.URL || saved.ID != config.ID {
		return nil, errors.New("MCP credential belongs to another endpoint")
	}
	if saved.Config.ClientSecret == "" {
		saved.Config.Endpoint.AuthStyle = oauth2.AuthStyleInParams
	}
	refreshContext := context.WithValue(context.Background(), oauth2.HTTPClient, oauthHTTPClient(config.OAuthClientID))
	return &durableTokenSource{source: saved.Config.TokenSource(refreshContext, &saved.Token), manager: manager, credential: saved}, nil
}

type publicOAuthTransport struct {
	clientID string
	base     http.RoundTripper
}

func (transport publicOAuthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clientID, secret, basic := request.BasicAuth()
	if basic && clientID == transport.clientID && secret == "" && request.Method == http.MethodPost && request.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
		encoded, err := io.ReadAll(io.LimitReader(request.Body, 16<<10+1))
		_ = request.Body.Close()
		if err != nil || len(encoded) > 16<<10 {
			return nil, errors.New("invalid OAuth token request")
		}
		form, err := url.ParseQuery(string(encoded))
		if err != nil {
			return nil, err
		}
		form.Set("client_id", clientID)
		request = request.Clone(request.Context())
		request.Header.Del("Authorization")
		body := form.Encode()
		request.Body = io.NopCloser(strings.NewReader(body))
		request.ContentLength = int64(len(body))
		request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(body)), nil }
	}
	return transport.base.RoundTrip(request)
}

func oauthHTTPClient(clientID string) *http.Client {
	client := network.NewClient(30 * time.Second)
	client.Transport = publicOAuthTransport{clientID: clientID, base: client.Transport}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

type passiveOAuth struct{ source oauth2.TokenSource }

func (handler passiveOAuth) TokenSource(context.Context) (oauth2.TokenSource, error) {
	return handler.source, nil
}
func (handler passiveOAuth) Authorize(_ context.Context, _ *http.Request, response *http.Response) error {
	_ = response.Body.Close()
	return errNeedsSignIn
}

type interactiveOAuth struct {
	mu      sync.Mutex
	handler auth.OAuthHandler
	err     error
	attempt bool
}

func (handler *interactiveOAuth) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	return handler.handler.TokenSource(ctx)
}

func (handler *interactiveOAuth) Authorize(ctx context.Context, request *http.Request, response *http.Response) error {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.attempt {
		_ = response.Body.Close()
		if handler.err != nil {
			return handler.err
		}
		return errNeedsSignIn
	}
	handler.attempt = true
	handler.err = handler.handler.Authorize(ctx, request, response)
	return handler.err
}

func (manager *Manager) oauthHandler(config captypes.ConnectionConfig, lifetime context.Context, epoch uint64, interactive bool) (auth.OAuthHandler, func(), error) {
	cleanup := func() {}
	initial, err := manager.loadCredential(config)
	if err != nil {
		return nil, cleanup, err
	}
	if !interactive {
		return passiveOAuth{source: initial}, cleanup, nil
	}
	initial = nil
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, cleanup, err
	}
	redirect := "http://" + listener.Addr().String() + "/callback"
	callback := make(chan string, 1)
	flow := &authorizationFlow{callback: callback, redirect: redirect, epoch: epoch}
	manager.mu.Lock()
	manager.authCallbacks[config.ID] = flow
	manager.mu.Unlock()
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	var once sync.Once
	cleanup = func() {
		once.Do(func() {
			// Receiving the code wakes the token exchange before this handler has
			// returned. Graceful bounded shutdown lets its HTTP acknowledgement
			// finish rather than racing server.Close against the browser response.
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := server.Shutdown(ctx); err != nil {
				_ = server.Close()
			}
			manager.mu.Lock()
			if manager.authCallbacks[config.ID] == flow {
				delete(manager.authCallbacks, config.ID)
			}
			if entry := manager.connections[config.ID]; entry != nil && entry.epoch == epoch {
				entry.state.AuthPending, entry.state.AuthURL = false, ""
			}
			manager.mu.Unlock()
		})
	}
	stop := context.AfterFunc(lifetime, cleanup)
	closeAuth := func() { stop(); cleanup() }
	server.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/callback" {
			http.NotFound(writer, request)
			return
		}
		select {
		case callback <- redirect + "?" + request.URL.RawQuery:
		default:
		}
		_, _ = writer.Write([]byte("Authorization received. You may return to Crude."))
	})
	go func() { _ = server.Serve(listener) }()
	configuration := &auth.AuthorizationCodeHandlerConfig{
		Client:                          oauthHTTPClient(config.OAuthClientID),
		RedirectURL:                     redirect,
		RequestRefreshToken:             true,
		InitialTokenSource:              initial,
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{RedirectURIs: []string{redirect}, ClientName: "Crude", TokenEndpointAuthMethod: "none", GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"}}},
		AuthorizationCodeFetcher: func(ctx context.Context, arguments *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			defer cleanup()
			manager.mu.Lock()
			if entry := manager.connections[config.ID]; entry != nil && entry.epoch == epoch {
				entry.state.AuthPending, entry.state.AuthURL = true, arguments.URL
			}
			manager.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case returned := <-callback:
				parsed, err := url.Parse(returned)
				if err != nil {
					return nil, errors.New("invalid authorization return URL")
				}
				if parsed.Query().Get("error") != "" {
					return nil, errors.New("MCP authorization was declined")
				}
				return &auth.AuthorizationResult{Code: parsed.Query().Get("code"), State: parsed.Query().Get("state"), Iss: parsed.Query().Get("iss")}, nil
			}
		},
		NewTokenSource: func(ctx context.Context, configuration *oauth2.Config, token *oauth2.Token) (oauth2.TokenSource, error) {
			manager.mu.Lock()
			if entry := manager.connections[config.ID]; entry != nil && entry.epoch == epoch {
				if entry.secrets == nil {
					entry.secrets = make(map[string]struct{})
				}
				entry.secrets[token.AccessToken] = struct{}{}
				entry.secrets[token.RefreshToken] = struct{}{}
			}
			manager.mu.Unlock()
			saved := credential{ID: config.ID, Endpoint: config.URL, Config: *configuration, Token: *token}
			if err := manager.saveCredential(saved); err != nil {
				return nil, err
			}
			return &durableTokenSource{source: configuration.TokenSource(ctx, token), manager: manager, credential: saved}, nil
		},
	}
	if config.OAuthClientID != "" {
		configuration.PreregisteredClient = &oauthex.ClientCredentials{ClientID: config.OAuthClientID}
		configuration.DynamicClientRegistrationConfig = nil
	}
	if len(config.OAuthScopes) > 0 {
		configuration.ScopeFilter = func([]string) []string { return append([]string(nil), config.OAuthScopes...) }
	}
	handler, err := auth.NewAuthorizationCodeHandler(configuration)
	if err != nil {
		closeAuth()
		return nil, func() {}, err
	}
	return &interactiveOAuth{handler: handler}, closeAuth, nil
}

func (manager *Manager) SignIn(ctx context.Context, id string) error {
	_ = manager.Disconnect(id)
	manager.mu.Lock()
	entry := manager.connections[id]
	if entry == nil || entry.config.Auth != "oauth" {
		manager.mu.Unlock()
		return errors.New("select OAuth authentication before signing in")
	}
	manager.mu.Unlock()
	defer func() {
		manager.mu.Lock()
		if entry := manager.connections[id]; entry != nil {
			entry.interactiveAuth = false
		}
		manager.mu.Unlock()
	}()
	return manager.connect(ctx, id, true)
}

func (manager *Manager) SubmitAuthorization(id, callbackURL string) error {
	manager.mu.RLock()
	callback := manager.authCallbacks[id]
	manager.mu.RUnlock()
	if callback == nil {
		return errors.New("no active authorization flow for this connection")
	}
	parsed, err := url.Parse(callbackURL)
	expected, _ := url.Parse(callback.redirect)
	if err != nil || parsed.Scheme != expected.Scheme || parsed.Host != expected.Host || parsed.Path != expected.Path {
		return errors.New("authorization return must match this flow's loopback callback")
	}
	select {
	case callback.callback <- callbackURL:
		return nil
	default:
		return errors.New("authorization callback is already pending")
	}
}

func (manager *Manager) SignOut(id string) error {
	manager.mu.RLock()
	entry := manager.connections[id]
	if entry == nil {
		manager.mu.RUnlock()
		return fmt.Errorf("unknown connection %q", id)
	}
	config := entry.config
	manager.mu.RUnlock()
	if err := manager.Disconnect(id); err != nil {
		return err
	}
	if err := os.Remove(manager.credentialPath(config)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
