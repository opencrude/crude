package chatprojection

import (
	"strings"
	"testing"
	"time"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/conversation"
)

func TestTransientLifecycleDoesNotChangeTranscriptGeometry(t *testing.T) {
	renderer := NewRenderer(theme.DefaultDark())
	renderer.SetSize(100, 20)
	renderer.SetChatAreaBounds(0, 12)
	renderer.SetSpinFrame(0)
	renderer.SyncMessages([]conversation.ChatMessage{{
		InternalID: "turn-status", Role: conversation.RoleStatus,
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
	msg := conversation.ChatMessage{InternalID: "history-status", Role: conversation.RoleStatus, Content: "Restored 1 file from Local History"}
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
	msg := conversation.ChatMessage{InternalID: "history", Role: conversation.RoleStatus, LifecyclePhase: "history_restored", LifecycleOperationID: "plan-1234567890abcdef",
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
		if lifecycleMessageFrameSensitive(conversation.ChatMessage{Role: conversation.RoleStatus, LifecyclePhase: phase}) {
			t.Fatalf("terminal or unknown phase %q animated", phase)
		}
	}
	for _, phase := range []string{"preparing", "waiting_provider", "cancelling", "compaction_preparing", "compaction_extracting", "compaction_retrying", "compaction_committing", "compaction_cancelling"} {
		if !lifecycleMessageFrameSensitive(conversation.ChatMessage{Role: conversation.RoleStatus, LifecyclePhase: phase}) {
			t.Fatalf("active phase %q did not animate", phase)
		}
	}
}

func TestLifecycleStatusProjectionTransformsInPlaceToTool(t *testing.T) {
	renderer := NewRenderer(theme.DefaultDark())
	renderer.SetSize(100, 20)
	renderer.SetChatAreaBounds(0, 12)
	renderer.SyncMessages([]conversation.ChatMessage{{
		InternalID: "stable-row", Role: conversation.RoleStatus,
		LifecyclePhase: "waiting_provider", LifecycleProvider: "OpenAI", Time: time.Now().Add(-2 * time.Second),
	}}, 1, 0)

	waiting, _, _ := renderer.ExportMessageRows(100)
	if len(waiting) != 0 {
		t.Fatalf("provider wait row = %#v", waiting)
	}

	renderer.SyncMessages([]conversation.ChatMessage{{
		InternalID: "stable-row", Role: conversation.RoleTool,
		ToolID: "tool-1", ToolName: "edit_file", ToolStatus: conversation.ToolStatusStreaming,
		ToolPreviewPhase: conversation.ToolPreviewPreparing, ToolStartTime: time.Now(),
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

func TestLiveManualAndRequestedCompactionUseOrdinaryToolGeometry(t *testing.T) {
	for _, trigger := range []string{"manual", "requested"} {
		t.Run(trigger, func(t *testing.T) {
			r := NewRenderer(theme.DefaultDark())
			r.SetSize(80, 24)
			r.SetChatAreaBounds(0, 12)
			msg := conversation.ChatMessage{
				InternalID: "compact", Role: conversation.RoleStatus, LifecyclePhase: "compaction_extracting",
				LifecycleOperationID: "one-checkpoint", LifecycleStageName: "Capturing state", LifecycleTrigger: trigger,
				LifecycleStageIndex: 3, LifecycleCompleted: 2, LifecycleStageCount: 4,
				LifecycleBeforeTokens: 420000, ModelID: "errand/model",
			}
			r.SyncMessages([]conversation.ChatMessage{msg}, 1, 0)
			rows, _, _ := r.ExportMessageRows(80)
			want := "│ ● Compacting context (Manual) 420K · Capturing state"
			if len(rows) != 1 || rows[0].ID != "compact" || rows[0].ContextBoundary || len(rows[0].Lines) != 2 || rows[0].Lines[0] != "" || rows[0].Lines[1] != want {
				t.Fatalf("want one ordinary tool row, got %v", rows)
			}
			msg.LifecyclePhase = "compaction_completed"
			msg.LifecycleAfterTokens = 12000
			r.SyncMessages([]conversation.ChatMessage{msg}, 2, 0)
			rows, _, _ = r.ExportMessageRows(80)
			if len(rows) != 1 || rows[0].ID != "compact" || rows[0].ContextBoundary || len(rows[0].Lines) != 2 || !rows[0].Toggle || !strings.Contains(rows[0].Lines[1], "Compacted context (Manual)") {
				t.Fatalf("completion changed identity, lost disclosure or restored a card: %v", rows)
			}
		})
	}
}

func TestCompactionTerminalFormsStayCompactAndDiscloseEvidence(t *testing.T) {
	for _, phase := range []string{"compaction_completed", "compaction_cancelled", "compaction_failed", "compaction_interrupted"} {
		msg := conversation.ChatMessage{
			InternalID: "compact", Role: conversation.RoleStatus, LifecyclePhase: phase,
			LifecycleOperationID: "compact-1", LifecycleStageName: "Capturing state",
			LifecycleStageIndex: 3, LifecycleCompleted: 2, LifecycleStageCount: 4,
			LifecycleBeforeTokens: 155955, LifecycleAfterTokens: 10476,
			LifecycleTrigger: "manual", Content: "network connection stalled",
		}
		if phase == "compaction_completed" {
			msg.LifecycleCompleted = 4
			msg.LifecycleStageIndex = 4
		}
		msg.ModelID = "errand/model"
		rendered := renderCompactionMessage(msg, renderContext{contentWidth: 76, innerW: 92})
		if len(rendered.native) != 1 || !strings.Contains(rendered.lines[0], "(Manual)") || !strings.Contains(rendered.lines[0], "▸") {
			t.Fatalf("%s is not compact with disclosure: %q", phase, rendered.lines)
		}
		expanded := renderCompactionMessage(msg, renderContext{contentWidth: 76, innerW: 92, disclosure: DisclosureExpanded})
		joined := strings.Join(expanded.lines, "\n")
		for _, want := range []string{"network connection stalled", "155955", "10476"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("%s lost %q: %s", phase, want, joined)
			}
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
		{phase: "compaction_preparing", want: "│ ● Compacting context (Auto)", animate: true},
		{phase: "compaction_extracting", stage: "Distilling work", before: 420000, want: "│ ● Compacting context (Auto) 420K · Distilling work", animate: true},
		{phase: "compaction_retrying", stage: "Distilling work", want: "│ ● Compacting context (Auto) · retry 2/3", animate: true},
		{phase: "compaction_committing", stage: "Preparing continuation", want: "│ ● Compacting context (Auto) · saving checkpoint", animate: true},
		{phase: "compaction_cancelling", want: "│ ● Stopping compaction (Auto)", animate: true},
		{phase: "compaction_completed", before: 155955, after: 10476, want: "│ ● Compacted context (Auto) 156.0K → ~10.5K"},
		{phase: "compaction_completed", trigger: "background", before: 155955, after: 10476, want: "│ ● Compacted context (Auto) 156.0K → ~10.5K · prepared in background"},
		{phase: "compaction_completed", trigger: "decay", before: 155955, after: 90000, want: "│ ● Compacted context (Auto) 156.0K → ~90K · older tool output trimmed"},
		{phase: "compaction_completed", want: "│ ● Compacted context (Auto)"},
		{phase: "compaction_cancelled", want: "│ ○ Compaction cancelled (Auto) · original context kept"},
		{phase: "compaction_failed", reason: "download stalled", want: "│ × Compaction failed (Auto) · download stalled"},
		{phase: "compaction_failed", want: "│ × Compaction failed (Auto) · original context kept"},
		{phase: "compaction_interrupted", want: "│ ○ Compaction interrupted (Auto) · original context kept"},
	}
	for _, test := range cases {
		trigger := test.trigger
		if trigger == "" {
			trigger = "automatic"
		}
		msg := conversation.ChatMessage{
			InternalID: "auto-compact", Role: conversation.RoleStatus, LifecyclePhase: test.phase,
			LifecycleOperationID: "compact-auto", LifecycleTrigger: trigger,
			LifecycleStageName: test.stage, LifecycleRetryAttempt: 2, LifecycleRetryLimit: 3,
			LifecycleStageIndex: 2, LifecycleCompleted: 1, LifecycleStageCount: 4,
			LifecycleBeforeTokens: test.before, LifecycleAfterTokens: test.after,
			Content: test.reason, ModelID: "errand/model",
		}
		rendered := renderCompactionMessage(msg, renderContext{innerW: 100, contentWidth: 84})
		want := test.want
		if settledCompaction(msg) && (msg.Content != "" || msg.LifecycleBeforeTokens > 0) {
			want += " ▸"
		}
		if len(rendered.lines) != 1 || rendered.lines[0] != want {
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
	renderer.SyncMessages([]conversation.ChatMessage{{
		InternalID: "auto-compact", Role: conversation.RoleStatus, LifecyclePhase: "compaction_completed",
		LifecycleOperationID: "compact-auto", LifecycleTrigger: "automatic",
		LifecycleBeforeTokens: 155955, LifecycleAfterTokens: 10476, ModelID: "errand/model",
	}}, 1, 0)
	rows, _, _ := renderer.ExportMessageRows(100)
	if len(rows) != 1 || rows[0].ContextBoundary || len(rows[0].Lines) != 2 || strings.TrimSpace(rows[0].Lines[0]) != "" {
		t.Fatalf("automatic compaction rows = %+v", rows)
	}
	if got := rows[0].Lines[1]; got != "│ ● Compacted context (Auto) 156.0K → ~10.5K ▸" {
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
		preparing := renderLifecycleMessage(conversation.ChatMessage{LifecyclePhase: "preparing"}, ctx).native[0]
		if preparing.Spans[1].Role != ui.ColorWarning {
			t.Fatalf("preparing dot role at frame %d = %v", frame, preparing.Spans[1].Role)
		}
		waiting := renderLifecycleMessage(conversation.ChatMessage{LifecyclePhase: "waiting_provider"}, ctx).native[0]
		if waiting.Spans[1].Role != ui.ColorAccent {
			t.Fatalf("waiting dot role at frame %d = %v", frame, waiting.Spans[1].Role)
		}
		tool := buildToolTitleLine(conversation.ChatMessage{
			ToolName: "edit_file", ToolStatus: conversation.ToolStatusStreaming,
			ToolPreviewPhase: conversation.ToolPreviewPreparing,
		}, conversation.ToolPreviewPreparing, frame)
		if tool.Spans[1].Role != ui.ColorToolTitle {
			t.Fatalf("tool preparing dot role at frame %d = %v", frame, tool.Spans[1].Role)
		}
		doing := buildToolTitleLine(conversation.ChatMessage{
			ToolName: "edit_file", ToolStatus: conversation.ToolStatusStreaming,
			ToolPreviewPhase: conversation.ToolPreviewStreaming,
		}, conversation.ToolPreviewStreaming, frame)
		if doing.Spans[1].Role != ui.ColorToolTitle {
			t.Fatalf("tool doing dot role at frame %d = %v", frame, doing.Spans[1].Role)
		}
		thinking := renderThinkingMessage(conversation.ChatMessage{
			Role: conversation.RoleThinking, ThinkingActive: true, Content: "working",
		}, renderContext{contentWidth: 80, spinFrame: frame}, false).native[0]
		if thinking.Spans[1].Role != ui.ColorAccent {
			t.Fatalf("thinking dot role at frame %d = %v", frame, thinking.Spans[1].Role)
		}
	}
}

func TestInvalidateWidthProfileDropsCompletedMessageCache(t *testing.T) {
	renderer := NewRenderer(theme.DefaultDark())
	renderer.SetSize(80, 20)
	renderer.SyncMessages([]conversation.ChatMessage{{
		InternalID: "static", Role: conversation.RoleAssistant, Content: "ambiguous ○ text",
	}}, 1, 0)
	renderer.sync.Messages[0].CachedLines = []string{"cached under old width profile"}
	renderer.InvalidateWidthProfile()
	if renderer.sync.Messages[0].CachedLines != nil {
		t.Fatal("completed message width cache survived calibration")
	}
}

func TestLongToolTitleWrapsWithoutLosingOperand(t *testing.T) {
	path := "/home/example/.crude/workspaces/hash/sessions/id/reports/smoke-blocking-errand-report.md"
	lines := buildToolTitleLines(conversation.ChatMessage{
		ToolName: "publish_report", ToolStatus: conversation.ToolStatusSuccess,
		ToolPreviewPhase: conversation.ToolPreviewDone,
		ToolArgs:         path,
		ToolLivedAdded:   21, ToolDuration: 50 * time.Millisecond,
	}, conversation.ToolPreviewDone, 0, 52, DisclosureExpanded)
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
	waiting := buildToolTitleLine(conversation.ChatMessage{
		ToolName: "agent_start", ToolStatus: conversation.ToolStatusStreaming,
		ToolPreviewPhase: conversation.ToolPreviewStreaming, ToolProgressLabel: "Lily · Researcher — survey",
	}, conversation.ToolPreviewStreaming, 0).PlainText()
	if !strings.Contains(waiting, "Starting agent Lily · Researcher — survey") {
		t.Fatalf("running label = %q", waiting)
	}
	failed := buildToolTitleLine(conversation.ChatMessage{
		ToolName: "agent_start", ToolStatus: conversation.ToolStatusError,
		ToolPreviewPhase: conversation.ToolPreviewError,
	}, conversation.ToolPreviewError, 0).PlainText()
	if !strings.Contains(failed, "Agent start failed") {
		t.Fatalf("failed label = %q", failed)
	}
}

func TestCompactAgentWaitKeepsNameAndIdentityWithoutJSON(t *testing.T) {
	msg := &conversation.ChatMessage{InternalID: "wait", Role: conversation.RoleTool, ToolName: "agent_wait", ToolArgs: "agent-8f21",
		ToolInput:  `{"agent_id":"agent-8f21"}`,
		Content:    `{"id":"agent-8f21","name":"Lily sub Dawn","category":"researcher","state":"available","presence":"attached","runtime_epoch":1}`,
		ToolStatus: conversation.ToolStatusSuccess, ToolPreviewPhase: conversation.ToolPreviewDone}
	r := NewRenderer(theme.DefaultDark())
	r.AppendMessage(*msg)
	lines, _ := r.BuildSubPaneToolLines([]*conversation.ChatMessage{msg}, 42, 0)
	var text strings.Builder
	for _, line := range lines {
		text.WriteString(line.PlainText())
	}
	if !strings.Contains(text.String(), "Lily sub Dawn") || strings.Contains(text.String(), `"agent_id"`) {
		t.Fatalf("compact agent wait label: %q", text.String())
	}
	expanded := renderToolMessageLines(*msg, theme.DefaultDark(), 42, 42, 42, 0, 24, conversation.ToolPreviewDone, DisclosureExpanded)
	text.Reset()
	for _, line := range expanded.native {
		text.WriteString(line.PlainText())
	}
	if !strings.Contains(text.String(), "agent-8f21") || !strings.Contains(text.String(), "Researcher") {
		t.Fatal("expanded agent wait lost its exact identity")
	}
}

func TestCompactWaitTitleMatchesItsTarget(t *testing.T) {
	for _, tc := range []struct {
		name, input, status, phase, content, want string
	}{
		{"next update", `{"agent_id":"","run_id":"","artifact_id":"","timeout_seconds":600}`,
			conversation.ToolStatusStreaming, conversation.ToolPreviewStreaming, "", "Waiting for an update"},
		{"named agent", `{"agent_id":"agent-8f21","timeout_seconds":600}`,
			conversation.ToolStatusStreaming, conversation.ToolPreviewStreaming, "", "Waiting for agent event agent-8f21"},
		{"command update", `{"timeout_seconds":600}`, conversation.ToolStatusSuccess, conversation.ToolPreviewDone,
			`{"status":"directed_event","event":{"id":"event-1","origin":"runtime","kind":"command_outcome"}}`, "Wait finished"},
		{"no update", `{"timeout_seconds":600}`, conversation.ToolStatusSuccess, conversation.ToolPreviewDone,
			`{"status":"timeout_no_match"}`, "Wait finished"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := &conversation.ChatMessage{Role: conversation.RoleTool, ToolName: "agent_wait", ToolInput: tc.input,
				ToolStatus: tc.status, ToolPreviewPhase: tc.phase, Content: tc.content}
			lines, _ := NewRenderer(theme.DefaultDark()).BuildSubPaneToolLines([]*conversation.ChatMessage{msg}, 80, 0)
			var text strings.Builder
			for _, line := range lines {
				text.WriteString(line.PlainText())
			}
			if !strings.Contains(text.String(), tc.want) {
				t.Fatalf("wait row does not describe its actual target: got %q, want %q", text.String(), tc.want)
			}
		})
	}
}

// The writer label is enough on the live row: the user asked for what
// happens now instead of the model ID, which stays in the expanded result.
func TestCompactionRowShowsWriterAndMeasuredPhaseWithoutTimer(t *testing.T) {
	for writer, label := range map[string]string{"native": "Native", "errand": "Errand"} {
		for stage, want := range map[string]string{"Read 50%": "· Read 50%", "": "· Preparing"} {
			msg := conversation.ChatMessage{LifecyclePhase: "compaction_extracting", LifecycleTrigger: "automatic", LifecycleProvider: writer, ModelID: "ollama/deepseek-v4.1-flash:1017", LifecycleStageName: stage, LifecycleBeforeTokens: 411900, Time: time.Now().Add(-30 * time.Minute)}
			row := strings.Join(renderCompactionMessage(msg, renderContext{innerW: 90, contentWidth: 90}).lines, " ")
			if !strings.Contains(row, "(Auto - "+label+") 411.9K "+want) || strings.Contains(row, "deepseek") || strings.Contains(row, "30m") {
				t.Errorf("wrong active row: %q", row)
			}
		}
	}
}
