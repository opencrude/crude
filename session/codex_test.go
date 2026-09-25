package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/agent"
	"crude/codexio"
	"crude/fileio"
	"crude/safety"
)

func TestNativePatchSessionHistoryAndChildOwnership(t *testing.T) {
	s, err := NewWithSessionDir("codex-session", testConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	s.SetWorkDir(root)
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

func TestGPTPromptCacheTracksModelWithoutIdentityCommentary(t *testing.T) {
	s, err := NewWithSessionDir("codex-prompts", testConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, model := range []string{"gpt-5.5", "gpt-6-astra", "claude-test", "gpt-5.5"} {
		prompt := s.systemPromptForModel(false, model)
		if codexio.IsGPT(model) {
			base, _ := codexio.Instructions(model)
			if !strings.HasPrefix(prompt, base) {
				t.Fatal("stale model prompt")
			}
			if strings.Contains(prompt, "<agent_identity>") || strings.Contains(prompt, "host application is Crude") {
				t.Fatal("identity commentary leaked")
			}
		}
	}
}

func TestPatchAutoReviewDoesNotApproveUnseenDeleteOrMove(t *testing.T) {
	root := t.TempDir()
	for _, body := range []string{"*** Delete File: gone", "*** Update File: a\n*** Move to: b\n-a\n+b", "*** Add File: ../outside\n+x"} {
		if _, ok := permissionReviewPayload(root, "apply_patch", map[string]any{"input": "*** Begin Patch\n" + body + "\n*** End Patch"}, safety.RiskProjectWrite, 4096); ok {
			t.Fatal("auto-approved operation needing human review")
		}
	}
}
