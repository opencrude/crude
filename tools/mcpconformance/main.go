// Command mcpconformance drives the production MCP manager against the official local referee.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"crude/agent/mcpclient"
	"crude/internal/network"
	captypes "crude/types/capability"
)

const id = "referee"

type scenarioContext struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	ToolCalls    []struct {
		Name      string
		Arguments map[string]any
	} `json:"toolCalls"`
}

func loopback(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Fragment != "" || u.Port() == "" {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

func run(ctx context.Context, name, endpoint string, extra scenarioContext) error {
	if name == "" || !loopback(endpoint) {
		return errors.New("use the local conformance runner")
	}
	// Some OAuth negative scenarios contain non-local metadata. Never resolve or dial those hosts.
	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer deny.Close()
	policy := network.DefaultSettings()
	policy.ProxyMode, policy.ProxyURL, policy.NoProxy = network.ProxyModeCustom, deny.URL, "localhost,127.0.0.1,::1"
	if err := network.ConfigureDefault(policy); err != nil {
		return err
	}
	root, err := os.MkdirTemp("", "mcp-referee-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	manager := mcpclient.New(root)
	defer manager.Close()
	manager.SetCredentialDirectory(root)
	config := captypes.ConnectionConfig{ID: id, Name: "Official MCP referee", Transport: "http", URL: endpoint, TimeoutSeconds: 12}
	isAuth := strings.HasPrefix(name, "auth/")
	if isAuth {
		config.Auth, config.OAuthClientID = "oauth", extra.ClientID
		// The pinned official fixture supplies its public CIMD identity in the
		// upstream everything-client instead of MCP_CONFORMANCE_CONTEXT.
		// This is a synthetic connection setting, not a product default.
		if name == "auth/basic-cimd" {
			config.OAuthMetadataURL = "https://conformance-test.local/client-metadata.json"
		}
		if extra.ClientSecret != "" && extra.ClientID != "" && name == "auth/pre-registration" {
			issuer, err := syntheticIssuer(ctx, endpoint)
			if err != nil {
				return err
			}
			const secretName = "REFEREE_OAUTH_CLIENT"
			if err := manager.SetSecrets(map[string]string{secretName: extra.ClientSecret}); err != nil {
				return errors.New("could not store synthetic client credentials")
			}
			config.OAuthIssuer, config.CredentialRef = issuer, "secret:"+secretName
		}
	}
	manager.Configure(map[string]captypes.ConnectionConfig{id: config})
	if isAuth {
		err = signIn(ctx, manager)
		if err != nil && name == "auth/authorization-server-migration" && manager.States()[0].State == "needs sign-in" {
			// The synthetic user selects Sign in at the changed issuer.
			err = signIn(ctx, manager)
		}
	} else {
		err = manager.Connect(ctx, id)
	}
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	states := manager.States()
	if len(states) != 1 || states[0].State != "connected" {
		return errors.New("connection did not finish discovery")
	}
	state := states[0]
	find := func(name string) (captypes.Tool, error) {
		for _, tool := range state.Tools {
			if tool.OriginalName == name {
				return tool, nil
			}
		}
		return captypes.Tool{}, fmt.Errorf("tool not discovered: %s", name)
	}
	call := func(name string, args map[string]any) (captypes.Result, error) {
		tool, err := find(name)
		if err != nil {
			return captypes.Result{}, err
		}
		r, err := manager.Call(ctx, tool, args)
		if err == nil && (r.IsError || r.Unknown) {
			err = errors.New("tool result is an error or unknown")
		}
		return r, err
	}
	switch {
	case isAuth:
		_, err = call("test-tool", map[string]any{})
		if err != nil && name == "auth/scope-step-up" && manager.States()[0].State == "needs sign-in" {
			// This test tool is a fixed, side-effect-free fixture. Rediscover
			// it after explicit sign-in. Do not guess scopes from its name.
			if err = signIn(ctx, manager); err == nil {
				state = manager.States()[0]
				_, err = call("test-tool", map[string]any{})
			}
		}
	case name == "initialize" || name == "request-metadata": // Connect performed version negotiation and discovery.
	case name == "json-schema-ref-no-deref":
		if len(state.Tools) == 0 {
			return errors.New("no tool schema discovered")
		}
	case name == "tools_call":
		r, e := call("add_numbers", map[string]any{"a": 5, "b": 3})
		if e != nil {
			return e
		}
		if len(r.Content) != 1 || r.Content[0].Text != "The sum of 5 and 3 is 8" {
			return errors.New("wrong tool result")
		}
	case name == "json-schema-2020-12-preservation":
		focal, e := find("json_schema_2020_12_tool")
		if e != nil {
			return e
		}
		_, err = call("json_schema_echo", map[string]any{"schema": focal.InputSchema})
	case name == "http-standard-headers":
		if len(state.Resources) == 0 || len(state.Prompts) == 0 {
			return errors.New("missing referee resource or prompt")
		}
		if _, err = call("test_headers", map[string]any{}); err != nil {
			return err
		}
		if _, err = manager.ReadResource(ctx, id, state.Resources[0].URI); err != nil {
			return err
		}
		_, err = manager.GetPrompt(ctx, id, state.Prompts[0].Name, nil)
	case name == "http-custom-headers":
		if len(extra.ToolCalls) == 0 {
			return errors.New("missing referee tool calls")
		}
		for _, tc := range extra.ToolCalls {
			if _, err = call(tc.Name, tc.Arguments); err != nil {
				return err
			}
		}
	case name == "http-invalid-tool-headers":
		if len(state.Tools) == 0 {
			return errors.New("missing valid tool")
		}
		for _, tool := range state.Tools {
			if _, err = call(tool.OriginalName, map[string]any{"region": "us-west1"}); err != nil {
				return err
			}
		}
	case name == "sep-2322-client-request-state":
		for _, fixture := range []struct{ tool, text string }{
			{"test_mrtr_echo_state", "echo-state-ok"}, {"test_mrtr_no_state", "no-state-ok"},
			{"test_mrtr_unrelated", "unrelated-ok"}, {"test_mrtr_no_result_type", "no-result-type-test-ok"},
		} {
			tool, err := find(fixture.tool)
			if err != nil {
				return err
			}
			answered := false
			owner := captypes.WithFormHandler(ctx, tool.Name, func(_ context.Context, f captypes.Form) captypes.FormReply {
				if f.ConnectionID != id || f.Tool != fixture.tool || len(f.Fields) != 1 || f.Fields[0].Name != "confirmed" || f.Fields[0].Type != "boolean" {
					return captypes.FormReply{Action: "cancel"}
				}
				answered = true
				return captypes.FormReply{Action: "accept", Values: map[string]string{"confirmed": "true"}}
			})
			r, err := manager.Call(owner, tool, nil)
			if err != nil || r.IsError || r.Unknown || len(r.Content) != 1 || r.Content[0].Text != fixture.text || answered != (fixture.text == "echo-state-ok" || fixture.text == "no-state-ok") {
				return fmt.Errorf("invalid %s form exchange: %v", fixture.tool, err)
			}
		}
	default:
		return errors.New("scenario has no driver")
	}
	return err
}

// Older referee contexts supply the authorized client ID and secret but omit
// their synthetic issuer. Read it from the fixture's public loopback metadata
// to emulate entering that issuer in Settings. Production never infers it.
func syntheticIssuer(ctx context.Context, endpoint string) (string, error) {
	resource, err := url.Parse(endpoint)
	if err != nil || !loopback(endpoint) {
		return "", errors.New("invalid synthetic MCP resource")
	}
	resource.Path = "/.well-known/oauth-protected-resource" + resource.Path
	client := network.NewClient(5 * time.Second)
	defer client.CloseIdleConnections()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	metadata, err := oauthex.GetProtectedResourceMetadata(ctx, resource.String(), endpoint, client)
	if err != nil || len(metadata.AuthorizationServers) != 1 || !loopback(metadata.AuthorizationServers[0]) {
		return "", errors.New("referee did not disclose one loopback issuer")
	}
	return metadata.AuthorizationServers[0], nil
}

// Follow only the synthetic authorization server's first redirect. Submit it to
// the manager, which owns the expected callback, state and token exchange.
func signIn(parent context.Context, m *mcpclient.Manager) error {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	var signInErr error
	go func() {
		signInErr = m.SignIn(ctx, id)
		close(done)
	}()
	defer func() {
		cancel()
		<-done // Join on early return without replacing the original error.
	}()
	client := network.NewClient(5 * time.Second)
	defer client.CloseIdleConnections()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ticks := time.NewTicker(15 * time.Millisecond)
	defer ticks.Stop()
	for {
		select {
		case <-done:
			return signInErr
		case <-ctx.Done():
			return ctx.Err()
		case <-ticks.C:
			state := m.States()[0]
			if !state.AuthPending {
				continue
			}
			if !loopback(state.AuthURL) {
				return errors.New("authorization requires a non-loopback URL")
			}
			req, err := http.NewRequestWithContext(ctx, "GET", state.AuthURL, nil)
			if err != nil {
				return err
			}
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			_ = resp.Body.Close()
			loc := resp.Header.Get("Location")
			if resp.StatusCode/100 != 3 || !loopback(loc) {
				return errors.New("referee did not return a loopback callback")
			}
			if err = m.SubmitAuthorization(id, loc); err != nil {
				return err
			}
			<-done
			return signInErr
		}
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Use the local conformance runner.")
		os.Exit(2)
	}
	var extra scenarioContext
	if s := os.Getenv("MCP_CONFORMANCE_CONTEXT"); s != "" {
		if err := json.Unmarshal([]byte(s), &extra); err != nil {
			fmt.Fprintln(os.Stderr, "Invalid referee context.")
			os.Exit(2)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	name := os.Getenv("MCP_CONFORMANCE_SCENARIO")
	if err := run(ctx, name, os.Args[1], extra); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		os.Exit(1)
	}
	fmt.Println("completed", name)
}
