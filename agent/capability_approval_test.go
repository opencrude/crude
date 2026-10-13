package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"crude/provider"
	"crude/safety"
	"crude/tools"
	ttypes "crude/types"
	captypes "crude/types/capability"
)

func TestTypedApprovalUsesLiveAutonomyAndExactEvidence(t *testing.T) {
	for _, mode := range []string{"manual", "reviewer", "yolo"} {
		t.Run(mode, func(t *testing.T) {
			a := newPermissionLifecycleAgent(t)
			a.Yolo = mode == "yolo"
			request := &captypes.LaunchApprovalRequired{Preview: captypes.LaunchPreview{Token: "exact-token", Connection: captypes.ConnectionConfig{ID: "local", Transport: "stdio", Command: "test-helper", Environment: map[string]string{"TOKEN": "do-not-leak"}}}}
			reviews, blocks, calls := 0, 0, 0
			a.PermissionReviewer = func(_ context.Context, call provider.ToolCall, evidence map[string]any, risk safety.CommandRisk, _ func(AgentEvent)) PermissionReview {
				reviews++
				if mode != "reviewer" {
					t.Fatal("unexpected reviewer")
				}
				if evidence["phase"] != "launch" || !strings.Contains(call.Function.Arguments, "test-helper") || strings.Contains(call.Function.Arguments, "do-not-leak") || risk != safety.RiskUnknown {
					t.Fatalf("wrong review evidence: %s %+v", call.Function.Arguments, evidence)
				}
				return PermissionReview{Decision: "allow"}
			}
			a.ToolFn = func(ctx context.Context, _ string, _ map[string]any) (tools.ToolResult, error) {
				calls++
				if _, ok := ctx.Value(captypes.LaunchApprovedKey{}).(captypes.LaunchPreview); !ok {
					return tools.ToolResult{}, fmt.Errorf("wrapped: %w", request)
				}
				auth, ok := captypes.AuthorizationFrom(ctx)
				want := mode
				if mode == "manual" {
					want = "human"
				}
				if !ok || auth.Origin != want || auth.Token != "exact-token" {
					t.Fatalf("incorrect internal authority %+v", auth)
				}
				return tools.ToolResult{Content: "connected"}, nil
			}
			if mode == "manual" {
				a.PermissionReviewer = nil
			}
			responses := make(chan ttypes.BlockResponse, 1)
			call := provider.ToolCall{ID: "call", InternalID: "internal", Function: provider.FunctionCall{Name: "capability_activate", Arguments: `{"id":"local"}`}}
			result := a.executeOneToolCall(t.Context(), call, func(event AgentEvent) {
				if event.Type == "blocking" {
					blocks++
					responses <- ttypes.BlockResponse{RequestID: "internal", Value: "allow", Outcome: ttypes.BlockAnswered}
				}
			}, responses, new(string))
			if result.toolError || calls != 2 || (mode == "manual" && blocks != 1) || (mode != "manual" && blocks != 0) || (mode == "reviewer" && reviews != 1) {
				t.Fatalf("mode %s calls=%d blocks=%d reviews=%d result=%+v", mode, calls, blocks, reviews, result)
			}
		})
	}
}

func TestIdenticalTypedApprovalCannotPromptRepeatedly(t *testing.T) {
	a := newPermissionLifecycleAgent(t)
	a.Yolo = true
	request := &captypes.ApprovalRequired{ID: "exact", Reason: "same", Preview: captypes.AcquisitionPreview{Token: "same"}}
	calls := 0
	a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		calls++
		return tools.ToolResult{}, request
	}
	call := provider.ToolCall{ID: "call", Function: provider.FunctionCall{Name: "capability_activate", Arguments: `{"id":"exact"}`}}
	result := a.executeOneToolCall(t.Context(), call, func(AgentEvent) {}, nil, new(string))
	if !result.toolError || calls != 2 {
		t.Fatalf("repeated intent calls=%d result=%+v", calls, result)
	}
}
