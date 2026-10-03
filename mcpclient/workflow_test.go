package mcpclient

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	captypes "crude/types/capability"
)

const workflowResourceURI = "workflow://fixture/document"
const workflowSecret = "explicit-workflow-test-secret"

type workflowCallOutcome struct {
	result captypes.Result
	err    error
}

type workflowStoredCredential struct {
	Endpoint string
	ID       string
	Config   oauth2.Config
	Token    oauth2.Token
}

func workflowServer(directory string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "managed-workflow-fixture", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{
		Name: "inspect", Description: "Inspect the approved helper environment",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"message": map[string]any{"type": "string"}}, "required": []string{"message"}},
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var arguments struct{ Message string }
		if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(directory, "inspect.called"), []byte(arguments.Message), 0600); err != nil {
			return nil, err
		}
		if err := request.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: request.Params.GetProgressToken(), Progress: 1, Total: 1}); err != nil {
			return nil, err
		}
		workingDirectory, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		_, inheritedSecret := os.LookupEnv("CRUDE_MCP_WORKFLOW_UNSCOPED_SECRET")
		_, inheritedHome := os.LookupEnv("HOME")
		_, inheritedCloudSecret := os.LookupEnv("AWS_SECRET_ACCESS_KEY")
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + arguments.Message}, &mcp.TextContent{Text: "credential: " + os.Getenv("WORKFLOW_AUTH")}},
			StructuredContent: map[string]any{
				"message":             arguments.Message,
				"explicitEnvironment": os.Getenv("CRUDE_MCP_WORKFLOW_ALLOWED") == "explicit-approved-environment",
				"referencedSecret":    os.Getenv("WORKFLOW_AUTH") == workflowSecret,
				"workingDirectory":    workingDirectory == directory,
				"inheritedSecret":     inheritedSecret, "inheritedHome": inheritedHome, "inheritedCloudSecret": inheritedCloudSecret,
				"credential": os.Getenv("WORKFLOW_AUTH"),
			},
		}, nil
	})
	server.AddTool(&mcp.Tool{
		Name: "wait", Description: "Wait until the caller cancels or disconnects",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"mode": map[string]any{"type": "string", "enum": []string{"cancel", "disconnect"}}}, "required": []string{"mode"}},
	}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var arguments struct{ Mode string }
		if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
			return nil, err
		}
		if arguments.Mode != "cancel" && arguments.Mode != "disconnect" {
			return nil, errors.New("unsupported fixture wait mode")
		}
		if err := os.WriteFile(filepath.Join(directory, arguments.Mode+".started"), []byte("started"), 0600); err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			if err := os.WriteFile(filepath.Join(directory, arguments.Mode+".cancelled"), []byte("cancelled"), 0600); err != nil {
				return nil, err
			}
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			return nil, errors.New("fixture wait exceeded its deadline")
		}
	})
	server.AddResource(&mcp.Resource{URI: workflowResourceURI, Name: "Document", MIMEType: "text/plain"}, func(_ context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		contents, err := os.ReadFile(filepath.Join(directory, "document.txt"))
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: request.Params.URI, MIMEType: "text/plain", Text: string(contents)}}}, nil
	})
	server.AddPrompt(&mcp.Prompt{Name: "review", Description: "Review a selected topic", Arguments: []*mcp.PromptArgument{{Name: "topic", Required: true}}}, func(_ context.Context, request *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		topic := request.Params.Arguments["topic"]
		if topic == "" {
			return nil, errors.New("topic is required")
		}
		return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: "Review " + topic}}}}, nil
	})
	return server
}

func TestManagedWorkflowStdioHelper(test *testing.T) {
	if os.Getenv("CRUDE_MCP_WORKFLOW_HELPER") != "managed-stdio-workflow-helper" {
		return
	}
	directory := os.Getenv("CRUDE_MCP_WORKFLOW_DIRECTORY")
	if err := os.WriteFile(filepath.Join(directory, "helper.started"), []byte(fmt.Sprint(os.Getpid())), 0600); err != nil {
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := workflowServer(directory).Run(ctx, &mcp.StdioTransport{MaxLineLength: 1 << 20}); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func workflowState(test *testing.T, manager *Manager, id string) captypes.ConnectionState {
	test.Helper()
	for _, state := range manager.States() {
		if state.ID == id {
			return state
		}
	}
	test.Fatalf("connection %q has no published state", id)
	return captypes.ConnectionState{}
}

func workflowTool(test *testing.T, manager *Manager, id, name string) captypes.Tool {
	test.Helper()
	for _, tool := range workflowState(test, manager, id).Tools {
		if tool.OriginalName == name {
			return tool
		}
	}
	test.Fatalf("connection %q did not discover tool %q", id, name)
	return captypes.Tool{}
}

func workflowEventually(test *testing.T, description string, ready func() bool) {
	test.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			test.Fatalf("timed out waiting for %s", description)
		}
	}
}

