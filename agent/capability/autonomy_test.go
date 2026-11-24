package capability

import (
	"context"
	"errors"
	"os"
	"testing"

	captypes "crude/types/capability"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAutonomyStdioHelper(t *testing.T) {
	if os.Getenv("CRUDE_AUTONOMY_STDIO_HELPER") != "1" {
		return
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "autonomy-helper", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "lookup", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "local process evidence"}}}, nil
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestAuthorizedLocalProcessLaunchDiscoveryAccessAndUse(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	settings.Connections["helper"] = captypes.ConnectionConfig{ID: "helper", Name: "Helper", Scope: captypes.ScopeApplication, Transport: "stdio", Command: executable, Args: []string{"-test.run=^TestAutonomyStdioHelper$"}, Environment: map[string]string{"CRUDE_AUTONOMY_STDIO_HELPER": "1"}}
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	actor := captypes.RuntimeActor{ID: "root", RunID: "run", Effects: []captypes.Effect{captypes.EffectRead, captypes.EffectExecute, captypes.EffectDelete, captypes.EffectWrite, captypes.EffectCommunication}}
	_, err = service.Activate(t.Context(), actor, "helper", false)
	var launch *captypes.LaunchApprovalRequired
	if !errors.As(err, &launch) {
		t.Fatal(err)
	}
	result, err := service.Activate(launch.CapabilityApprovalContextFor(t.Context(), "yolo"), actor, "helper", false)
	if err != nil {
		t.Fatal(err)
	}
	_ = result
	tools := service.Snapshot().Connections[0].Tools
	if len(tools) != 1 {
		t.Fatalf("missing live stdio discovery: %+v", tools)
	}
	prepared, err := service.Prepare(t.Context(), actor)
	if err != nil || len(prepared.Tools) != 1 {
		t.Fatalf("missing prepared stdio tool: %+v %v", prepared, err)
	}
	result, err = service.Execute(t.Context(), actor, prepared, prepared.Tools[0].Name, map[string]any{})
	if err != nil || len(result.Content) != 1 || result.Content[0].Text != "local process evidence" {
		t.Fatalf("local execution: %+v %v", result, err)
	}
	config := service.Snapshot().Settings.Connections["helper"]
	if config.LaunchApproved || len(config.Grants) != 0 {
		t.Fatal("task launch/use authority became durable")
	}
}

func TestLocalLaunchRequiresExactExecutionAuthorityBeforeEffects(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	settings.Connections["local"] = captypes.ConnectionConfig{ID: "local", Name: "Local", Transport: "stdio", Command: "not-executed", Environment: map[string]string{"TOKEN": "secret"}}
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	actor := captypes.RuntimeActor{ID: "root", RunID: "run", Effects: []captypes.Effect{captypes.EffectRead}}
	if _, err := service.Activate(t.Context(), actor, "local", false); err == nil {
		t.Fatal("read ceiling launched host process")
	}
	actor.Effects = append(actor.Effects, captypes.EffectExecute)
	_, err := service.Activate(t.Context(), actor, "local", false)
	var approval *captypes.LaunchApprovalRequired
	if !errors.As(err, &approval) {
		t.Fatal(err)
	}
	if approval.Evidence()["connection"].(captypes.ConnectionConfig).Environment["TOKEN"] != "[redacted]" {
		t.Fatal("review leaked secret")
	}
	changed := settings.Connections["local"]
	changed.Args = []string{"changed"}
	settings.Connections["local"] = changed
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Activate(approval.CapabilityApprovalContextFor(t.Context(), "yolo"), actor, "local", false); err == nil {
		t.Fatal("changed launch preimage authorized")
	}
	changed.RequiredEnv = []string{"MISSING"}
	settings.Connections["local"] = changed
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	_, err = service.Activate(t.Context(), actor, "local", false)
	if err == nil || errors.As(err, &approval) {
		t.Fatal("missing credentials were approval eligible")
	}
}
