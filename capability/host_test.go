package capability

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"testing"
	"time"

	captypes "crude/types/capability"
	"crude/types/control"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestShellHostSDKFixture(t *testing.T) {
	if os.Getenv("CRUDE_SHELL_HOST_FIXTURE") != "yes" {
		return
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "exact-shell-fixture", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "picture", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "shell fixture"}, &mcp.ImageContent{Data: []byte{1, 2, 3, 4}, MIMEType: "image/png"}}}, nil
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
func hostFixtureRequest(t *testing.T) captypes.HostRequest {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	owner := control.ShellOwner{ClientID: "shell-a", Epoch: 3}
	return captypes.HostRequest{ID: "connect", WorkspaceID: "workspace", Actor: captypes.RuntimeActor{ID: "agent", SessionID: "session", RunID: "run", RuntimeEpoch: 1, TurnGeneration: 1, WorkspaceID: "workspace", ShellOwner: &owner, Effects: []captypes.Effect{captypes.EffectRead, captypes.EffectExecute}}, Operation: "connect", Authorized: true, Connection: captypes.ConnectionConfig{ID: "fixture", Name: "fixture", Host: "shell", Directory: t.TempDir(), Transport: "stdio", Command: executable, Args: []string{"-test.run=^TestShellHostSDKFixture$"}, SecretRefs: map[string]string{"CRUDE_SHELL_HOST_FIXTURE": "env:CRUDE_SHELL_HOST_FIXTURE"}}}
}
func TestShellWorkerSDKExactOwnerMediaAndPassiveReconnect(t *testing.T) {
	t.Setenv("CRUDE_SHELL_HOST_FIXTURE", "yes")
	r := hostFixtureRequest(t)
	worker := NewHostWorker(t.Context(), *r.Actor.ShellOwner, r.WorkspaceID, t.TempDir())
	t.Cleanup(worker.Close)
	peer := NewHostWorker(t.Context(), control.ShellOwner{ClientID: "shell-b", Epoch: 3}, r.WorkspaceID, t.TempDir())
	defer peer.Close()
	if reply := peer.Handle(t.Context(), r); reply.Error == "" {
		t.Fatal("peer accepted request")
	}
	connected := worker.Handle(t.Context(), r)
	if connected.Error != "" {
		t.Fatal(connected.Error)
	}
	if len(connected.States) != 1 || len(connected.States[0].Tools) != 1 {
		t.Fatalf("missing actual SDK inventory: %+v", connected)
	}
	tool := connected.States[0].Tools[0]
	tool.Selected = true
	tool.Effect = captypes.EffectRead
	tool.Scope = "fixture"
	grant := captypes.ToolGrant{Fingerprint: tool.Fingerprint, Effect: tool.Effect, Scope: tool.Scope}
	call := r
	call.ID = "picture"
	call.Operation = "call"
	call.Tool = tool
	call.Grant = &grant
	reply := worker.Handle(t.Context(), call)
	if reply.Error != "" {
		t.Fatal(reply.Error)
	}
	if len(reply.Result.Content) != 2 || reply.Result.Content[0].Text != "shell fixture" || reply.Result.Content[1].Data != base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4}) {
		t.Fatalf("lost ordered media: %+v", reply.Result)
	}
	// Read-only request boundaries release both request and response budgets.
	for i := 0; i < 300; i++ {
		prepared := r
		prepared.ID = fmt.Sprintf("prepare-%d", i)
		prepared.Operation = "prepare"
		if result := worker.Handle(t.Context(), prepared); result.Error != "" {
			t.Fatalf("routine discovery exhausted after %d: %s", i, result.Error)
		}
	}
	worker.mu.Lock()
	retained := len(worker.receipts)
	worker.mu.Unlock()
	if retained != 1 {
		t.Fatalf("read receipts retained: %d", retained)
	}
	// Receipt reuse observes a retained outcome, never invokes the server twice.
	if second := worker.Handle(t.Context(), call); second.Result.Content[1].Data != reply.Result.Content[1].Data {
		t.Fatal("receipt changed")
	}
	worker.Close()
	call.ID = "after-detach"
	if reply := worker.Handle(t.Context(), call); reply.Error == "" {
		t.Fatal("detached shell ran")
	}
	replacement := NewHostWorker(t.Context(), control.ShellOwner{ClientID: "shell-a", Epoch: 4}, r.WorkspaceID, t.TempDir())
	defer replacement.Close()
	if reply := replacement.Handle(t.Context(), r); reply.Error == "" {
		t.Fatal("old epoch launched on replacement")
	}
	fresh := r
	owner := control.ShellOwner{ClientID: "shell-a", Epoch: 4}
	fresh.Actor.ShellOwner = &owner
	fresh.Operation = "prepare"
	fresh.ID = "passive"
	if reply := replacement.Handle(t.Context(), fresh); reply.Error != "" || reply.States[0].State != "not started" {
		t.Fatalf("passive reconnect launched: %+v", reply)
	}
}
func TestCoreManagerNeverLaunchesShellAndNoDispatcherFallback(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	r := hostFixtureRequest(t)
	settings.Connections = map[string]captypes.ConnectionConfig{r.Connection.ID: r.Connection}
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if err := service.clients.Connect(t.Context(), r.Connection.ID); err == nil {
		t.Fatal("core launched shell host")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := service.hostRequest(ctx, r.Actor, r.Connection, "connect", captypes.HostRequest{Authorized: true}); err == nil {
		t.Fatal("missing exact shell fell back")
	}
}
