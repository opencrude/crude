package capability

import (
	"errors"
	"os"
	"strings"
	"testing"

	captypes "crude/types/capability"
)

// An agent configures a server's required setting itself, under the session's
// approval policy, instead of sending the user to Settings. One approval covers
// the durable change and launching exactly that configuration in the run.
func TestAgentConfiguredEnvironmentIsOneExactDurableApproval(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	settings.Connections["helper"] = captypes.ConnectionConfig{ID: "helper", Name: "Helper", Scope: captypes.ScopeApplication, Transport: "stdio",
		Command: executable, Args: []string{"-test.run=^TestAutonomyStdioHelper$"}, RequiredEnv: []string{"CRUDE_AUTONOMY_STDIO_HELPER"},
		SecretRefs: map[string]string{"HELPER_TOKEN": "env:HOME"}}
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	actor := captypes.RuntimeActor{ID: "root", RunID: "run", Effects: []captypes.Effect{captypes.EffectRead, captypes.EffectExecute}}
	if _, err := service.Activate(t.Context(), actor, "helper", false); err == nil || !strings.Contains(err.Error(), "CRUDE_AUTONOMY_STDIO_HELPER") || !strings.Contains(err.Error(), "environment") {
		t.Fatalf("missing setup did not name the variable and its repair: %v", err)
	}
	params := map[string]any{"id": "helper", "environment": map[string]any{"CRUDE_AUTONOMY_STDIO_HELPER": "1"}}
	_, err = service.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "capability_activate", params)
	var configuration *captypes.ConfigurationApprovalRequired
	if !errors.As(err, &configuration) {
		t.Fatalf("environment change was not an exact authorization request: %v", err)
	}
	if _, reason := configuration.CapabilityApproval(); !strings.Contains(reason, "CRUDE_AUTONOMY_STDIO_HELPER=1") {
		t.Fatalf("approver cannot see the exact value: %s", reason)
	}
	if service.Snapshot().Settings.Connections["helper"].Environment != nil {
		t.Fatal("environment changed before authorization")
	}
	if _, err := service.Execute(configuration.CapabilityApprovalContextFor(t.Context(), "reviewer"), actor, captypes.RuntimeSnapshot{}, "capability_activate", params); err != nil {
		t.Fatalf("approved configuration required another decision: %v", err)
	}
	if connectionState(service, "helper") != "connected" {
		t.Fatal("approved configuration did not launch")
	}
	if got := service.Snapshot().Settings.Connections["helper"].Environment["CRUDE_AUTONOMY_STDIO_HELPER"]; got != "1" {
		t.Fatalf("configuration is not durable: %q", got)
	}
	// Repeating the same values is a no-op, not another approval.
	if _, err := service.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "capability_activate", params); err != nil {
		t.Fatalf("unchanged environment asked again: %v", err)
	}
	// Credential references stay user-owned.
	shadow := map[string]any{"id": "helper", "environment": map[string]any{"HELPER_TOKEN": "guess"}}
	if _, err := service.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "capability_activate", shadow); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("plain value shadowed a credential reference: %v", err)
	}
	// Providers that force every schema property send an empty object.
	if _, err := service.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "capability_activate", map[string]any{"id": "helper", "environment": map[string]any{}}); err != nil {
		t.Fatalf("empty environment was not a no-op: %v", err)
	}
}
