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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"

	"crude/internal/network"
	captypes "crude/types/capability"
)

type credential struct {
	Endpoint string
	ID       string
	Binding  string
	Config   oauth2.Config
	Token    oauth2.Token
}

var errNeedsSignIn = errors.New("MCP needs sign-in; use explicit Sign in")

func validateOAuth(config captypes.ConnectionConfig) error {
	if config.Auth != "oauth" {
		if config.OAuthClientID != "" || config.OAuthMetadataURL != "" || config.OAuthIssuer != "" || config.CredentialRef != "" || len(config.OAuthScopes) != 0 {
			return errors.New("Select OAuth to save OAuth settings.")
		}
		return nil
	}
	if config.Transport != "http" {
		return errors.New("OAuth requires a Streamable HTTP connection.")
	}
	if config.OAuthMetadataURL != "" {
		u, err := url.Parse(config.OAuthMetadataURL)
		if err != nil || !oauthURL(u) || u.Scheme != "https" || u.Path == "" || u.Path == "/" || u.ForceQuery || u.RawQuery != "" {
			return errors.New("Enter a public HTTPS URL for the OAuth client metadata document.")
		}
		if config.OAuthClientID != "" {
			return errors.New("Enter an OAuth client ID or a metadata URL, not both.")
		}
	}
	if config.OAuthIssuer != "" {
		u, err := url.Parse(config.OAuthIssuer)
		if err != nil || !oauthURL(u) || u.ForceQuery || u.RawQuery != "" || config.OAuthClientID == "" {
			return errors.New("Enter a valid issuer URL and a registered OAuth client ID.")
		}
	}
	if config.CredentialRef != "" {
		name, stored := strings.CutPrefix(config.CredentialRef, "secret:")
		if !stored || !ValidSecretName(name) || config.OAuthIssuer == "" {
			return errors.New("A client secret needs a secret:NAME reference, a registered client ID and its issuer URL.")
		}
	}
	return nil
}

func oauthURL(u *url.URL) bool {
	if u == nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback())
}

func oauthBinding(config captypes.ConnectionConfig) string {
	binding, _ := json.Marshal([]any{config.Scope, config.OAuthClientID, config.OAuthMetadataURL, config.OAuthIssuer, config.CredentialRef, config.OAuthScopes})
	return digest(binding)
}

func publicClientID(config captypes.ConnectionConfig) string {
	if config.OAuthMetadataURL != "" {
		return config.OAuthMetadataURL
	}
	return config.OAuthClientID
}

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
	endpoint   oauth2.Endpoint
	scopes     []string
	last       *oauth2.Token
	err        error
	lifetime   context.Context
	epoch      uint64
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
	if source.lifetime != nil && source.lifetime.Err() != nil {
		return nil, source.lifetime.Err()
	}
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
	if token != source.last {
		saved := source.credential
		saved.Token = *token
		if err := source.manager.persistToken(source.lifetime, source.epoch, saved); err != nil {
			return nil, err
		}
		source.credential.Token, source.last = *token, token
	}
	return token, nil
}

func (manager *Manager) SetCredentialDirectory(directory string) { manager.credentialDir = directory }

func (manager *Manager) credentialPath(config captypes.ConnectionConfig) string {
	return filepath.Join(manager.credentialDir, digest([]byte(config.ID+"\n"+config.URL))+".json")
}

func (manager *Manager) persistToken(ctx context.Context, epoch uint64, saved credential) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
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
	// Stage the sync without blocking discovery or inspection. Only the exact
	// surviving connection may publish the credential or its redaction values.
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry := manager.connections[saved.ID]
	if ctx != nil && (ctx.Err() != nil || manager.closed || entry == nil || entry.epoch != epoch) {
		return context.Canceled
	}
	if err := os.Rename(temporary.Name(), manager.credentialPath(captypes.ConnectionConfig{ID: saved.ID, URL: saved.Endpoint})); err != nil {
		return err
	}
	if entry != nil {
		entry.rememberToken(saved.Token)
	}
	return nil
}

