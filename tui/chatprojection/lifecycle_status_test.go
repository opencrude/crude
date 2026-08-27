package chatprojection

import (
	"strings"
	"testing"
	"time"

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

func TestActivityDotSemanticColorsStayStableAcrossBlinkPhases(t *testing.T) {
	for _, frame := range []int{0, ui.ActivityBlinkPhaseMillis} {
		preparing := renderLifecycleMessage(types.ChatMessage{LifecyclePhase: "preparing"}, frame).native[0]
		if preparing.Spans[1].Role != ui.ColorWarning {
			t.Fatalf("preparing dot role at frame %d = %v", frame, preparing.Spans[1].Role)
		}
		waiting := renderLifecycleMessage(types.ChatMessage{LifecyclePhase: "waiting_provider"}, frame).native[0]
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
