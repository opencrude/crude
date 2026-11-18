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
	actor := captypes.RuntimeActor{ID: "root", RunID: "run", Effects: []captypes.Effect{captypes.EffectRead, captypes.EffectExecute}}
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
	params := map[string]any{"connection_id": "helper", "grants": []captypes.AccessRequest{{Tool: "lookup", Fingerprint: tools[0].Fingerprint, Effect: captypes.EffectRead, Scope: "fixture"}}}
	_, err = service.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "mcp_request_access", params)
	var access *captypes.AccessApprovalRequired
	if !errors.As(err, &access) {
		t.Fatal(err)
	}
	if _, err := service.Execute(access.CapabilityApprovalContextFor(t.Context(), "yolo"), actor, captypes.RuntimeSnapshot{}, "mcp_request_access", params); err != nil {
		t.Fatal(err)
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

func TestAutomaticAccessIsExactRunLocalAndRevocable(t *testing.T) {
	for _, origin := range []string{"yolo", "reviewer"} {
		t.Run(origin, func(t *testing.T) {
			service, actor, calls, _ := accessFixture(t)
			actor.SessionID, actor.RunID = "session", "run-1"
			params := observedAccess(t, service, actor)
			_, err := service.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "mcp_request_access", params)
			var approval *captypes.AccessApprovalRequired
			if !errors.As(err, &approval) {
				t.Fatal(err)
			}
			if _, err := service.Execute(approval.CapabilityApprovalContextFor(t.Context(), origin), actor, captypes.RuntimeSnapshot{}, "mcp_request_access", params); err != nil {
				t.Fatal(err)
			}
			if len(service.Snapshot().Settings.Connections["docs"].Grants) != 0 {
				t.Fatal("automatic access persisted trust")
			}
			prepared, err := service.Prepare(t.Context(), actor)
			if err != nil || len(prepared.Tools) != 1 {
				t.Fatalf("task tool unavailable: %+v %v", prepared, err)
			}
			if _, err := service.Execute(t.Context(), actor, prepared, prepared.Tools[0].Name, nil); err != nil {
				t.Fatal(err)
			}
			other := actor
			other.ID = "other"
			if snapshot, err := service.Prepare(t.Context(), other); err != nil || len(snapshot.Tools) != 0 {
				t.Fatal("grant escaped owner")
			}
			next := actor
			next.RunID = "run-2"
			if snapshot, err := service.Prepare(t.Context(), next); err != nil || len(snapshot.Tools) != 0 {
				t.Fatal("grant escaped run")
			}
			if _, err := service.Execute(t.Context(), actor, prepared, prepared.Tools[0].Name, nil); err == nil {
				t.Fatal("expired request executed")
			}
			if calls.Load() != 1 {
				t.Fatal("expired call reached server")
			}
			var recordedOrigin, run string
			if err := service.db.QueryRow(`SELECT origin,run_id FROM capability_authorizations WHERE phase='access'`).Scan(&recordedOrigin, &run); err != nil || recordedOrigin != origin || run != "run-1" {
				t.Fatalf("audit: %s %s %v", recordedOrigin, run, err)
			}
		})
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
