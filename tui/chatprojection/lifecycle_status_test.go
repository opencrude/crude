package chatprojection

import (
	"strings"
	"testing"
	"time"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestTransientLifecycleDoesNotChangeTranscriptGeometry(t *testing.T) {
	renderer := NewRenderer(theme.DefaultDark())
	renderer.SetSize(100, 20)
	renderer.SetChatAreaBounds(0, 12)
	renderer.SetSpinFrame(0)
	renderer.SyncMessages([]types.ChatMessage{{
		InternalID: "turn-status", Role: types.RoleStatus,
		LifecyclePhase: "preparing", LifecycleProgress: 42,
		LifecycleUploadedBytes: 43008, LifecycleTotalBytes: 102400, Time: time.Now(),
	}}, 1, 0)

	rows, _, _ := renderer.ExportMessageRows(100)
	if len(rows) != 0 {
		t.Fatalf("rows = %#v", rows)
	}
	renderer.SetSpinFrame(ui.ActivityBlinkPhaseMillis)
	renderer.InvalidateCache()
	rows, _, _ = renderer.ExportMessageRows(100)
	if len(rows) != 0 {
		t.Fatalf("animation reintroduced a transient row: %+v", rows)
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
	for _, phase := range []string{"", "history_restored", "compaction_completed", "compaction_cancelled", "compaction_failed", "compaction_interrupted", "unknown_future_state"} {
		if lifecycleMessageFrameSensitive(types.ChatMessage{Role: types.RoleStatus, LifecyclePhase: phase}) {
			t.Fatalf("terminal or unknown phase %q animated", phase)
		}
	}
	for _, phase := range []string{"preparing", "waiting_provider", "cancelling", "compaction_preparing", "compaction_extracting", "compaction_retrying", "compaction_committing", "compaction_cancelling"} {
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
	}}, 1, 0)

	waiting, _, _ := renderer.ExportMessageRows(100)
	if len(waiting) != 0 {
		t.Fatalf("provider wait row = %#v", waiting)
	}

	renderer.SyncMessages([]types.ChatMessage{{
		InternalID: "stable-row", Role: types.RoleTool,
		ToolID: "tool-1", ToolName: "edit_file", ToolStatus: types.ToolStatusStreaming,
		ToolPreviewPhase: types.ToolPreviewPreparing, ToolStartTime: time.Now(),
	}}, 2, 0)
	toolRows, _, _ := renderer.ExportMessageRows(100)
	if len(toolRows) != 1 || toolRows[0].ID != "stable-row" {
		t.Fatalf("tool transform changed row identity: %#v", toolRows)
	}
	joined := strings.Join(toolRows[0].Lines, "\n")
	if !strings.Contains(joined, "Preparing changes") || strings.Contains(joined, "Waiting for provider") {
		t.Fatalf("transformed tool row = %q", joined)
	}
}

func TestCompactionLifecycleUsesRetainedBoundaryTable(t *testing.T) {
	renderer := NewRenderer(theme.DefaultDark())
	renderer.SetSize(100, 20)
	renderer.SetChatAreaBounds(0, 12)
	msg := types.ChatMessage{
		InternalID: "compact", Role: types.RoleStatus, LifecyclePhase: "compaction_extracting",
		LifecycleOperationID: "compact-1", LifecycleStageName: "Capturing state", LifecycleTrigger: "manual",
		LifecycleStageIndex: 3, LifecycleCompleted: 2, LifecycleStageCount: 4,
		LifecycleBeforeTokens: 420000, Time: time.Now().Add(-2 * time.Second),
	}
	renderer.SyncMessages([]types.ChatMessage{msg}, 1, 0)
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
	direct := renderCompactionMessage(msg, renderContext{innerW: 92, contentWidth: 76, spinFrame: 1})
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
		stageVisible := strings.Contains(joined, "Capturing state") || phase == "compaction_completed" && strings.Contains(joined, "Active context ready")
		if !strings.Contains(joined, "Crude aeron/model") || !stageVisible ||
			(!strings.Contains(joined, "original context retained") && phase != "compaction_completed") {
			t.Fatalf("%s table=%q", phase, joined)
		}
	}
}

