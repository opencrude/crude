package chatprojection

import (
	"strings"
	"testing"
	"time"

	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

func TestLifecycleStatusRendersAsToolShapedChatRow(t *testing.T) {
	renderer := NewRenderer(theme.DefaultDark())
	renderer.SetSize(100, 20)
	renderer.SetChatAreaBounds(0, 12)
	renderer.SetSpinFrame(0)
	renderer.SyncMessages([]types.ChatMessage{{
		InternalID: "turn-status", Role: types.RoleStatus,
		LifecyclePhase: "preparing", LifecycleProgress: 42,
		LifecycleUploadedBytes: 43008, LifecycleTotalBytes: 102400, Time: time.Now(),
	}}, 1)

	rows, _, _ := renderer.ExportMessageRows(100)
	if len(rows) != 1 {
		t.Fatalf("rows = %#v", rows)
	}
	joined := strings.Join(rows[0].Lines, "\n")
	if !strings.Contains(joined, "│ ● Getting next turn ready · ↑ 42 KB / 100 KB · 42%") {
		t.Fatalf("preparing lifecycle row = %q", joined)
	}
	renderer.SetSpinFrame(ui.ActivityBlinkPhaseMillis)
	renderer.InvalidateCache()
	rows, _, _ = renderer.ExportMessageRows(100)
	if joined = strings.Join(rows[0].Lines, "\n"); !strings.Contains(joined, "│ ○ Getting next turn ready") {
		t.Fatalf("hollow lifecycle phase = %q", joined)
	}
}

func TestPlainTerminalStatusNeverAnimates(t *testing.T) {
	msg := types.ChatMessage{InternalID: "history-status", Role: types.RoleStatus, Content: "Restored 1 file from Local History"}
	first := renderLifecycleMessage(msg, renderContext{spinFrame: 0})
	second := renderLifecycleMessage(msg, renderContext{spinFrame: ui.ActivityBlinkPhaseMillis})
	if first.frameSensitive || second.frameSensitive {
		t.Fatalf("plain status remained frame-sensitive: first=%v second=%v", first.frameSensitive, second.frameSensitive)
	}
	if len(first.native) != 1 || len(second.native) != 1 || first.native[0].PlainText() != second.native[0].PlainText() {
		t.Fatalf("plain status changed across animation phases: first=%q second=%q", first.native[0].PlainText(), second.native[0].PlainText())
	}
	if !strings.Contains(first.native[0].PlainText(), "● Restored") {
		t.Fatalf("plain status marker=%q", first.native[0].PlainText())
	}
}

func TestHistoryRestoreUsesPermanentContextBoundaryTable(t *testing.T) {
	msg := types.ChatMessage{InternalID: "history", Role: types.RoleStatus, LifecyclePhase: "history_restored", LifecycleOperationID: "plan-1234567890abcdef",
		HistoryPathCount: 1, HistoryTargetSnapshot: "target-1234567890", HistorySafetySnapshot: "safety-1234567890", Time: time.Now()}
	rendered := renderLifecycleMessage(msg, renderContext{innerW: 92, spinFrame: 0})
	if rendered.frameSensitive || len(rendered.native) != 8 {
		t.Fatalf("history boundary rows=%d frameSensitive=%v", len(rendered.native), rendered.frameSensitive)
	}
	joined := strings.Join(rendered.lines, "\n")
	for _, want := range []string{"Workspace restored", "1 file restored", "agent-scoped", "Target target-12345", "Safety safety-12345", "Conversation retained", "context updated"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("history boundary missing %q:\n%s", want, joined)
		}
	}
	second := renderLifecycleMessage(msg, renderContext{innerW: 92, spinFrame: ui.ActivityBlinkPhaseMillis})
	if strings.Join(second.lines, "\n") != joined {
		t.Fatal("history boundary changed across animation phases")
	}
}

func TestLifecycleAnimationRequiresExplicitActivePhase(t *testing.T) {
	for _, phase := range []string{"", "history_restored", "compacted", "compaction_completed", "compaction_cancelled", "compaction_failed", "compaction_interrupted", "unknown_future_state"} {
		if lifecycleMessageFrameSensitive(types.ChatMessage{Role: types.RoleStatus, LifecyclePhase: phase}) {
			t.Fatalf("terminal or unknown phase %q animated", phase)
		}
	}
	for _, phase := range []string{"preparing", "waiting_provider", "compacting", "cancelling", "compaction_extracting", "compaction_retrying", "compaction_committing", "compaction_cancelling"} {
		if !lifecycleMessageFrameSensitive(types.ChatMessage{Role: types.RoleStatus, LifecyclePhase: phase}) {
			t.Fatalf("active phase %q did not animate", phase)
		}
	}
}