func workflowAwaitError(test *testing.T, description string, completed <-chan error) error {
	test.Helper()
	select {
	case err := <-completed:
		return err
	case <-time.After(3 * time.Second):
		test.Fatalf("%s did not finish within its deadline", description)
		return nil
	}
}

func workflowAwaitCall(test *testing.T, completed <-chan workflowCallOutcome) workflowCallOutcome {
	test.Helper()
	select {
	case outcome := <-completed:
		return outcome
	case <-time.After(3 * time.Second):
		test.Fatal("pending MCP call did not finish within its deadline")
		return workflowCallOutcome{}
	}
}

func TestManagedWorkflowStdioApprovalIsolationAndContext(test *testing.T) {
	directory := test.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "document.txt"), []byte("real helper resource"), 0600); err != nil {
		test.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		test.Fatal(err)
	}
	test.Setenv("CRUDE_MCP_WORKFLOW_APPLICATION_SECRET", workflowSecret)
	test.Setenv("CRUDE_MCP_WORKFLOW_UNSCOPED_SECRET", "unapproved-parent-secret")
	test.Setenv("AWS_SECRET_ACCESS_KEY", "unapproved-cloud-secret")
	test.Setenv("HOME", filepath.Join(directory, "unapproved-home"))
	manager := New(directory)
	test.Cleanup(manager.Close)
	config := captypes.ConnectionConfig{
		ID: "stdio_workflow", Name: "Approved helper", Transport: "stdio", Command: executable,
		Args: []string{"-test.run=^TestManagedWorkflowStdioHelper$", "-test.timeout=25s"}, TimeoutSeconds: 5,
		Environment: map[string]string{
			"CRUDE_MCP_WORKFLOW_HELPER":    "managed-stdio-workflow-helper",
			"CRUDE_MCP_WORKFLOW_DIRECTORY": directory,
			"CRUDE_MCP_WORKFLOW_ALLOWED":   "explicit-approved-environment",
		},
		SecretRefs: map[string]string{"WORKFLOW_AUTH": "env:CRUDE_MCP_WORKFLOW_APPLICATION_SECRET"},
	}
	ctx, cancel := context.WithTimeout(test.Context(), 15*time.Second)
	defer cancel()
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if err := manager.Connect(ctx, config.ID); err == nil || !strings.Contains(err.Error(), "approval") {
		test.Fatalf("unapproved local launch was not refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "helper.started")); !os.IsNotExist(err) {
		test.Fatalf("unapproved configuration launched the helper: %v", err)
	}
	config.LaunchApproved = true
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if err := manager.Connect(ctx, config.ID); err != nil {
		test.Fatal(err)
	}
	state := workflowState(test, manager, config.ID)
	if state.State != "connected" || state.Cached || len(state.Tools) != 2 || len(state.Resources) != 1 || len(state.Prompts) != 1 {
		test.Fatalf("incomplete stdio discovery: %#v", state)
	}
	if state.Resources[0].URI != workflowResourceURI || state.Resources[0].ConnectionID != config.ID || len(state.Prompts[0].Arguments) != 1 || !state.Prompts[0].Arguments[0].Required {
		test.Fatalf("resource or prompt identity was lost: %#v", state)
	}
	inspect := workflowTool(test, manager, config.ID, "inspect")
	if inspect.Selected || inspect.Name != "mcp__stdio_workflow__inspect" {
		test.Fatalf("server annotation granted a tool or changed its identity: %#v", inspect)
	}
	if _, err := manager.Call(ctx, inspect, map[string]any{"message": "denied"}); err == nil {
		test.Fatal("ungranted helper tool executed")
	}
	if _, err := os.Stat(filepath.Join(directory, "inspect.called")); !os.IsNotExist(err) {
		test.Fatalf("ungranted call reached the helper: %v", err)
	}
	wait := workflowTool(test, manager, config.ID, "wait")
	config.Grants = map[string]captypes.ToolGrant{
		"inspect": {Effect: captypes.EffectRead, Scope: "workspace", Fingerprint: inspect.Fingerprint},
		"wait":    {Effect: captypes.EffectWrite, Scope: "workspace", Fingerprint: wait.Fingerprint},
	}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if err := manager.Connect(ctx, config.ID); err != nil {
		test.Fatal(err)
	}
	inspect = workflowTool(test, manager, config.ID, "inspect")
	result, err := manager.Call(ctx, inspect, map[string]any{"message": "approved request"})
	if err != nil || result.IsError || result.Unknown || len(result.Content) != 2 || result.Content[0].Text != "echo: approved request" || result.Content[1].Text != "credential: [redacted]" {
		test.Fatalf("stdio tool result %#v: %v", result, err)
	}
	var observed struct {
		Message              string
		ExplicitEnvironment  bool
		ReferencedSecret     bool
		WorkingDirectory     bool
		InheritedSecret      bool
		InheritedHome        bool
		InheritedCloudSecret bool
		Credential           string
	}
	if err := json.Unmarshal(result.Structured, &observed); err != nil {
		test.Fatal(err)
	}
	if observed.Message != "approved request" || !observed.ExplicitEnvironment || !observed.ReferencedSecret || !observed.WorkingDirectory || observed.InheritedSecret || observed.InheritedHome || observed.InheritedCloudSecret || observed.Credential != "[redacted]" {
		test.Fatalf("helper did not receive exactly the approved environment: %#v", observed)
	}
	workflowEventually(test, "managed progress notification", func() bool {
		return strings.Contains(strings.Join(workflowState(test, manager, config.ID).Logs, "\n"), "Progress: 1")
	})
	resource, err := manager.ReadResource(ctx, config.ID, workflowResourceURI)
	if err != nil || len(resource.Content) != 1 || resource.Content[0].Type != "resource" || resource.Content[0].URI != workflowResourceURI || resource.Content[0].Text != "real helper resource" || resource.Content[0].MIMEType != "text/plain" {
		test.Fatalf("stdio resource %#v: %v", resource, err)
	}
	prompt, err := manager.GetPrompt(ctx, config.ID, "review", map[string]string{"topic": "transport isolation"})
	if err != nil || len(prompt.Content) != 1 || prompt.Content[0].Type != "prompt" || prompt.Content[0].Text != "user: Review transport isolation" {
		test.Fatalf("stdio prompt %#v: %v", prompt, err)
	}
	if _, err := manager.GetPrompt(ctx, config.ID, "review", nil); err == nil {
		test.Fatal("missing required prompt argument succeeded")
	}
	wait = workflowTool(test, manager, config.ID, "wait")
	callContext, cancelCall := context.WithCancel(ctx)
	defer cancelCall()
	cancelled := make(chan workflowCallOutcome, 1)
	go func() {
		result, err := manager.Call(callContext, wait, map[string]any{"mode": "cancel"})
		cancelled <- workflowCallOutcome{result: result, err: err}
	}()
	workflowEventually(test, "helper's pending call", func() bool {
		_, err := os.Stat(filepath.Join(directory, "cancel.started"))
		return err == nil
	})
	cancelCall()
	outcome := workflowAwaitCall(test, cancelled)
	if !errors.Is(outcome.err, context.Canceled) || !outcome.result.IsError || !outcome.result.Unknown || len(outcome.result.Content) != 0 {
		test.Fatalf("cancelled mutation fabricated an outcome: %#v, %v", outcome.result, outcome.err)
	}
	workflowEventually(test, "cancellation at the real helper", func() bool {
		_, err := os.Stat(filepath.Join(directory, "cancel.cancelled"))
		return err == nil
	})
	if _, err := manager.ReadResource(ctx, config.ID, workflowResourceURI); err != nil {
		test.Fatalf("call cancellation disconnected the session: %v", err)
	}
	disconnected := make(chan workflowCallOutcome, 1)
	go func() {
		result, err := manager.Call(ctx, wait, map[string]any{"mode": "disconnect"})
		disconnected <- workflowCallOutcome{result: result, err: err}
	}()
	workflowEventually(test, "helper's disconnect workload", func() bool {
		_, err := os.Stat(filepath.Join(directory, "disconnect.started"))
		return err == nil
	})
	closed := make(chan error, 1)
	go func() { closed <- manager.Disconnect(config.ID) }()
	if err := workflowAwaitError(test, "stdio disconnect", closed); err != nil {
		test.Errorf("stdio disconnect returned an error: %v", err)
	}
	outcome = workflowAwaitCall(test, disconnected)
	if outcome.err == nil || !outcome.result.Unknown || !outcome.result.IsError || len(outcome.result.Content) != 0 {
		test.Fatalf("disconnected mutation fabricated an outcome: %#v, %v", outcome.result, outcome.err)
	}
	state = workflowState(test, manager, config.ID)
	if state.State != "not started" || !state.Cached || len(state.Resources) != 1 || len(state.Prompts) != 1 {
		test.Fatalf("disconnect lost the cached catalog: %#v", state)
	}
	if _, err := manager.Call(ctx, inspect, map[string]any{"message": "after disconnect"}); err == nil {
		test.Error("disconnected tool call succeeded")
	}
	if _, err := manager.ReadResource(ctx, config.ID, workflowResourceURI); err == nil {
		test.Error("disconnected resource fetch succeeded")
	}
	if _, err := manager.GetPrompt(ctx, config.ID, "review", map[string]string{"topic": "disconnected"}); err == nil {
		test.Error("disconnected prompt fetch succeeded")
	}
}

