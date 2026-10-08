package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/agent"
	"crude/collaboration"
	"crude/fileio"
	"crude/safety"
	"crude/tools"
)

func TestNativePatchSessionHistoryAndChildOwnership(t *testing.T) {
	s, err := NewWithSessionDir("codex-session", testConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	s.SetWorkDir(root)
	store := attachDelegationTestCollaboration(t, s)
	if _, err := s.registerCollaborator(t.Context(), "root-agent", "root", s.sessionID, "", CategoryDefault, s.db); err != nil {
		t.Fatal(err)
	}
	registered, err := s.registerCollaborator(t.Context(), "codex-child", "child", "child-session", "root-agent", CategoryCrafter, s.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartRun(t.Context(), collaboration.StartRunRequest{AgentID: registered.ID, RuntimeEpoch: registered.RuntimeEpoch, RequestID: "patch-assignment", Origin: collaboration.OriginPeer}); err != nil {
		t.Fatal(err)
	}
	var receipts []HistoryMutation
	s.SetHistoryMutationHook(func(_ context.Context, m HistoryMutation) error {
		if m.Committed == nil {
			receipts = append(receipts, m)
		}
		return nil
	})
	r := NewSubAgentRunner(s, 2)
	r.SetParentToolFn(s.newToolFn(true))
	spec := agent.SubAgentSpec{ID: "codex-child", Name: "child", Policy: safety.DefaultPolicy()}
	tool := r.buildProxiedToolFn(spec, nil, nil)
	input := "*** Begin Patch\n*** Add File: original\n+old\n*** Update File: original\n*** Move to: nested/renamed\n-old\n+new\n*** End Patch"
	ctx := withHistoryScope(t.Context(), "session", "message", "root-agent")
	ctx = context.WithValue(ctx, tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: "codex-child", ToolCallID: "patch-effect"})
	result, err := tool(ctx, "apply_patch", map[string]any{"input": input})
	if err != nil || !strings.Contains(result.Content, "```diff") {
		t.Fatalf("%s %v", result.Content, err)
	}
	got, err := os.ReadFile(filepath.Join(root, "nested/renamed"))
	if err != nil || string(got) != "new\n" {
		t.Fatalf("%q %v", got, err)
	}
	if len(receipts) != 3 {
		t.Fatalf("receipts %d", len(receipts))
	}
	for _, m := range receipts {
		if m.AgentID != "codex-child" || m.MessageID != "message" || m.Receipt.OperationName != "apply_patch" {
			t.Fatalf("provenance %+v", m)
		}
	}
	if receipts[2].Receipt.Paths[0].After.Kind != fileio.MutationAbsent {
		t.Fatal("source removal missing")
	}
	other := r.buildProxiedToolFn(agent.SubAgentSpec{ID: "other", Name: "other", Policy: safety.DefaultPolicy()}, nil, nil)
	_, err = other(ctx, "apply_patch", map[string]any{"input": "*** Begin Patch\n*** Update File: nested/renamed\n-new\n+bad\n*** End Patch"})
	if err == nil {
		t.Fatal("patch bypassed ownership")
	}
}

func TestSystemPromptCacheUsesCanonicalPolicyAcrossModels(t *testing.T) {
	s, err := NewWithSessionDir("codex-prompts", testConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var canonical string
	for _, model := range []string{"gpt-5.5", "gpt-6-astra", "claude-test", "gemini-test", "gpt-5.5"} {
		prompt := s.systemPromptForModel(false, model)
		if !strings.Contains(prompt, `<crude_agent version="3">`) {
			t.Fatal("model switch lost canonical root prompt")
		}
		if canonical == "" {
			canonical = prompt
		} else if prompt != canonical {
			t.Fatalf("model %q received a divergent behavioral prompt", model)
		}
	}
}

func TestPatchAutoReviewSendsExactDeleteAndMoveForDecision(t *testing.T) {
	root := t.TempDir()
	for _, body := range []string{"*** Delete File: gone", "*** Update File: a\n*** Move to: b\n-a\n+b", "*** Add File: ../outside\n+x"} {
		input := "*** Begin Patch\n" + body + "\n*** End Patch"
		payload, ok := permissionReviewPayload(root, "apply_patch", map[string]any{"input": input}, safety.RiskProjectWrite, 4096)
		var envelope struct {
			Arguments map[string]string `json:"arguments"`
		}
		if !ok || json.Unmarshal(payload, &envelope) != nil || envelope.Arguments["input"] != input {
			t.Fatal("reviewer did not receive exact patch operation")
		}
	}
}