// An automatic compaction is maintenance the agent does on its own, so its
// boundary paints as one ordinary tool row: no model header, stage progress
// bar, source trailer, context-boundary rules or extra blank rows.
func TestAutomaticCompactionPaintsAsOneToolRow(t *testing.T) {
	cases := []struct {
		phase, trigger, stage, reason string
		before, after                 int
		want                          string
		animate                       bool
	}{
		{phase: "compaction_preparing", want: "│ ● Compacting context", animate: true},
		{phase: "compaction_extracting", stage: "Distilling work", before: 420000, want: "│ ● Compacting context 420K — Distilling work", animate: true},
		{phase: "compaction_retrying", stage: "Distilling work", want: "│ ● Compacting context — retry 2/3", animate: true},
		{phase: "compaction_committing", stage: "Preparing continuation", want: "│ ● Compacting context — saving checkpoint", animate: true},
		{phase: "compaction_cancelling", want: "│ ● Stopping compaction", animate: true},
		{phase: "compaction_completed", before: 155955, after: 10476, want: "│ ● Compacted context 156.0K → ~10.5K"},
		{phase: "compaction_completed", trigger: "background", before: 155955, after: 10476, want: "│ ● Compacted context 156.0K → ~10.5K — prepared in background"},
		{phase: "compaction_completed", trigger: "decay", before: 155955, after: 90000, want: "│ ● Compacted context 156.0K → ~90K — older tool output trimmed"},
		{phase: "compaction_completed", want: "│ ● Compacted context"},
		{phase: "compaction_cancelled", want: "│ ○ Compaction cancelled — original context kept"},
		{phase: "compaction_failed", reason: "download stalled", want: "│ × Compaction failed — download stalled"},
		{phase: "compaction_failed", want: "│ × Compaction failed — original context kept"},
		{phase: "compaction_interrupted", want: "│ ○ Compaction interrupted — original context kept"},
	}
	for _, test := range cases {
		trigger := test.trigger
		if trigger == "" {
			trigger = "automatic"
		}
		msg := types.ChatMessage{
			InternalID: "auto-compact", Role: types.RoleStatus, LifecyclePhase: test.phase,
			LifecycleOperationID: "compact-auto", LifecycleTrigger: trigger,
			LifecycleStageName: test.stage, LifecycleRetryAttempt: 2, LifecycleRetryLimit: 3,
			LifecycleStageIndex: 2, LifecycleCompleted: 1, LifecycleStageCount: 4,
			LifecycleBeforeTokens: test.before, LifecycleAfterTokens: test.after,
			Content: test.reason, ModelID: "aeron/model",
		}
		rendered := renderCompactionMessage(msg, renderContext{innerW: 100, contentWidth: 84})
		if len(rendered.lines) != 1 || rendered.lines[0] != test.want {
			t.Fatalf("%s/%s lines = %q, want [%q]", test.phase, trigger, rendered.lines, test.want)
		}
		if rendered.frameSensitive != test.animate || lifecycleMessageFrameSensitive(msg) != test.animate {
			t.Fatalf("%s frameSensitive=%v, want %v", test.phase, rendered.frameSensitive, test.animate)
		}
	}
}

// The tool row is painted with the tool-title ink, and through the renderer it
// keeps only the transcript's ordinary leading spacer: no accent rules above
// or below it.
func TestAutomaticCompactionRowHasNoContextBoundaryRules(t *testing.T) {
	renderer := NewRenderer(theme.DefaultDark())
	renderer.SetSize(100, 20)
	renderer.SetChatAreaBounds(0, 12)
	renderer.SyncMessages([]types.ChatMessage{{
		InternalID: "auto-compact", Role: types.RoleStatus, LifecyclePhase: "compaction_completed",
		LifecycleOperationID: "compact-auto", LifecycleTrigger: "automatic",
		LifecycleBeforeTokens: 155955, LifecycleAfterTokens: 10476, ModelID: "aeron/model",
	}}, 1, 0)
	rows, _, _ := renderer.ExportMessageRows(100)
	if len(rows) != 1 || rows[0].ContextBoundary || len(rows[0].Lines) != 2 || strings.TrimSpace(rows[0].Lines[0]) != "" {
		t.Fatalf("automatic compaction rows = %+v", rows)
	}
	if got := rows[0].Lines[1]; got != "│ ● Compacted context 156.0K → ~10.5K" {
		t.Fatalf("automatic compaction line = %q", got)
	}
	spans := rows[0].Native[1].Spans
	if len(spans) < 4 || spans[2].Role != ui.ColorToolTitle || spans[3].Role != ui.ColorToolOperand {
		t.Fatalf("automatic compaction is not painted with tool-title ink: %+v", spans)
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
		if tool.Spans[1].Role != ui.ColorToolTitle {
			t.Fatalf("tool preparing dot role at frame %d = %v", frame, tool.Spans[1].Role)
		}
		doing := buildToolTitleLine(types.ChatMessage{
			ToolName: "edit_file", ToolStatus: types.ToolStatusStreaming,
			ToolPreviewPhase: types.ToolPreviewStreaming,
		}, types.ToolPreviewStreaming, frame)
		if doing.Spans[1].Role != ui.ColorToolTitle {
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
	}}, 1, 0)
	renderer.sync.Messages[0].CachedLines = []string{"cached under old width profile"}
	renderer.InvalidateWidthProfile()
	if renderer.sync.Messages[0].CachedLines != nil {
		t.Fatal("completed message width cache survived calibration")
	}
}