func TestManagedWorkflowHTTPDisconnectIsUnknownWithoutReplay(test *testing.T) {
	started := make(chan struct{}, 1)
	cancelled := make(chan struct{}, 1)
	var calls atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "disconnect-fixture", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "write", Description: "Pending remote mutation", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			cancelled <- struct{}{}
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
			return nil, errors.New("fixture mutation exceeded its deadline")
		}
	})
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, PropagateRequestCancellation: true}))
	test.Cleanup(endpoint.Close)
	manager := New(test.TempDir())
	test.Cleanup(manager.Close)
	config := captypes.ConnectionConfig{ID: "remote_workflow", Name: "Pending remote write", Transport: "http", URL: endpoint.URL, TimeoutSeconds: 5}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
	defer cancel()
	if err := manager.Connect(ctx, config.ID); err != nil {
		test.Fatal(err)
	}
	tool := workflowTool(test, manager, config.ID, "write")
	config.Grants = map[string]captypes.ToolGrant{"write": {Effect: captypes.EffectWrite, Scope: "workspace", Fingerprint: tool.Fingerprint}}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if err := manager.Connect(ctx, config.ID); err != nil {
		test.Fatal(err)
	}
	tool = workflowTool(test, manager, config.ID, "write")
	completed := make(chan workflowCallOutcome, 1)
	go func() {
		result, err := manager.Call(ctx, tool, map[string]any{})
		completed <- workflowCallOutcome{result: result, err: err}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		test.Fatal("remote mutation never reached the SDK server")
	}
	if err := manager.Disconnect(config.ID); err != nil {
		test.Fatal(err)
	}
	outcome := workflowAwaitCall(test, completed)
	if outcome.err == nil || !outcome.result.Unknown || !outcome.result.IsError || len(outcome.result.Content) != 0 || calls.Load() != 1 {
		test.Fatalf("remote disconnect outcome %#v, %v, calls=%d", outcome.result, outcome.err, calls.Load())
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		test.Fatal("disconnect did not cancel the actual remote workload")
	}
	if state := workflowState(test, manager, config.ID); state.State != "not started" || !state.Cached {
		test.Fatalf("remote disconnect state %#v", state)
	}
}

