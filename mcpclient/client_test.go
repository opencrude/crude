package mcpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	captypes "crude/types/capability"
)

type cancelledLifetimeTransport struct{ calls int }

func (transport *cancelledLifetimeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls++
	return nil, errors.New("cancelled lifetime reached transport")
}

func TestClosedMCPLifetimeCannotStartLateHTTPRequests(t *testing.T) {
	lifetime, cancel := context.WithCancel(t.Context())
	cancel()
	base := &cancelledLifetimeTransport{}
	endpoint, _ := url.Parse("https://example.test/mcp")
	transport := headerTransport{base: base, endpoint: endpoint, lifetime: lifetime}
	request, _ := http.NewRequest(http.MethodDelete, endpoint.String(), nil)
	if _, err := transport.RoundTrip(request); !errors.Is(err, context.Canceled) || base.calls != 0 {
		t.Fatalf("late teardown traffic escaped closed owner: calls=%d err=%v", base.calls, err)
	}
}

func managedServer(t *testing.T, toolName string, call func(context.Context) (*mcp.CallToolResult, error)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: toolName, Description: "Fixture tool", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return call(ctx)
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
	endpoint := httptest.NewServer(handler)
	t.Cleanup(endpoint.Close)
	return endpoint, calls
}

func TestManagedHTTPGrantAndPassiveConfigure(t *testing.T) {
	endpoint, calls := managedServer(t, "read", func(context.Context) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "verified output"}}}, nil
	})
	manager := New(t.TempDir())
	t.Cleanup(manager.Close)
	config := captypes.ConnectionConfig{ID: "fixture", Name: "Fixture", Transport: "http", URL: endpoint.URL}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if manager.States()[0].State != "not started" || calls.Load() != 0 {
		t.Fatal("configuration started runtime work")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Connect(ctx, config.ID); err != nil {
		t.Fatal(err)
	}
	tool := manager.States()[0].Tools[0]
	if tool.Selected {
		t.Fatal("server annotation granted a tool")
	}
	if _, err := manager.Call(ctx, tool, map[string]any{}); err == nil || calls.Load() != 0 {
		t.Fatal("ungranted operation executed")
	}
	config.Grants = map[string]captypes.ToolGrant{"read": {Effect: captypes.EffectRead, Scope: "workspace", Fingerprint: tool.Fingerprint}}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if err := manager.Connect(ctx, config.ID); err != nil {
		t.Fatal(err)
	}
	tool = manager.States()[0].Tools[0]
	result, err := manager.Call(ctx, tool, map[string]any{})
	if err != nil || len(result.Content) != 1 || result.Content[0].Text != "verified output" || calls.Load() != 1 {
		t.Fatalf("call result %#v: %v", result, err)
	}
	if err := manager.Disconnect(config.ID); err != nil {
		t.Fatal(err)
	}
	if !manager.States()[0].Cached || manager.States()[0].State != "not started" {
		t.Fatal("disconnect lost cached discovery")
	}
}

func TestChangedSchemaRevokesGrantAndToolFailure(t *testing.T) {
	endpoint, calls := managedServer(t, "write", func(context.Context) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "mutation refused"}}}, nil
	})
	manager := New(t.TempDir())
	t.Cleanup(manager.Close)
	config := captypes.ConnectionConfig{ID: "fixture", Name: "Fixture", Transport: "http", URL: endpoint.URL, Grants: map[string]captypes.ToolGrant{"write": {Effect: captypes.EffectWrite, Scope: "workspace", Fingerprint: "old-schema"}}}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Connect(ctx, config.ID); err != nil {
		t.Fatal(err)
	}
	tool := manager.States()[0].Tools[0]
	if tool.Selected || tool.Reason == "" {
		t.Fatal("changed schema kept old grant")
	}
	config.Grants["write"] = captypes.ToolGrant{Effect: captypes.EffectWrite, Scope: "workspace", Fingerprint: tool.Fingerprint}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if err := manager.Connect(ctx, config.ID); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Call(ctx, manager.States()[0].Tools[0], map[string]any{})
	if err != nil || !result.IsError || calls.Load() != 1 {
		t.Fatalf("server error must remain error %#v %v", result, err)
	}
}

func TestMutatingTimeoutIsUnknownWithoutRetry(t *testing.T) {
	endpoint, calls := managedServer(t, "write", func(ctx context.Context) (*mcp.CallToolResult, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "late"}}}, nil
		}
	})
	manager := New(t.TempDir())
	t.Cleanup(manager.Close)
	config := captypes.ConnectionConfig{ID: "fixture", Name: "Fixture", Transport: "http", URL: endpoint.URL}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Connect(ctx, config.ID); err != nil {
		t.Fatal(err)
	}
	tool := manager.States()[0].Tools[0]
	config.Grants = map[string]captypes.ToolGrant{"write": {Effect: captypes.EffectWrite, Scope: "workspace", Fingerprint: tool.Fingerprint}}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if err := manager.Connect(ctx, config.ID); err != nil {
		t.Fatal(err)
	}
	short, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stop()
	result, err := manager.Call(short, manager.States()[0].Tools[0], map[string]any{})
	if err == nil || !result.Unknown || calls.Load() != 1 {
		t.Fatalf("mutation %#v %v calls %d", result, err, calls.Load())
	}
}

func TestEndpointCredentialsDoNotCrossRedirects(t *testing.T) {
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		leaked.Store(request.Header.Get("Authorization") != "")
		writer.WriteHeader(http.StatusOK)
	}))
	defer other.Close()
	endpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, other.URL, http.StatusTemporaryRedirect)
	}))
	defer endpoint.Close()
	manager := New(t.TempDir())
	defer manager.Close()
	t.Setenv("MCP_SECRET_TEST", "test-secret")
	config := captypes.ConnectionConfig{ID: "private", Name: "Private", Transport: "http", URL: endpoint.URL, SecretRefs: map[string]string{"Authorization": "env:MCP_SECRET_TEST"}}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := manager.Connect(ctx, config.ID)
	if err == nil || leaked.Load() || strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("redirect leaked credential: %v %v", leaked.Load(), err)
	}
	if err := Validate(captypes.ConnectionConfig{ID: "bad", Name: "Bad", Transport: "http", URL: "https://user:secret@example.test"}); err == nil {
		t.Fatal("URL credentials accepted")
	}
	if !errors.Is(ctx.Err(), nil) {
		t.Log("request correctly bounded by context")
	}
}

func TestPackageConnectionRequiresDeclaredEnvironmentBeforeLaunch(t *testing.T) {
	manager := New(t.TempDir())
	defer manager.Close()
	config := captypes.ConnectionConfig{
		ID: "ghidra", Name: "Ghidra", Transport: "stdio", Command: "uvx",
		Args: []string{"ghidra-retro-mcp==1.0.0"}, LaunchApproved: true,
		RequiredEnv: []string{"GHIDRA_INSTALL_DIR"},
	}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	err := manager.Connect(t.Context(), config.ID)
	if err == nil || !strings.Contains(err.Error(), "GHIDRA_INSTALL_DIR") {
		t.Fatalf("missing package setup was not reported: %v", err)
	}
}