func TestLongToolTitleWrapsWithoutLosingOperand(t *testing.T) {
	path := "/home/lenovo/.crude/workspaces/hash/sessions/id/reports/smoke-blocking-aeron-report.md"
	lines := buildToolTitleLines(types.ChatMessage{
		ToolName: "publish_report", ToolStatus: types.ToolStatusSuccess,
		ToolPreviewPhase: types.ToolPreviewDone,
		ToolArgs:         path,
		ToolLivedAdded:   21, ToolDuration: 50 * time.Millisecond,
	}, types.ToolPreviewDone, 0, 52, DisclosureCollapsed)
	var operand, plain strings.Builder
	for _, line := range lines {
		if line.VisibleWidth() > 52 {
			t.Fatalf("overflowing title: %q", line.PlainText())
		}
		plain.WriteString(line.PlainText())
		for _, span := range line.Spans {
			if span.Role == ui.ColorToolOperand {
				operand.WriteString(span.Text)
			}
		}
	}
	if strings.TrimSpace(operand.String()) != path || !strings.Contains(plain.String(), "<0.1s") {
		t.Fatalf("wrapped title lost operand or metadata: %q", plain.String())
	}
}

func TestSubAgentToolLabelsDistinguishRunningAndFailure(t *testing.T) {
	waiting := buildToolTitleLine(types.ChatMessage{
		ToolName: "agent_start", ToolStatus: types.ToolStatusStreaming,
		ToolPreviewPhase: types.ToolPreviewStreaming, ToolProgressLabel: "Lily · Researcher — survey",
	}, types.ToolPreviewStreaming, 0).PlainText()
	if !strings.Contains(waiting, "Starting agent Lily · Researcher — survey") {
		t.Fatalf("running label = %q", waiting)
	}
	failed := buildToolTitleLine(types.ChatMessage{
		ToolName: "agent_start", ToolStatus: types.ToolStatusError,
		ToolPreviewPhase: types.ToolPreviewError,
	}, types.ToolPreviewError, 0).PlainText()
	if !strings.Contains(failed, "Agent start failed") {
		t.Fatalf("failed label = %q", failed)
	}
}

func TestCompactAgentWaitKeepsNameAndIdentityWithoutJSON(t *testing.T) {
	msg := &types.ChatMessage{Role: types.RoleTool, ToolName: "agent_wait", ToolArgs: "agent-8f21",
		ToolInput:  `{"agent_id":"agent-8f21"}`,
		Content:    `{"id":"agent-8f21","name":"Lily sub Dawn","category":"researcher","state":"available","presence":"attached","runtime_epoch":1}`,
		ToolStatus: types.ToolStatusSuccess, ToolPreviewPhase: types.ToolPreviewDone}
	lines, _ := NewRenderer(theme.DefaultDark()).BuildSubPaneToolLines([]*types.ChatMessage{msg}, 42, 0)
	var text strings.Builder
	for _, line := range lines {
		text.WriteString(line.PlainText())
	}
	if !strings.Contains(text.String(), "Lily sub Dawn") || !strings.Contains(text.String(), "Researcher") || !strings.Contains(text.String(), "agent-8f21") || strings.Contains(text.String(), `"agent_id"`) {
		t.Fatalf("compact agent wait label: %q", text.String())
	}
}