type workflowAuthorizationCode struct {
	redirect  string
	challenge string
	resource  string
}

type workflowOAuthFixture struct {
	endpoint        *httptest.Server
	mu              sync.Mutex
	codes           map[string]workflowAuthorizationCode
	validTokens     map[string]bool
	requests        atomic.Int32
	challenges      atomic.Int32
	authorizations  atomic.Int32
	exchanges       atomic.Int32
	refreshes       atomic.Int32
	credentialLeaks atomic.Int32
}

func workflowWriteJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		return
	}
}

func workflowOAuthServer(test *testing.T) *workflowOAuthFixture {
	test.Helper()
	directory := test.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "document.txt"), []byte("authenticated document"), 0600); err != nil {
		test.Fatal(err)
	}
	fixture := &workflowOAuthFixture{codes: make(map[string]workflowAuthorizationCode), validTokens: make(map[string]bool)}
	server := workflowServer(directory)
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
	fixture.endpoint = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.requests.Add(1)
		endpoint := fixture.endpoint.URL
		resource := endpoint + "/mcp"
		switch request.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			workflowWriteJSON(writer, map[string]any{"resource": resource, "authorization_servers": []string{endpoint}, "scopes_supported": []string{"read"}})
		case "/.well-known/oauth-authorization-server":
			workflowWriteJSON(writer, map[string]any{
				"issuer": endpoint, "authorization_endpoint": endpoint + "/authorize", "token_endpoint": endpoint + "/token",
				"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
				"token_endpoint_auth_methods_supported": []string{"none"}, "code_challenge_methods_supported": []string{"S256"},
				"scopes_supported": []string{"read", "offline_access"}, "authorization_response_iss_parameter_supported": true,
			})
		case "/authorize":
			arguments := request.URL.Query()
			redirect, err := url.Parse(arguments.Get("redirect_uri"))
			if err != nil || redirect.Scheme != "http" || redirect.Hostname() != "127.0.0.1" || redirect.Port() == "" || redirect.Path != "/callback" || arguments.Get("client_id") != "workflow-client" || arguments.Get("state") == "" || arguments.Get("resource") != resource || arguments.Get("code_challenge_method") != "S256" || arguments.Get("code_challenge") == "" || arguments.Get("response_type") != "code" || !strings.Contains(arguments.Get("scope"), "read") {
				http.Error(writer, "invalid authorization request", http.StatusBadRequest)
				return
			}
			code := fmt.Sprintf("workflow-code-%d", fixture.authorizations.Add(1))
			fixture.mu.Lock()
			fixture.codes[code] = workflowAuthorizationCode{redirect: redirect.String(), challenge: arguments.Get("code_challenge"), resource: resource}
			fixture.mu.Unlock()
			returned := redirect.Query()
			returned.Set("code", code)
			returned.Set("state", arguments.Get("state"))
			returned.Set("iss", endpoint)
			redirect.RawQuery = returned.Encode()
			http.Redirect(writer, request, redirect.String(), http.StatusFound)
		case "/token":
			if err := request.ParseForm(); err != nil {
				http.Error(writer, "invalid token request", http.StatusBadRequest)
				return
			}
			if request.Form.Get("grant_type") == "refresh_token" {
				fixture.refreshes.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusBadRequest)
				workflowWriteJSON(writer, map[string]string{"error": "invalid_grant"})
				return
			}
			fixture.mu.Lock()
			code, exists := fixture.codes[request.Form.Get("code")]
			fixture.mu.Unlock()
			challenge := sha256.Sum256([]byte(request.Form.Get("code_verifier")))
			if !exists || request.Method != http.MethodPost || request.Form.Get("grant_type") != "authorization_code" || request.Form.Get("client_id") != "workflow-client" || request.Form.Get("redirect_uri") != code.redirect || request.Form.Get("resource") != code.resource || base64.RawURLEncoding.EncodeToString(challenge[:]) != code.challenge {
				http.Error(writer, "invalid code, resource, redirect or PKCE verifier", http.StatusBadRequest)
				return
			}
			fixture.mu.Lock()
			delete(fixture.codes, request.Form.Get("code"))
			fixture.mu.Unlock()
			token := fmt.Sprintf("workflow-access-token-%d", fixture.exchanges.Add(1))
			fixture.mu.Lock()
			fixture.validTokens[token] = true
			fixture.mu.Unlock()
			workflowWriteJSON(writer, map[string]any{"access_token": token, "token_type": "Bearer", "refresh_token": "workflow-refresh-token", "expires_in": 3600})
		case "/mcp", "/alternate":
			fixture.mu.Lock()
			valid := fixture.validTokens[strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")]
			fixture.mu.Unlock()
			if request.URL.Path == "/alternate" && request.Header.Get("Authorization") != "" {
				fixture.credentialLeaks.Add(1)
			}
			if !valid {
				fixture.challenges.Add(1)
				writer.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer resource_metadata=%q, scope=\"read\"", endpoint+"/.well-known/oauth-protected-resource/mcp"))
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}
			transport.ServeHTTP(writer, request)
		default:
			http.NotFound(writer, request)
		}
	}))
	test.Cleanup(fixture.endpoint.Close)
	return fixture
}

