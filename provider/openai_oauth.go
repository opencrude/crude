package provider

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"crude/logger"
)

// OAuthCredentials holds tokens from an OAuth sign-in flow. Currently this is
// the ChatGPT-subscription "Sign in with ChatGPT" flow used by the codex
// provider. Like an API key, these never leave the application config layer.
type OAuthCredentials struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	Email        string `json:"email,omitempty"`
	// ExpiresAt is the access-token expiry as a unix timestamp (seconds).
	ExpiresAt int64 `json:"expires_at,omitempty"`
}

// expiredSoon reports whether the access token is missing or expires within the
// refresh skew window. A missing expiry is treated as expired to be safe.
func (c *OAuthCredentials) expiredSoon() bool {
	if c == nil {
		return false
	}
	if c.AccessToken == "" {
		return true
	}
	if c.ExpiresAt == 0 {
		return true
	}
	return time.Now().Unix()+60 >= c.ExpiresAt
}

// OpenAI ChatGPT-subscription OAuth constants. These mirror the public PKCE
// client used by the Codex CLI and other third-party agents (e.g. Zed). The
// codex backend at chatgpt.com/backend-api/codex is an undocumented endpoint;
// it may change without notice.
const (
	openAIAuthorizeURL  = "https://auth.openai.com/oauth/authorize"
	openAIClientID      = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAIScopes        = "openid profile email offline_access"
	openAIOAuthOrigin   = "crude"
	openAIAuthCallback  = "/auth/callback"
	openAIAuthPort      = 1455
	openAIAuthPortAlt   = 1457
	openAIAuthHost      = "localhost"
	openAITokenSkewSecs = 60
)

// openAITokenURL is a var (not a const) so tests can point it at an httptest
// server to exercise the token exchange / refresh request shape end-to-end.
var openAITokenURL = "https://auth.openai.com/oauth/token"

// ChatGPTSignInError is returned by SignInWithChatGPT/RefreshOpenAIToken when the
// OAuth server response indicates a fatal auth failure (HTTP 400/401/403) that
// should clear stored credentials, as opposed to a transient network/server
// error that leaves existing credentials intact.
type ChatGPTSignInError struct {
	Fatal bool
	Msg   string
}

func (e *ChatGPTSignInError) Error() string { return e.Msg }

// SignInWithChatGPT runs the PKCE "Sign in with ChatGPT" authorization-code
// flow: it starts a local callback server, opens the user's browser to the
// OpenAI authorize endpoint, waits for the redirect, and exchanges the code for
// tokens. It blocks until the flow completes, the context is cancelled, or the
// callback wait times out (5 minutes).
func SignInWithChatGPT(ctx context.Context) (*OAuthCredentials, error) {
	verifier, challenge, err := pkceChallenge()
	if err != nil {
		return nil, fmt.Errorf("generate pkce: %w", err)
	}
	state, err := randomHex(16)
	if err != nil {
		return nil, fmt.Errorf("generate state: %w", err)
	}

	srv, redirectURI, err := startCallbackServer()
	if err != nil {
		return nil, fmt.Errorf("start callback server: %w", err)
	}
	defer srv.Close()

	authorizeURL := buildAuthorizeURL(redirectURI, challenge, state)
	if err := openBrowser(authorizeURL); err != nil {
		// Non-fatal: the user can open the URL manually. Log via package logger
		// if set, but proceed regardless.
		logIf(func(lg *logger.Logger) {
			lg.Warn("OAUTH", "open_browser", "error", err.Error(), "url", authorizeURL)
		})
	}

	code, err := waitForCallback(ctx, state)
	if err != nil {
		return nil, err
	}

	creds, err := exchangeCode(ctx, code, redirectURI, verifier)
	if err != nil {
		return nil, err
	}
	return creds, nil
}

// RefreshOpenAIToken exchanges a refresh token for a fresh access token. A
// fatal (400/401/403) response yields a *ChatGPTSignInError with Fatal=true so
// callers can sign the user out; other failures are returned as transient
// errors that leave existing credentials intact.
func RefreshOpenAIToken(ctx context.Context, refreshToken string) (*OAuthCredentials, error) {
	if refreshToken == "" {
		return nil, &ChatGPTSignInError{Fatal: true, Msg: "no refresh token"}
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {openAIClientID},
		"refresh_token": {refreshToken},
	}
	return postTokenRequest(ctx, form)
}

func buildAuthorizeURL(redirectURI, challenge, state string) string {
	q := url.Values{
		"response_type":              {"code"},
		"client_id":                  {openAIClientID},
		"redirect_uri":               {redirectURI},
		"scope":                      {openAIScopes},
		"code_challenge":             {challenge},
		"code_challenge_method":      {"S256"},
		"state":                      {state},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {openAIOAuthOrigin},
	}
	return openAIAuthorizeURL + "?" + q.Encode()
}