func TestLifecycleStatusProjectionTransformsInPlaceToTool(t *testing.T) {
	renderer := NewRenderer(theme.DefaultDark())
	renderer.SetSize(100, 20)
	renderer.SetChatAreaBounds(0, 12)
	renderer.SyncMessages([]types.ChatMessage{{
		InternalID: "stable-row", Role: types.RoleStatus,
		LifecyclePhase: "waiting_provider", LifecycleProvider: "OpenAI", Time: time.Now().Add(-2 * time.Second),
	}}, 1)

	waiting, _, _ := renderer.ExportMessageRows(100)
	if len(waiting) != 1 || !strings.Contains(strings.Join(waiting[0].Lines, "\n"), "Waiting for OpenAI · 2s") {
		t.Fatalf("provider wait row = %#v", waiting)
	}

	renderer.SyncMessages([]types.ChatMessage{{
		InternalID: "stable-row", Role: types.RoleTool,
		ToolID: "tool-1", ToolName: "edit_file", ToolStatus: types.ToolStatusStreaming,
		ToolPreviewPhase: types.ToolPreviewPreparing, ToolStartTime: time.Now(),
	}}, 2)
	toolRows, _, _ := renderer.ExportMessageRows(100)
	if len(toolRows) != 1 || toolRows[0].ID != "stable-row" {
		t.Fatalf("tool transform changed row identity: %#v", toolRows)
	}
	joined := strings.Join(toolRows[0].Lines, "\n")
	if !strings.Contains(joined, "Preparing Edit File") || strings.Contains(joined, "Waiting for provider") {
		t.Fatalf("transformed tool row = %q", joined)
	}
}

func TestCompactionLifecycleUsesRetainedBoundaryTable(t *testing.T) {
	renderer := NewRenderer(theme.DefaultDark())
	renderer.SetSize(100, 20)
	renderer.SetChatAreaBounds(0, 12)
	msg := types.ChatMessage{
		InternalID: "compact", Role: types.RoleStatus, LifecyclePhase: "compaction_extracting",
		LifecycleOperationID: "compact-1", LifecycleStageName: "Capturing state",
		LifecycleStageIndex: 3, LifecycleCompleted: 2, LifecycleStageCount: 4,
		LifecycleBeforeTokens: 420000, Time: time.Now().Add(-2 * time.Second),
	}
	renderer.SyncMessages([]types.ChatMessage{msg}, 1)
	rows, _, _ := renderer.ExportMessageRows(100)
	joined := strings.Join(rows[0].Lines, "\n")
	if len(rows[0].Lines) != 7 || !rows[0].ContextBoundary || rows[0].ContextBoundaryStart != 1 ||
		!strings.Contains(joined, "Crude Aeron") || !strings.Contains(joined, "Compaction") ||
		!strings.Contains(joined, "Capturing state") ||
		!strings.Contains(joined, "■ ■ ● □") || !strings.Contains(joined, "420K") || !strings.Contains(joined, "esc cancel") {
		t.Fatalf("compaction lifecycle = %q", joined)
	}
	if strings.Contains(joined, "●  Compaction") || strings.Contains(joined, "Context       ") || strings.Contains(joined, "Stage         ") {
		t.Fatalf("compaction retained obsolete labels or two-column marker gap: %q", joined)
	}
	direct := renderCompactionMessage(msg, renderContext{innerW: 92, contentWidth: 76, legacyModelFallback: "aeron/model", spinFrame: 1})
	for index := 1; index < len(direct.native)-1; index++ {
		if got := direct.native[index].VisibleWidth(); got != 92 {
			t.Fatalf("row %d width=%d want right column aligned with 92-column header", index, got)
		}
	}
}

func TestCompactionTerminalFormsKeepTableGeometry(t *testing.T) {
	for _, phase := range []string{"compaction_completed", "compaction_cancelled", "compaction_failed", "compaction_interrupted"} {
		msg := types.ChatMessage{
			InternalID: "compact", Role: types.RoleStatus, LifecyclePhase: phase,
			LifecycleOperationID: "compact-1", LifecycleStageName: "Capturing state",
			LifecycleStageIndex: 3, LifecycleCompleted: 2, LifecycleStageCount: 4,
			LifecycleBeforeTokens: 155955, LifecycleAfterTokens: 10476,
			LifecycleTrigger: "manual", Content: "network connection stalled",
		}
		if phase == "compaction_completed" {
			msg.LifecycleCompleted = 4
			msg.LifecycleStageIndex = 4
		}
		msg.ModelID = "aeron/model"
		rendered := renderCompactionMessage(msg, renderContext{contentWidth: 76, innerW: 92})
		if len(rendered.native) != 6 || strings.TrimSpace(rendered.native[0].PlainText()) != "" || strings.TrimSpace(rendered.native[5].PlainText()) != "" {
			t.Fatalf("%s geometry=%d lines=%q", phase, len(rendered.native), rendered.lines)
		}
		joined := strings.Join(rendered.lines, "\n")
		stageVisible := strings.Contains(joined, "Capturing state") || phase == "compaction_completed" && strings.Contains(joined, "All extraction stages")
		if !strings.Contains(joined, "Crude aeron/model") || !stageVisible ||
			(!strings.Contains(joined, "original context retained") && phase != "compaction_completed") {
			t.Fatalf("%s table=%q", phase, joined)
		}
	}
}

