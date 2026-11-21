package core

import (
	"strings"
	"testing"
	"time"

	"crude/agent/prompt"
	"crude/types"
)

// A problem reaches Errand once it has stayed the same for attentionRipe, at
// most attentionBatch at a time and never twice for the same cause. A new
// cause of the same issue is raised again; a resolved one is not.
func TestAttentionRaisesEachCauseOnceAfterItSettles(t *testing.T) {
	now := time.Now()
	failed := types.IssueSnapshot{ID: "lsp:c", Severity: types.SeverityError, Summary: "clangd failed",
		Detail: "clangd stopped unexpectedly 5 times in 3 minutes.", Investigate: true, UpdatedAt: now.Add(-attentionRipe)}
	fresh := types.IssueSnapshot{ID: "lsp-results:c", Summary: "clangd reports errors in 1 of 1 checked files",
		Detail: "Errors in many files.", Investigate: true, UpdatedAt: now.Add(-time.Second)}
	human := types.IssueSnapshot{ID: "git", Summary: "Source control failed", UpdatedAt: now.Add(-time.Hour)}
	resolved := failed
	resolved.ID, resolved.ResolvedAt = "lsp:old", now

	due, next := dueAttention(now, []types.IssueSnapshot{failed, fresh, human, resolved}, nil)
	if len(due) != 1 || due[0].ID != failed.ID {
		t.Fatalf("due = %+v", due)
	}
	if next <= 0 || next > attentionRipe {
		t.Fatalf("the fresh issue does not wake the check when it ripens: %v", next)
	}
	sent := map[string]bool{attentionKey(failed): true}
	if due, _ := dueAttention(now.Add(attentionRipe), []types.IssueSnapshot{failed}, sent); len(due) != 0 {
		t.Fatalf("the same cause was raised twice: %+v", due)
	}
	changed := failed
	changed.Detail = "clangd failed to start: permission denied"
	if due, _ := dueAttention(now, []types.IssueSnapshot{changed}, sent); len(due) != 1 {
		t.Fatalf("a new cause of the same issue was not raised: %+v", due)
	}
	many := make([]types.IssueSnapshot, 0, attentionBatch+2)
	for i := range attentionBatch + 2 {
		issue := failed
		issue.ID = "lsp:" + string(rune('a'+i))
		many = append(many, issue)
	}
	if due, _ := dueAttention(now, many, nil); len(due) != attentionBatch {
		t.Fatalf("one evaluation raised %d notices", len(due))
	}
}

// Errand takes a notice only while it waits for work: not during a turn, not
// while it waits for an answer, and not after the user stopped it.
func TestAttentionWaitsForAnIdleErrand(t *testing.T) {
	for phase, want := range map[types.AgentPhase]bool{
		types.AgentIdle: true, types.AgentCompleted: true,
		types.AgentThinking: false, types.AgentCancelled: false, types.AgentFailed: false,
	} {
		if got := errandReady(types.AgentStatusSnapshot{Phase: phase}); got != want {
			t.Fatalf("phase %s ready=%v", phase, got)
		}
	}
	if errandReady(types.AgentStatusSnapshot{Phase: types.AgentIdle, NeedsAction: true}) {
		t.Fatal("Errand took a notice while it waited for the user")
	}
}

// The notice carries Crude's own summary only, never the server's text.
func TestAttentionNoticeIsCrudesSummary(t *testing.T) {
	body := prompt.AttentionNotice("clangd failed.")
	if body != "Crude found a problem in this workspace: clangd failed. Investigate it." || strings.Contains(body, "<") {
		t.Fatalf("notice = %q", body)
	}
}
