package chatprojection

import (
	"strings"
	"testing"
	"time"

	"crude/tui/cell"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestErrorAndYoloNoticesShareReadableBoundedRows(t *testing.T) {
	diagnostic := `File-change tracking could not start: localhistory: scan workspace: sync C:\Users\Lenovo\.crude\workspaces\fb777db91a52df30\local-history\objects\files\df: Access is denied.`
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, width := range []int{1, 12, 40, 80, 160} {
			ctx := renderContext{th: th, tokens: ui.NewTokenSet(th), innerW: width, contentWidth: max(1, width-16)}
			for _, test := range []struct {
				message types.ChatMessage
				role    ui.SemanticColor
			}{
				{types.ChatMessage{Role: types.RoleError, Content: diagnostic}, ui.ColorError},
				{types.ChatMessage{Role: types.RoleStatus, LifecyclePhase: "yolo_enabled", Content: "Commands and file changes can run without confirmation."}, ui.ColorWarning},
				{types.ChatMessage{Role: types.RoleStatus, LifecyclePhase: "yolo_disabled", Content: "Tool approval prompts are restored for this session."}, ui.ColorSuccess},
			} {
				test.message.Time = time.Now()
				rows := renderMessageContent(test.message, ctx, false)
				if len(rows.native) < 2 || rows.frameSensitive || rows.native[0].Spans[0].Role != test.role || rows.native[0].Spans[0].Attr != cell.AttrBold {
					t.Fatalf("notice lost its static severity heading: %+v", rows)
				}
				for index, row := range rows.native {
					if row.VisibleWidth() > width || row.PlainText() != rows.lines[index] {
						t.Fatalf("width=%d, row=%+v", width, row)
					}
					if index > 0 {
						for _, span := range row.Spans {
							if span.Role != ui.ColorFg || span.Attr != 0 {
								t.Fatalf("diagnostic body was tinted/bold: %+v", span)
							}
						}
					}
				}
				compact := func(text string) string { return strings.Join(strings.Fields(text), "") }
				if compact(strings.Join(rows.lines[1:], "")) != compact(test.message.Content) {
					t.Fatalf("notice discarded diagnostic text: %+v", rows.lines)
				}
			}
		}
	}
}

func TestFailedActivityStripDoesNotRepeatTheDiagnostic(t *testing.T) {
	th := theme.DefaultDark()
	var strip ActivityStrip
	strip.Update(types.AgentActivityView{Status: types.AgentStatusSnapshot{Phase: types.AgentFailed, Detail: "sync C:\\history: Access is denied."}}, 80, th, ui.NewTokenSet(th))
	line := strip.Line(1000)
	if line.PlainText() != "Turn failed · details in transcript" || line.TextTide {
		t.Fatalf("failure strip repeats/animates a diagnostic: %+v", line)
	}
}
