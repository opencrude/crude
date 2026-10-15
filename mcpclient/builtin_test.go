package mcpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	captypes "crude/types/capability"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerEchoBuiltin(t *testing.T, name string) *atomic.Value {
	t.Helper()
	seen := new(atomic.Value)
	RegisterBuiltin(name, func(lookup func(string) string, _ int) (*mcp.Server, map[string]captypes.Effect) {
		server := mcp.NewServer(&mcp.Implementation{Name: name, Version: "1"}, nil)
		server.AddTool(&mcp.Tool{Name: "echo_key", Description: "Echo", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			key := lookup("FIXTURE_KEY")
			seen.Store(key)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "key=" + key}}}, nil
		})
		server.AddTool(&mcp.Tool{Name: "off", Description: "Turned off", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
		return server, map[string]captypes.Effect{"echo_key": captypes.EffectRead, "off": captypes.EffectRead}
	})
	return seen
}

func TestBuiltinServerStartsWithoutApprovalUsesPlainNamesAndStoredSecrets(t *testing.T) {
	registerEchoBuiltin(t, "fixture-builtin")
	manager := New(t.TempDir())
	manager.SetCredentialDirectory(t.TempDir())
	t.Cleanup(manager.Close)
	config := captypes.ConnectionConfig{ID: "fx", Name: "Fixture", Transport: captypes.TransportBuiltin, Command: "fixture-builtin",
		Grants: map[string]captypes.ToolGrant{"off": {Disabled: true}}}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if err := manager.SetSecrets(map[string]string{"FIXTURE_KEY": "s3cret"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := manager.Recover(ctx, config.ID); err != nil {
		t.Fatalf("built-in server did not start on first use: %v", err)
	}
	tools := map[string]captypes.Tool{}
	for _, tool := range manager.States()[0].Tools {
		tools[tool.Name] = tool
	}
	echo, off := tools["echo_key"], tools["off"]
	if !echo.Selected || echo.Effect != captypes.EffectRead || off.Selected {
		t.Fatalf("built-in selection echo=%+v off=%+v", echo, off)
	}
	result, err := manager.Call(ctx, echo, map[string]any{})
	if err != nil || len(result.Content) != 1 || result.Content[0].Text != "key=[redacted]" {
		t.Fatalf("call result=%+v err=%v (stored secrets must be read but never echoed back)", result, err)
	}
	if names := manager.SecretNames(); len(names) != 1 || names[0] != "FIXTURE_KEY" {
		t.Fatalf("secret names=%v", names)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(manager.credentialDir, secretsFile))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("secret store mode=%v err=%v", info, err)
		}
	}
	if err := manager.SetSecrets(map[string]string{"FIXTURE_KEY": ""}); err != nil || len(manager.SecretNames()) != 0 {
		t.Fatalf("removing a secret: names=%v err=%v", manager.SecretNames(), err)
	}
	if err := manager.SetSecrets(map[string]string{"bad name": "x"}); err == nil {
		t.Fatal("invalid secret name accepted")
	}
}

func TestSecretReferencesResolveFromStoreAndExplainMissingValues(t *testing.T) {
	manager := New(t.TempDir())
	manager.SetCredentialDirectory(t.TempDir())
	if err := manager.SetSecrets(map[string]string{"GITHUB_TOKEN": "ghp_x"}); err != nil {
		t.Fatal(err)
	}
	if value, err := manager.resolveSecret("secret:GITHUB_TOKEN"); err != nil || value != "ghp_x" {
		t.Fatalf("resolved=%q err=%v", value, err)
	}
	if _, err := manager.resolveSecret("secret:MISSING"); err == nil || !strings.Contains(err.Error(), "not set") {
		t.Fatalf("missing secret err=%v", err)
	}
	if _, err := manager.resolveSecret("plain"); err == nil || !strings.Contains(err.Error(), "secret:NAME") {
		t.Fatalf("invalid reference err=%v", err)
	}
}

func TestStreamableConnectionFallsBackToLegacySSE(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "legacy", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "ping", Description: "Ping", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
	})
	sse := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return server }, nil)
	var rejectedPosts atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A 2024-11-05 server: the initialize POST to the base URL is not
		// allowed; the SSE stream and its session endpoint are.
		if r.Method == http.MethodPost && r.URL.RawQuery == "" {
			rejectedPosts.Add(1)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		sse.ServeHTTP(w, r)
	}))
	t.Cleanup(endpoint.Close)
	manager := New(t.TempDir())
	t.Cleanup(manager.Close)
	config := captypes.ConnectionConfig{ID: "legacy", Name: "Legacy", Transport: "http", URL: endpoint.URL}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := manager.Connect(ctx, config.ID); err != nil {
		t.Fatalf("legacy server connect: %v", err)
	}
	state := manager.States()[0]
	if rejectedPosts.Load() == 0 || state.State != "connected" || len(state.Tools) != 1 || state.Tools[0].OriginalName != "ping" {
		t.Fatalf("fallback state=%+v rejected=%d", state, rejectedPosts.Load())
	}
	explicit := captypes.ConnectionConfig{ID: "explicit", Name: "Explicit", Transport: captypes.TransportSSE, URL: endpoint.URL}
	if err := Validate(explicit); err != nil {
		t.Fatalf("explicit SSE config rejected: %v", err)
	}
}

func TestRecoverReconnectsDroppedConnectionButRespectsUserDisconnect(t *testing.T) {
	registerEchoBuiltin(t, "fixture-recover")
	manager := New(t.TempDir())
	manager.SetCredentialDirectory(t.TempDir())
	t.Cleanup(manager.Close)
	config := captypes.ConnectionConfig{ID: "rc", Name: "Recover", Transport: captypes.TransportBuiltin, Command: "fixture-recover"}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := manager.Recover(ctx, config.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Restart(ctx, config.ID); err != nil || manager.States()[0].State != "connected" {
		t.Fatalf("restart state=%s err=%v", manager.States()[0].State, err)
	}
	if err := manager.Disconnect(config.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Recover(ctx, config.ID); err != ErrNotRecoverable || manager.States()[0].State == "connected" {
		t.Fatalf("recovery overrode a user disconnect: %v", err)
	}
	if err := manager.Connect(ctx, config.ID); err != nil {
		t.Fatal(err)
	}
	stdio := captypes.ConnectionConfig{ID: "local", Name: "Local", Transport: "stdio", Command: "never-launched"}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config, stdio.ID: stdio})
	if err := manager.Recover(ctx, stdio.ID); err != ErrNotRecoverable {
		t.Fatalf("unapproved local server must not be launched by recovery: %v", err)
	}
}