func workflowOAuthManager(test *testing.T, fixture *workflowOAuthFixture, vault string, config captypes.ConnectionConfig) *Manager {
	test.Helper()
	manager := New(test.TempDir())
	manager.SetCredentialDirectory(vault)
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	test.Cleanup(manager.Close)
	return manager
}

func workflowOAuthConfig(fixture *workflowOAuthFixture) captypes.ConnectionConfig {
	return captypes.ConnectionConfig{ID: "oauth_workflow", Name: "OAuth workflow", Transport: "http", URL: fixture.endpoint.URL + "/mcp", Auth: "oauth", OAuthClientID: "workflow-client", OAuthScopes: []string{"read"}, TimeoutSeconds: 5}
}

func workflowStartSignIn(test *testing.T, manager *Manager, id string) (string, <-chan error, context.CancelFunc) {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 8*time.Second)
	test.Cleanup(cancel)
	completed := make(chan error, 1)
	go func() { completed <- manager.SignIn(ctx, id) }()
	var authorizationURL string
	workflowEventually(test, "an explicit OAuth authorization request", func() bool {
		select {
		case err := <-completed:
			test.Fatalf("SignIn finished before presenting authorization: %v; state=%#v", err, workflowState(test, manager, id))
		default:
		}
		state := workflowState(test, manager, id)
		if state.AuthPending && state.AuthURL != "" {
			authorizationURL = state.AuthURL
			return true
		}
		return false
	})
	return authorizationURL, completed, cancel
}

