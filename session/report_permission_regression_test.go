package session

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"crude/agent"
	"crude/collaboration"
	"crude/provider"
	"crude/safety"
	"crude/tools"
	captypes "crude/types/capability"
	recoverytypes "crude/types/recovery"
)

func TestChildReportTaskAllowlistMatchesPromptSchemaAndExecution(t *testing.T) {
	for _, category := range []string{CategoryResearcher, CategoryCrafter, CategoryAeron} {
		t.Run(category, func(t *testing.T) {
			s, store, parent, child := collaborationContractFixture(t)
			run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, Origin: collaboration.OriginPeer})
			if err != nil {
				t.Fatal(err)
			}
			spec := agent.SubAgentSpec{ID: agent.AgentID(child.ID), ParentID: agent.AgentID(parent.ID), Category: category, AllowedTools: []string{"read_file"}}
			constrainConfiguredTools(&spec, []string{"read_file"}, []string{"delete_file"})
			if err := validateChildReportPermission(spec); err != nil {
				t.Fatal(err)
			}
			if slices.Contains(spec.DeniedTools, "publish_report") {
				t.Fatal("task allowlist denied mandatory reporting")
			}
			live := agent.NewAgent(provider.FromPreset("openai-chat", "key", ""), "test", s.logger)
			live.AgentID, live.ParentID, live.Depth = spec.ID, spec.ParentID, 1
			live.Policy = agent.ComposePolicy(safety.DefaultPolicy(), "default", category)
			live.FilteredTools = agent.FilterTools(tools.AgentTools, spec.AllowedTools, spec.DeniedTools, category, true)
			live.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
				t.Error("report escaped collaboration owner")
				return tools.ToolResult{}, nil
			}
			s.bindAgentRuntime(t.Context(), live, captypes.RuntimeActor{ID: child.ID, ParentID: parent.ID, AllowedTools: spec.AllowedTools, DeniedTools: spec.DeniedTools}, "child")
			definitions, _ := live.PrepareStep()
			if !slices.ContainsFunc(definitions, func(d tools.ToolDef) bool { return d.Function.Name == "publish_report" }) {
				t.Fatal("required report not offered")
			}
			ctx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{ToolCallID: "required-report"})
			result, err := live.ToolFn(ctx, "publish_report", map[string]any{"recipient_ids": []any{parent.ID}, "kind": "final", "summary": "Scoped final", "body": "Exact delegated evidence."})
			if err != nil {
				t.Fatalf("offered report cannot execute: %v", err)
			}
			var published collaboration.PublishReportResult
			if err := json.Unmarshal([]byte(result.Content), &published); err != nil || published.Artifact.AuthorID != child.ID || published.Artifact.RunID != run.ID {
				t.Fatalf("report provenance: %s %v", result.Content, err)
			}
			if _, err := live.ToolFn(ctx, "delete_file", map[string]any{"path": "other"}); err == nil {
				t.Fatal("report allowance widened task permissions")
			}
		})
	}
}

func TestChildExplicitReportDenialRejectedBeforeLaunch(t *testing.T) {
	s, _, _, _ := collaborationContractFixture(t)
	s.cfg = modelAttributionConfig("http://127.0.0.1:9", "test")
	runner := NewSubAgentRunner(s, 1)
	t.Cleanup(runner.SessionCancel)
	_, err := runner.StartAgent(t.Context(), map[string]any{"category": CategoryCrafter, "description": "must not launch", "denied_tools": []string{"publish_report"}})
	if err == nil || !strings.Contains(err.Error(), "required publish_report") {
		t.Fatalf("explicit report conflict was not actionable at admission: %v", err)
	}
}

func TestUnavailableReportParksWithoutProviderOrReminderLoop(t *testing.T) {
	home := t.TempDir()
	s, err := NewWithSessionDir("", modelAttributionConfig("http://127.0.0.1:9", "test"), home, home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	live := agent.NewAgent(provider.FromPreset("openai-chat", "key", "http://127.0.0.1:9"), "test", s.logger)
	live.Depth = 1
	live.FilteredTools = agent.FilterTools(tools.AgentTools, []string{"read_file"}, nil, "crafter", true)
	supervisor := agent.NewSupervisor("child", 1, nil)
	supervisor.Persist = func(status recoverytypes.Status) error { return nil }
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result := live.RunSupervised(ctx, []provider.ChatMessage{provider.NewTextMessage("user", "assignment")}, "system", nil, nil, "child", nil,
		agent.SupervisedRunOptions{Supervisor: supervisor, OnAttention: func(f agent.RecoveryFailure) agent.AttentionDisposition { return agent.AttentionParkAttempt }})
	if result.State != agent.SupervisedRunParked || result.Failure.Kind != agent.RecoveryFailureConfiguration || !strings.Contains(result.Failure.Cause.Error(), "publish_report") {
		t.Fatalf("unavailable report entered retry loop: %+v", result)
	}
}

func TestModelNativeToolSelectionMatchesRuntimeDispatch(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "allowed", true: "denied"}[denied], func(t *testing.T) {
			s, _, _, child := collaborationContractFixture(t)
			live := agent.NewAgent(provider.FromPreset("openai-chat", "key", ""), "gpt-5", s.logger)
			live.Policy = safety.DefaultPolicy()
			allowed := []string{"execute_command", "write_file", "edit_file"}
			var deny []string
			if denied {
				deny = []string{"execute_command", "write_file", "edit_file"}
			}
			live.FilteredTools = agent.FilterToolsForModel(tools.AgentTools, allowed, deny, CategoryCrafter, true, live.Model)
			calls := 0
			live.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
				calls++
				return tools.ToolResult{}, nil
			}
			s.bindAgentRuntime(t.Context(), live, captypes.RuntimeActor{ID: child.ID, ParentID: "parent", AllowedTools: allowed, DeniedTools: deny}, "child")
			for _, name := range []string{"exec_command", "write_stdin", "apply_patch"} {
				_, err := live.ToolFn(t.Context(), name, map[string]any{})
				if denied && err == nil || !denied && err != nil {
					t.Fatalf("%s offered/dispatch disagreement: %v", name, err)
				}
			}
			if denied && calls != 0 || !denied && calls != 3 {
				t.Fatalf("executed=%d", calls)
			}
		})
	}
}