func exchangeCode(ctx context.Context, code, redirectURI, verifier string) (*OAuthCredentials, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {openAIClientID},
		"code_verifier": {verifier},
	}
	return postTokenRequest(ctx, form)
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Email        string `json:"email"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func postTokenRequest(ctx context.Context, form url.Values) (*OAuthCredentials, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAITokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := fmt.Sprintf("token endpoint returned HTTP %d", resp.StatusCode)
		var tr tokenResponse
		if json.Unmarshal(body, &tr) == nil {
			if tr.Error != "" {
				msg = tr.Error
				if tr.ErrorDesc != "" {
					msg += ": " + tr.ErrorDesc
				}
			}
		}
		fatal := resp.StatusCode == http.StatusBadRequest ||
			resp.StatusCode == http.StatusUnauthorized ||
			resp.StatusCode == http.StatusForbidden
		return nil, &ChatGPTSignInError{Fatal: fatal, Msg: msg}
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return nil, &ChatGPTSignInError{Fatal: true, Msg: "token response missing access_token"}
	}

	creds := &OAuthCredentials{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		IDToken:      tr.IDToken,
		Email:        tr.Email,
		ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second).Unix(),
	}
	creds.AccountID = extractChatGPTAccountID(creds.IDToken, creds.AccessToken)
	return creds, nil
}

// startCallbackServer binds a loopback HTTP server on the registered redirect
// port (1455, falling back to 1457) and returns the matching redirect URI.
func startCallbackServer() (*http.Server, string, error) {
	for _, port := range []int{openAIAuthPort, openAIAuthPortAlt} {
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", openAIAuthHost, port))
		if err != nil {
			continue
		}
		mux := http.NewServeMux()
		srv := &http.Server{Handler: mux}
		callbackMu.Lock()
		callbackCred = make(chan struct{})
		callbackErr = nil
		callbackMu.Unlock()
		mux.HandleFunc(openAIAuthCallback, handleOAuthCallback)
		go func() { _ = srv.Serve(ln) }()
		return srv, fmt.Sprintf("http://%s:%d%s", openAIAuthHost, port, openAIAuthCallback), nil
	}
	return nil, "", errors.New("could not bind callback port 1455 or 1457")
}

var (
	callbackMu   sync.Mutex
	callbackCred chan struct{}
	callbackErr  error
)

func handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	q := r.URL.Query()
	if codeErr := q.Get("error"); codeErr != "" {
		desc := q.Get("error_description")
		if desc == "" {
			desc = codeErr
		}
		callbackMu.Lock()
		callbackErr = &ChatGPTSignInError{Fatal: true, Msg: "authorization denied: " + desc}
		callbackMu.Unlock()
		_, _ = w.Write([]byte(oauthCallbackHTML("Sign-in denied. You can close this tab.")))
		signalCallback()
		return
	}
	code := q.Get("code")
	state := q.Get("state")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	authCodeSlotMu.Lock()
	authCodeSlot = code
	authCodeSlotState = state
	authCodeSlotMu.Unlock()
	_, _ = w.Write([]byte(oauthCallbackHTML("Signed in. You can close this tab and return to Crude.")))
	signalCallback()
}

var (
	authCodeSlotMu    sync.Mutex
	authCodeSlot      string
	authCodeSlotState string
)

func signalCallback() {
	callbackMu.Lock()
	defer callbackMu.Unlock()
	if callbackCred != nil {
		close(callbackCred)
		callbackCred = nil
	}
}

func waitForCallback(ctx context.Context, expectedState string) (string, error) {
	callbackMu.Lock()
	ch := callbackCred
	callbackMu.Unlock()
	if ch == nil {
		// The browser callback may have already fired and signalCallback
		// cleared the channel before we read it. Fall through to the shared
		// result check instead of erroring on a nil channel.
		return readCallbackResult(expectedState)
	}

	timeout := 5 * time.Minute
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ch:
	case <-timer.C:
		return "", errors.New("sign-in timed out waiting for browser callback")
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return readCallbackResult(expectedState)
}

// readCallbackResult consumes the code/error stored by the callback handler.
// It is safe to call after the signal channel fires OR when the channel was
// already cleared by an earlier signalCallback.
func readCallbackResult(expectedState string) (string, error) {
	callbackMu.Lock()
	err := callbackErr
	callbackMu.Unlock()
	if err != nil {
		return "", err
	}

	authCodeSlotMu.Lock()
	code := authCodeSlot
	state := authCodeSlotState
	authCodeSlot = ""
	authCodeSlotState = ""
	authCodeSlotMu.Unlock()

	if code == "" {
		return "", errors.New("callback received no authorization code")
	}
	if state != expectedState {
		return "", &ChatGPTSignInError{Fatal: true, Msg: "oauth state mismatch — possible CSRF, aborting"}
	}
	return code, nil
}

func oauthCallbackHTML(message string) string {
	return "<!doctype html><html><head><meta charset=\"utf-8\"><title>Crude</title>" +
		"<style>body{font-family:system-ui,sans-serif;text-align:center;padding:3rem}</style></head>" +
		"<body><h2>Crude</h2><p>" + message + "</p></body></html>"
}

// extractChatGPTAccountID decodes the JWT middle segment of the id_token (or the
// access token if no id_token was returned) and looks for the ChatGPT account id
// in one of three claim locations used by OpenAI's tokens.
func extractChatGPTAccountID(idToken, accessToken string) string {
	token := idToken
	if token == "" {
		token = accessToken
	}
	claims := decodeJWTClaims(token)
	if claims == nil {
		return ""
	}
	if v, ok := claims["chatgpt_account_id"].(string); ok && v != "" {
		return v
	}
	if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if v, ok := auth["chatgpt_account_id"].(string); ok && v != "" {
			return v
		}
	}
	if orgs, ok := claims["organizations"].([]any); ok && len(orgs) > 0 {
		if org, ok := orgs[0].(map[string]any); ok {
			if v, ok := org["id"].(string); ok && v != "" {
				return v
			}
		}
	}
	return ""
}

func decodeJWTClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	return claims
}

// pkceChallenge generates a PKCE code verifier (32 random bytes, base64url no
// padding) and its S256 code challenge.
func pkceChallenge() (verifier, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", buf), nil
}

// newSessionUUID generates a random UUID v4 string for the codex session-id /
// thread-id headers.
func newSessionUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000-0000-0000-0000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func openBrowser(rawURL string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", rawURL).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL).Start()
	default:
		if _, err := exec.LookPath("xdg-open"); err == nil {
			return exec.Command("xdg-open", rawURL).Start()
		}
		return fmt.Errorf("no browser launcher found; open this URL manually: %s", rawURL)
	}
}