func TestActivityDotSemanticColorsStayStableAcrossBlinkPhases(t *testing.T) {
	for _, frame := range []int{0, ui.ActivityBlinkPhaseMillis} {
		ctx := renderContext{contentWidth: 80, spinFrame: frame}
		preparing := renderLifecycleMessage(types.ChatMessage{LifecyclePhase: "preparing"}, ctx).native[0]
		if preparing.Spans[1].Role != ui.ColorWarning {
			t.Fatalf("preparing dot role at frame %d = %v", frame, preparing.Spans[1].Role)
		}
		waiting := renderLifecycleMessage(types.ChatMessage{LifecyclePhase: "waiting_provider"}, ctx).native[0]
		if waiting.Spans[1].Role != ui.ColorAccent {
			t.Fatalf("waiting dot role at frame %d = %v", frame, waiting.Spans[1].Role)
		}
		tool := buildToolTitleLine(types.ChatMessage{
			ToolName: "edit_file", ToolStatus: types.ToolStatusStreaming,
			ToolPreviewPhase: types.ToolPreviewPreparing,
		}, types.ToolPreviewPreparing, frame)
		if tool.Spans[1].Role != ui.ColorWarning {
			t.Fatalf("tool preparing dot role at frame %d = %v", frame, tool.Spans[1].Role)
		}
		doing := buildToolTitleLine(types.ChatMessage{
			ToolName: "edit_file", ToolStatus: types.ToolStatusStreaming,
			ToolPreviewPhase: types.ToolPreviewStreaming,
		}, types.ToolPreviewStreaming, frame)
		if doing.Spans[1].Role != ui.ColorAccent2 {
			t.Fatalf("tool doing dot role at frame %d = %v", frame, doing.Spans[1].Role)
		}
		thinking := renderThinkingMessage(types.ChatMessage{
			Role: types.RoleThinking, ThinkingActive: true, Content: "working",
		}, renderContext{contentWidth: 80, spinFrame: frame}, false).native[0]
		if thinking.Spans[1].Role != ui.ColorAccent {
			t.Fatalf("thinking dot role at frame %d = %v", frame, thinking.Spans[1].Role)
		}
	}
}

func TestInvalidateWidthProfileDropsCompletedMessageCache(t *testing.T) {
	renderer := NewRenderer(theme.DefaultDark())
	renderer.SetSize(80, 20)
	renderer.SyncMessages([]types.ChatMessage{{
		InternalID: "static", Role: types.RoleAssistant, Content: "ambiguous ○ text",
	}}, 1)
	renderer.sync.Messages[0].CachedLines = []string{"cached under old width profile"}
	renderer.InvalidateWidthProfile()
	if renderer.sync.Messages[0].CachedLines != nil {
		t.Fatal("completed message width cache survived calibration")
	}
}

func TestLongToolTitleFitsWidthWithVisibleElision(t *testing.T) {
	line := buildToolTitleLine(types.ChatMessage{
		ToolName: "write_report", ToolStatus: types.ToolStatusSuccess,
		ToolPreviewPhase: types.ToolPreviewDone,
		ToolArgs:         "/home/lenovo/.crude/workspaces/hash/sessions/id/reports/smoke-blocking-aeron-report.md",
		ToolLivedAdded:   21, ToolDuration: 50 * time.Millisecond,
	}, types.ToolPreviewDone, 0, 52)
	plain := line.PlainText()
	if line.VisibleWidth() > 52 || !strings.Contains(plain, "…") || !strings.Contains(plain, "<0.1s") {
		t.Fatalf("width-planned tool title = %q (%d cells)", plain, rendering.VisibleWidth(plain))
	}
}

func TestSubAgentToolLabelsDistinguishWaitingAndFailure(t *testing.T) {
	waiting := buildToolTitleLine(types.ChatMessage{
		ToolName: "run_sub_agent", ToolStatus: types.ToolStatusStreaming,
		ToolPreviewPhase: types.ToolPreviewStreaming,
	}, types.ToolPreviewStreaming, 0).PlainText()
	if !strings.Contains(waiting, "Waiting for sub-agent") || strings.Contains(waiting, "Spawning") {
		t.Fatalf("synchronous label = %q", waiting)
	}
	failed := buildToolTitleLine(types.ChatMessage{
		ToolName: "run_sub_agent", ToolStatus: types.ToolStatusError,
		ToolPreviewPhase: types.ToolPreviewError,
	}, types.ToolPreviewError, 0).PlainText()
	if !strings.Contains(failed, "Failed to spawn sub-agent") || strings.Contains(failed, "Spawned") {
		t.Fatalf("failed label = %q", failed)
	}
}