func workflowAuthorizationReturn(test *testing.T, authorizationURL string) string {
	test.Helper()
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(authorizationURL)
	if err != nil {
		test.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusFound {
		contents, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		test.Fatalf("authorization server returned %s: %s", response.Status, contents)
	}
	returned, err := response.Location()
	if err != nil {
		test.Fatal(err)
	}
	return returned.String()
}

func workflowCredentialPath(vault string, config captypes.ConnectionConfig) string {
	hash := sha256.Sum256([]byte(config.ID + "\n" + config.URL))
	return filepath.Join(vault, fmt.Sprintf("%x.json", hash))
}

func workflowReadCredential(test *testing.T, vault string, config captypes.ConnectionConfig) workflowStoredCredential {
	test.Helper()
	path := workflowCredentialPath(vault, config)
	info, err := os.Stat(path)
	if err != nil {
		test.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		test.Fatalf("credential mode is %o, want 0600", info.Mode().Perm())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		test.Fatal(err)
	}
	var saved workflowStoredCredential
	if err := json.Unmarshal(contents, &saved); err != nil {
		test.Fatal(err)
	}
	if saved.ID != config.ID || saved.Endpoint != config.URL || saved.Token.AccessToken == "" || saved.Token.RefreshToken == "" || saved.Config.ClientID != config.OAuthClientID {
		test.Fatalf("credential identity or token was not persisted: %#v", saved)
	}
	return saved
}

func TestManagedWorkflowOAuthLoopbackPersistenceBindingAndReauthentication(test *testing.T) {
	fixture := workflowOAuthServer(test)
	config := workflowOAuthConfig(fixture)
	vault := filepath.Join(test.TempDir(), "credentials")
	manager := workflowOAuthManager(test, fixture, vault, config)
	ctx, cancel := context.WithTimeout(test.Context(), 15*time.Second)
	defer cancel()
	if state := workflowState(test, manager, config.ID); state.State != "not started" || fixture.requests.Load() != 0 {
		test.Fatalf("configuration performed OAuth or network work: %#v", state)
	}
	if err := manager.Connect(ctx, config.ID); err == nil {
		test.Fatal("unauthenticated passive connect succeeded")
	}
	if state := workflowState(test, manager, config.ID); state.State != "needs sign-in" || state.AuthPending || state.AuthURL != "" || fixture.authorizations.Load() != 0 {
		test.Fatalf("passive connection initiated sign-in or lost recoverable state: %#v", state)
	}
	authorizationURL, completed, _ := workflowStartSignIn(test, manager, config.ID)
	returned := workflowAuthorizationReturn(test, authorizationURL)
	parsed, err := url.Parse(returned)
	if err != nil {
		test.Fatal(err)
	}
	for _, mutation := range []struct{ name, scheme, host, path string }{
		{name: "scheme", scheme: "https"}, {name: "origin", host: "localhost:" + parsed.Port()}, {name: "path", path: "/unrelated"},
	} {
		wrong := *parsed
		if mutation.scheme != "" {
			wrong.Scheme = mutation.scheme
		}
		if mutation.host != "" {
			wrong.Host = mutation.host
		}
		if mutation.path != "" {
			wrong.Path = mutation.path
		}
		if err := manager.SubmitAuthorization(config.ID, wrong.String()); err == nil {
			test.Fatalf("callback with wrong %s was accepted", mutation.name)
		}
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Get(returned)
	if err != nil {
		test.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		test.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		test.Fatalf("loopback callback status %s", response.Status)
	}
	if err := workflowAwaitError(test, "loopback sign-in", completed); err != nil {
		test.Fatal(err)
	}
	if state := workflowState(test, manager, config.ID); state.State != "connected" || state.AuthPending || state.AuthURL != "" || fixture.exchanges.Load() != 1 {
		test.Fatalf("successful sign-in state %#v", state)
	}
	saved := workflowReadCredential(test, vault, config)
	if info, err := os.Stat(vault); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0700) {
		test.Fatalf("credential vault permissions: %v, %v", info, err)
	}
	if err := manager.SubmitAuthorization(config.ID, returned); err == nil {
		test.Fatal("completed authorization flow accepted a replay")
	}
	inspect := workflowTool(test, manager, config.ID, "inspect")
	config.Grants = map[string]captypes.ToolGrant{"inspect": {Effect: captypes.EffectRead, Scope: "workspace", Fingerprint: inspect.Fingerprint}}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if err := manager.Connect(ctx, config.ID); err != nil {
		test.Fatalf("saved credential could not reconnect: %v", err)
	}
	if result, err := manager.Call(ctx, workflowTool(test, manager, config.ID, "inspect"), map[string]any{"message": "authenticated request"}); err != nil || result.IsError || len(result.Content) == 0 || result.Content[0].Text != "echo: authenticated request" {
		test.Fatalf("authenticated SDK tool result %#v: %v", result, err)
	}
	for _, other := range []captypes.ConnectionConfig{
		{ID: "another_identity", Name: config.Name, Transport: "http", URL: config.URL, Auth: "oauth"},
		{ID: config.ID, Name: config.Name, Transport: "http", URL: fixture.endpoint.URL + "/alternate", Auth: "oauth"},
	} {
		isolated := workflowOAuthManager(test, fixture, vault, other)
		if err := isolated.Connect(ctx, other.ID); err == nil {
			test.Fatalf("credential crossed connection or endpoint identity: %#v", other)
		}
		if state := workflowState(test, isolated, other.ID); state.State != "needs sign-in" || state.AuthPending {
			test.Fatalf("credential isolation state %#v", state)
		}
	}
	if fixture.credentialLeaks.Load() != 0 || fixture.authorizations.Load() != 1 || fixture.exchanges.Load() != 1 {
		test.Fatal("passive reconnect or another identity shared authentication")
	}
	if err := manager.Disconnect(config.ID); err != nil {
		test.Fatal(err)
	}
	restored := workflowOAuthManager(test, fixture, vault, config)
	if err := restored.Connect(ctx, config.ID); err != nil {
		test.Fatalf("fresh manager did not load the credential: %v", err)
	}
	if err := restored.Disconnect(config.ID); err != nil {
		test.Fatal(err)
	}
	fixture.mu.Lock()
	fixture.validTokens[saved.Token.AccessToken] = false
	fixture.mu.Unlock()
	if err := restored.Connect(ctx, config.ID); err == nil || workflowState(test, restored, config.ID).State != "needs sign-in" {
		test.Fatalf("revoked token did not require explicit reauthentication: %v", err)
	}
	authorizationURL, completed, _ = workflowStartSignIn(test, restored, config.ID)
	if err := restored.SubmitAuthorization(config.ID, workflowAuthorizationReturn(test, authorizationURL)); err != nil {
		test.Fatal(err)
	}
	if err := workflowAwaitError(test, "headless reauthentication", completed); err != nil {
		test.Fatal(err)
	}
	if fixture.exchanges.Load() != 2 || workflowReadCredential(test, vault, config).Token.AccessToken == saved.Token.AccessToken {
		test.Fatal("reauthentication did not replace the revoked credential")
	}
	if err := restored.SignOut(config.ID); err != nil {
		test.Fatal(err)
	}
	if _, err := os.Stat(workflowCredentialPath(vault, config)); !os.IsNotExist(err) {
		test.Fatalf("sign-out retained the application credential: %v", err)
	}
	if err := restored.Connect(ctx, config.ID); err == nil || workflowState(test, restored, config.ID).State != "needs sign-in" {
		test.Fatalf("sign-out did not clear authentication: %v", err)
	}
}

func TestManagedWorkflowOAuthRejectsCallbackStateAndIssuer(test *testing.T) {
	for _, field := range []string{"state", "iss"} {
		test.Run(field, func(test *testing.T) {
			fixture := workflowOAuthServer(test)
			config := workflowOAuthConfig(fixture)
			vault := filepath.Join(test.TempDir(), "credentials")
			manager := workflowOAuthManager(test, fixture, vault, config)
			authorizationURL, completed, _ := workflowStartSignIn(test, manager, config.ID)
			returned, err := url.Parse(workflowAuthorizationReturn(test, authorizationURL))
			if err != nil {
				test.Fatal(err)
			}
			query := returned.Query()
			query.Set(field, query.Get(field)+"-forged")
			returned.RawQuery = query.Encode()
			if err := manager.SubmitAuthorization(config.ID, returned.String()); err != nil {
				test.Fatal(err)
			}
			err = workflowAwaitError(test, "invalid callback rejection", completed)
			if err == nil || fixture.exchanges.Load() != 0 {
				test.Fatalf("forged callback %s exchanged a token: %v", field, err)
			}
			if field == "state" && !strings.Contains(err.Error(), "state mismatch") {
				test.Fatalf("state was rejected for an unrelated reason: %v", err)
			}
			if field == "iss" && !strings.Contains(err.Error(), "issuer") {
				test.Fatalf("issuer was rejected for an unrelated reason: %v", err)
			}
			if state := workflowState(test, manager, config.ID); state.State == "connected" || state.AuthPending || state.AuthURL != "" {
				test.Fatalf("forged callback left active authorization: %#v", state)
			}
			if _, err := os.Stat(workflowCredentialPath(vault, config)); !os.IsNotExist(err) {
				test.Fatalf("forged callback persisted credentials: %v", err)
			}
		})
	}
}

func TestManagedWorkflowOAuthCancellationAndRetry(test *testing.T) {
	fixture := workflowOAuthServer(test)
	config := workflowOAuthConfig(fixture)
	vault := filepath.Join(test.TempDir(), "credentials")
	manager := workflowOAuthManager(test, fixture, vault, config)
	authorizationURL, completed, cancel := workflowStartSignIn(test, manager, config.ID)
	staleReturn := workflowAuthorizationReturn(test, authorizationURL)
	cancel()
	if err := workflowAwaitError(test, "cancelled sign-in", completed); !errors.Is(err, context.Canceled) {
		test.Fatalf("sign-in cancellation lost its context outcome: %v", err)
	}
	if state := workflowState(test, manager, config.ID); state.State != "not started" || state.AuthPending || state.AuthURL != "" || fixture.exchanges.Load() != 0 {
		test.Fatalf("cancelled sign-in state %#v", state)
	}
	if err := manager.SubmitAuthorization(config.ID, staleReturn); err == nil {
		test.Fatal("cancelled authorization accepted a stale callback")
	}
	response, err := (&http.Client{Timeout: time.Second}).Get(staleReturn)
	if err == nil {
		_ = response.Body.Close()
		test.Fatal("cancelled loopback callback listener remained open")
	}
	if _, err := os.Stat(workflowCredentialPath(vault, config)); !os.IsNotExist(err) {
		test.Fatalf("cancelled sign-in persisted credentials: %v", err)
	}
	authorizationURL, completed, _ = workflowStartSignIn(test, manager, config.ID)
	if err := manager.SubmitAuthorization(config.ID, staleReturn); err == nil {
		test.Fatal("replacement authorization accepted the previous callback origin")
	}
	if err := manager.SubmitAuthorization(config.ID, workflowAuthorizationReturn(test, authorizationURL)); err != nil {
		test.Fatal(err)
	}
	if err := workflowAwaitError(test, "sign-in retry", completed); err != nil {
		test.Fatal(err)
	}
	if state := workflowState(test, manager, config.ID); state.State != "connected" || state.AuthPending || fixture.exchanges.Load() != 1 {
		test.Fatalf("sign-in retry state %#v", state)
	}
}

func TestManagedWorkflowOAuthStoredCredentialIdentityValidation(test *testing.T) {
	fixture := workflowOAuthServer(test)
	config := workflowOAuthConfig(fixture)
	vault := filepath.Join(test.TempDir(), "credentials")
	manager := workflowOAuthManager(test, fixture, vault, config)
	authorizationURL, completed, _ := workflowStartSignIn(test, manager, config.ID)
	if err := manager.SubmitAuthorization(config.ID, workflowAuthorizationReturn(test, authorizationURL)); err != nil {
		test.Fatal(err)
	}
	if err := workflowAwaitError(test, "credential fixture sign-in", completed); err != nil {
		test.Fatal(err)
	}
	saved := workflowReadCredential(test, vault, config)
	for _, field := range []string{"endpoint", "id"} {
		test.Run(field, func(test *testing.T) {
			wrong := saved
			if field == "endpoint" {
				wrong.Endpoint += "/another"
			} else {
				wrong.ID = "another_identity"
			}
			encoded, err := json.Marshal(wrong)
			if err != nil {
				test.Fatal(err)
			}
			otherVault := test.TempDir()
			if err := os.WriteFile(workflowCredentialPath(otherVault, config), encoded, 0600); err != nil {
				test.Fatal(err)
			}
			isolated := workflowOAuthManager(test, fixture, otherVault, config)
			before := fixture.requests.Load()
			ctx, cancel := context.WithTimeout(test.Context(), 3*time.Second)
			defer cancel()
			if err := isolated.Connect(ctx, config.ID); err == nil || !strings.Contains(err.Error(), "another endpoint") {
				test.Fatalf("stored credential's wrong %s was accepted: %v", field, err)
			}
			if fixture.requests.Load() != before || workflowState(test, isolated, config.ID).State == "connected" {
				test.Fatal("mismatched credential reached the endpoint")
			}
		})
	}
}

func TestManagedWorkflowOAuthExpiredRefreshAllowsExplicitSignIn(test *testing.T) {
	fixture := workflowOAuthServer(test)
	config := workflowOAuthConfig(fixture)
	vault := filepath.Join(test.TempDir(), "credentials")
	manager := workflowOAuthManager(test, fixture, vault, config)
	authorizationURL, completed, _ := workflowStartSignIn(test, manager, config.ID)
	if err := manager.SubmitAuthorization(config.ID, workflowAuthorizationReturn(test, authorizationURL)); err != nil {
		test.Fatal(err)
	}
	if err := workflowAwaitError(test, "initial sign-in", completed); err != nil {
		test.Fatal(err)
	}
	if err := manager.Disconnect(config.ID); err != nil {
		test.Fatal(err)
	}
	saved := workflowReadCredential(test, vault, config)
	saved.Token.Expiry = time.Now().Add(-time.Hour)
	encoded, err := json.Marshal(saved)
	if err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(workflowCredentialPath(vault, config), encoded, 0600); err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 5*time.Second)
	defer cancel()
	if err := manager.Connect(ctx, config.ID); err == nil || workflowState(test, manager, config.ID).State != "needs sign-in" || fixture.refreshes.Load() != 1 {
		test.Fatalf("expired refresh was not recoverable: %v, state=%#v, refreshes=%d", err, workflowState(test, manager, config.ID), fixture.refreshes.Load())
	}
	authorizationURL, completed, _ = workflowStartSignIn(test, manager, config.ID)
	if err := manager.SubmitAuthorization(config.ID, workflowAuthorizationReturn(test, authorizationURL)); err != nil {
		test.Fatal(err)
	}
	if err := workflowAwaitError(test, "explicit sign-in after expired refresh", completed); err != nil {
		test.Fatalf("expired credential prevented reauthentication: %v", err)
	}
	if fixture.exchanges.Load() != 2 || workflowState(test, manager, config.ID).State != "connected" {
		test.Fatal("explicit reauthentication did not replace the expired credential")
	}
}