func (entry *connection) rememberToken(token oauth2.Token) {
	if entry.secrets == nil {
		entry.secrets = make(map[string]struct{})
	}
	entry.secrets[token.AccessToken] = struct{}{}
	entry.secrets[token.RefreshToken] = struct{}{}
}

func (manager *Manager) loadCredential(config captypes.ConnectionConfig) (*credential, error) {
	if manager.credentialDir == "" {
		return nil, nil
	}
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
	if saved.Binding != oauthBinding(config) || config.CredentialRef != "" && saved.Config.ClientSecret != "" {
		return nil, errNeedsSignIn
	}
	return &saved, nil
}

type oauthTransport struct {
	clientID string
	base     http.RoundTripper
	tokenURL string
}

func (transport oauthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if transport.tokenURL != "" && request.Method == http.MethodPost && request.Header.Get("Content-Type") == "application/x-www-form-urlencoded" && request.URL.String() != transport.tokenURL {
		_ = request.Body.Close()
		return nil, errors.New("The OAuth token endpoint changed. Check MCP settings before you use Sign in.")
	}
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

func (transport oauthTransport) CloseIdleConnections() {
	if base, ok := transport.base.(interface{ CloseIdleConnections() }); ok {
		base.CloseIdleConnections()
	}
}

func oauthHTTPClient(clientID, tokenURL string) *http.Client {
	client := network.NewClient(30 * time.Second)
	client.Transport = oauthTransport{clientID: clientID, base: client.Transport, tokenURL: tokenURL}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

type scopeChallenge struct {
	scopes        []string
	authEndpoint  string
	tokenEndpoint string
	resourceHint  string
}

func (challenge *scopeChallenge) err() error {
	return scopeChallengeError{scopes: strings.Join(challenge.scopes, ", ")}
}

type scopeChallengeError struct{ scopes string }

func (e scopeChallengeError) Error() string {
	return "MCP needs access to " + e.scopes + ". Select Sign in in MCP settings to review access."
}
func (e scopeChallengeError) Unwrap() error { return errNeedsSignIn }

type oauthAccess struct {
	attemptMu sync.Mutex
	start     func(context.Context, *http.Request, *http.Response) error
	authErr   error
	mu        sync.Mutex
	source    *durableTokenSource
	manager   *Manager
	config    captypes.ConnectionConfig
	lifetime  context.Context
	epoch     uint64
}

func (access *oauthAccess) TokenSource(context.Context) (oauth2.TokenSource, error) {
	access.mu.Lock()
	defer access.mu.Unlock()
	if access.source == nil {
		return nil, nil
	}
	return access.source, nil
}

func (access *oauthAccess) Authorize(ctx context.Context, request *http.Request, response *http.Response) error {
	access.attemptMu.Lock()
	defer access.attemptMu.Unlock()
	params := bearerChallenge(response)
	stepUp := response.StatusCode == http.StatusForbidden && params["error"] == "insufficient_scope"
	start := access.start
	access.start = nil // One handshake per deliberate Sign in. Never retry a tool call.
	if start != nil && (response.StatusCode == http.StatusUnauthorized || stepUp) {
		access.authErr = start(ctx, request, response)
		return access.authErr
	}
	_ = response.Body.Close()
	if access.authErr != nil {
		return access.authErr
	}
	access.mu.Lock()
	source := access.source
	access.mu.Unlock()
	if stepUp && source != nil && request.URL.String() == access.config.URL && ctx.Err() == nil {
		if scopes := challengedScopes(params["scope"]); len(scopes) != 0 {
			return access.manager.retainScopeChallenge(access, source.endpoint, source.scopes, scopes, params["resource_metadata"])
		}
	}
	if response.StatusCode == http.StatusForbidden && !stepUp {
		return errors.New("The MCP server denied access.")
	}
	return errNeedsSignIn
}

func bearerChallenge(response *http.Response) map[string]string {
	challenges, err := oauthex.ParseWWWAuthenticate(response.Header.Values("WWW-Authenticate"))
	if err != nil {
		return nil
	}
	var params map[string]string
	for _, challenge := range challenges {
		if challenge.Scheme == "bearer" {
			if params != nil {
				return nil
			}
			params = challenge.Params
		}
	}
	return params
}

func challengedScopes(raw string) []string {
	// RFC 6750 scope tokens are ASCII with spaces between tokens. Do not
	// present arbitrary response text as an access request.
	if len(raw) > 2048 {
		return nil
	}
	for _, c := range raw {
		if c < ' ' || c > '~' || c == '"' || c == '\\' {
			return nil
		}
	}
	return strings.Fields(raw)
}

func (manager *Manager) retainScopeChallenge(access *oauthAccess, endpoint oauth2.Endpoint, previous, scopes []string, hint string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry := manager.connections[access.config.ID]
	if manager.closed || entry == nil || entry.epoch != access.epoch || access.lifetime.Err() != nil {
		return context.Canceled
	}
	union := slices.Clone(previous)
	for _, scope := range scopes {
		if !slices.Contains(union, scope) {
			union = append(union, scope)
		}
	}
	if len(union) == len(previous) {
		return errors.New("The MCP server denied access with the current OAuth scopes.")
	}
	if entry.scopeChallenge == nil {
		entry.scopeChallenge = &scopeChallenge{scopes: union, authEndpoint: endpoint.AuthURL, tokenEndpoint: endpoint.TokenURL, resourceHint: hint}
	}
	manager.showScopeChallenge(entry)
	return entry.scopeChallenge.err()
}

func (manager *Manager) showScopeChallenge(entry *connection) {
	entry.state.State, entry.state.Cached = "needs sign-in", hasCatalog(entry.state)
	entry.state.Error = redactSecrets(entry.config, entry.secrets, manager.resolveSecret, entry.scopeChallenge.err().Error())
}

func (manager *Manager) newTokenSource(lifetime context.Context, epoch uint64, saved credential, configuration oauth2.Config, client *http.Client) *durableTokenSource {
	refreshContext := context.WithValue(lifetime, oauth2.HTTPClient, client)
	initial := &saved.Token
	return &durableTokenSource{source: configuration.TokenSource(refreshContext, initial), manager: manager, last: initial,
		credential: saved, lifetime: lifetime, epoch: epoch, endpoint: configuration.Endpoint, scopes: slices.Clone(configuration.Scopes)}
}

func (manager *Manager) confidentialTokenEndpoint(ctx context.Context, config captypes.ConnectionConfig) (string, error) {
	client := oauthHTTPClient("", "")
	defer client.CloseIdleConnections()
	metadata, err := auth.GetAuthServerMetadata(ctx, config.OAuthIssuer, client)
	if err != nil || metadata == nil {
		return "", errors.New("Could not check the OAuth issuer. Check its URL in MCP settings.")
	}
	if !trustedTokenEndpoint(config, metadata.TokenEndpoint) {
		return "", errors.New("The OAuth token endpoint must belong to the configured issuer, not the MCP resource.")
	}
	return metadata.TokenEndpoint, nil
}

func trustedTokenEndpoint(config captypes.ConnectionConfig, raw string) bool {
	issuer, _ := url.Parse(config.OAuthIssuer)
	resource, _ := url.Parse(config.URL)
	token, err := url.Parse(raw)
	return err == nil && oauthURL(token) && token.Scheme == issuer.Scheme && strings.EqualFold(token.Host, issuer.Host) &&
		!(strings.EqualFold(token.Host, resource.Host) && token.Scheme == resource.Scheme && token.Path == resource.Path)
}

func sameAuthorizationEndpoint(browserURL, previous string) bool {
	current, err := url.Parse(browserURL)
	original, oldErr := url.Parse(previous)
	return err == nil && oldErr == nil && current.Scheme == original.Scheme && current.Host == original.Host && current.EscapedPath() == original.EscapedPath()
}

func (manager *Manager) oauthHandler(ctx context.Context, config captypes.ConnectionConfig, lifetime context.Context, epoch uint64, interactive bool, challenge *scopeChallenge) (auth.OAuthHandler, func(), error) {
	cleanup := func() {}
	access := &oauthAccess{manager: manager, config: config, epoch: epoch, lifetime: lifetime}
	secret := ""
	if config.CredentialRef != "" {
		var err error
		if secret, err = manager.resolveSecret(config.CredentialRef); err != nil {
			return nil, cleanup, errors.New("The OAuth client secret is not set. Check its reference in MCP settings.")
		}
	}
	if !interactive {
		saved, err := manager.loadCredential(config)
		if err != nil {
			return nil, cleanup, err
		}
		if saved != nil {
			tokenURL := ""
			credentialConfig := saved.Config
			if secret != "" {
				tokenURL = credentialConfig.Endpoint.TokenURL
				if !trustedTokenEndpoint(config, tokenURL) {
					return nil, cleanup, errNeedsSignIn
				}
				credentialConfig.ClientSecret = secret
			} else if credentialConfig.ClientSecret == "" {
				credentialConfig.Endpoint.AuthStyle = oauth2.AuthStyleInParams
			}
			client := oauthHTTPClient(publicClientID(config), tokenURL)
			context.AfterFunc(lifetime, client.CloseIdleConnections)
			access.source = manager.newTokenSource(lifetime, epoch, *saved, credentialConfig, client)
			manager.mu.Lock()
			if entry := manager.connections[config.ID]; entry != nil && entry.epoch == epoch {
				entry.rememberToken(saved.Token)
			}
			manager.mu.Unlock()
		}
		return access, cleanup, nil
	}
	tokenURL := ""
	if secret != "" {
		var err error
		tokenURL, err = manager.confidentialTokenEndpoint(ctx, config)
		if err != nil {
			return nil, cleanup, err
		}
	}
	if challenge != nil && tokenURL != "" && challenge.tokenEndpoint != tokenURL {
		return nil, cleanup, errors.New("The OAuth token endpoint changed. Select Sign out, then Sign in in MCP settings.")
	}
	if challenge != nil {
		tokenURL = challenge.tokenEndpoint
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, cleanup, err
	}
	redirect := "http://" + listener.Addr().String() + "/callback"
	callback := make(chan string, 1)
	flow := &authorizationFlow{callback: callback, redirect: redirect, epoch: epoch}
	manager.mu.Lock()
	if entry := manager.connections[config.ID]; manager.closed || entry == nil || entry.epoch != epoch || ctx.Err() != nil {
		manager.mu.Unlock()
		_ = listener.Close()
		return nil, cleanup, context.Canceled
	}
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
	closeAuth := func() {
		access.attemptMu.Lock()
		access.start = nil
		access.attemptMu.Unlock()
		stop()
		cleanup()
	}
	server.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/callback" {
			http.NotFound(writer, request)
			return
		}
		if err := manager.submitAuthorization(config.ID, flow, redirect+"?"+request.URL.RawQuery); err != nil {
			http.Error(writer, "This sign-in link is no longer active.", http.StatusGone)
			return
		}
		_, _ = writer.Write([]byte("Authorization received. You may return to Crude."))
	})
	go func() { _ = server.Serve(listener) }()
	client := oauthHTTPClient(publicClientID(config), tokenURL)
	context.AfterFunc(lifetime, client.CloseIdleConnections)
	configuration := &auth.AuthorizationCodeHandlerConfig{
		Client:                          client,
		RedirectURL:                     redirect,
		RequestRefreshToken:             true,
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{RedirectURIs: []string{redirect}, ClientName: "Crude", TokenEndpointAuthMethod: "none", GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"}}},
		AuthorizationCodeFetcher: func(ctx context.Context, arguments *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			defer cleanup()
			if challenge != nil && !sameAuthorizationEndpoint(arguments.URL, challenge.authEndpoint) {
				return nil, errors.New("The authorization server changed. Select Sign out, then Sign in in MCP settings.")
			}
			manager.mu.Lock()
			entry := manager.connections[config.ID]
			if manager.closed || entry == nil || entry.epoch != epoch || ctx.Err() != nil {
				manager.mu.Unlock()
				return nil, context.Canceled
			}
			entry.state.AuthPending, entry.state.AuthURL = true, arguments.URL
			manager.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case returned := <-callback:
				parsed, err := url.Parse(returned)
				if err != nil {
					return nil, errors.New("invalid authorization return URL")
				}
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if parsed.Query().Get("error") != "" || parsed.Query().Get("code") == "" {
					return nil, errors.New("MCP authorization was declined.")
				}
				return &auth.AuthorizationResult{Code: parsed.Query().Get("code"), State: parsed.Query().Get("state"), Iss: parsed.Query().Get("iss")}, nil
			}
		},
		NewTokenSource: func(_ context.Context, configuration *oauth2.Config, token *oauth2.Token) (oauth2.TokenSource, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			saved := credential{ID: config.ID, Endpoint: config.URL, Binding: oauthBinding(config), Config: *configuration, Token: *token}
			if secret != "" {
				// Resolve a managed secret only at the host. Never copy it into the token file.
				saved.Config.ClientSecret = ""
			}
			if err := manager.persistToken(ctx, epoch, saved); err != nil {
				return nil, err
			}
			source := manager.newTokenSource(lifetime, epoch, saved, *configuration, client)
			access.mu.Lock()
			access.source = source
			access.mu.Unlock()
			manager.mu.Lock()
			if entry := manager.connections[config.ID]; entry != nil && entry.epoch == epoch && entry.scopeChallenge == challenge {
				entry.scopeChallenge = nil
			}
			manager.mu.Unlock()
			return source, nil
		},
	}
	if config.OAuthClientID != "" {
		configuration.PreregisteredClient = &oauthex.ClientCredentials{ClientID: config.OAuthClientID, Issuer: config.OAuthIssuer}
		if secret != "" {
			configuration.PreregisteredClient.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: secret}
		}
		configuration.DynamicClientRegistrationConfig = nil
	}
	if config.OAuthMetadataURL != "" {
		configuration.ClientIDMetadataDocumentConfig = &auth.ClientIDMetadataDocumentConfig{URL: config.OAuthMetadataURL}
	}
	if challenge != nil {
		configuration.ScopeFilter = func([]string) []string { return slices.Clone(challenge.scopes) }
	} else if len(config.OAuthScopes) > 0 {
		configuration.ScopeFilter = func([]string) []string { return append([]string(nil), config.OAuthScopes...) }
	}
	handler, err := auth.NewAuthorizationCodeHandler(configuration)
	if err != nil {
		closeAuth()
		return nil, func() {}, err
	}
	access.start = func(ctx context.Context, request *http.Request, response *http.Response) error {
		if challenge != nil && bearerChallenge(response)["resource_metadata"] != challenge.resourceHint {
			_ = response.Body.Close()
			return errors.New("The OAuth resource details changed. Select Sign out, then Sign in in MCP settings.")
		}
		err := handler.Authorize(ctx, request, response)
		// A token response can echo access credentials. Never publish its text.
		var rejected *oauth2.RetrieveError
		if errors.As(err, &rejected) {
			return errors.New("The OAuth token request was rejected. Check MCP settings and use Sign in again.")
		}
		return err
	}
	return access, closeAuth, nil
}

func (manager *Manager) SignIn(ctx context.Context, id string) error {
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
	return manager.submitAuthorization(id, callback, callbackURL)
}

func (manager *Manager) submitAuthorization(id string, flow *authorizationFlow, callbackURL string) error {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	entry := manager.connections[id]
	if entry == nil || entry.epoch != flow.epoch || !entry.state.AuthPending || manager.authCallbacks[id] != flow {
		return errors.New("No active authorization flow for this connection.")
	}
	select {
	case flow.callback <- callbackURL:
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
