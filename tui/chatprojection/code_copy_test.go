package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestAssistantCodeBlockPaintsCopyChipWithRawCode(t *testing.T) {
	th := theme.DefaultDark()
	md := rendering.MakeMdRenderer(80, th)
	ctx := renderContext{th: th, tokens: ui.NewTokenSet(th), contentWidth: 76, innerW: 80, width: 80, mdRenderer: md}
	raw := "func main() {\n\tfmt.Println(\"hi\")\n}"
	msg := types.ChatMessage{InternalID: "a1", Role: types.RoleAssistant, Content: "Here is the code:\n\n```go\n" + raw + "\n```\n\nDone."}
	ml := renderAssistantMessage(msg, ctx, true)
	var payload ChatCopyCodePayload
	found := false
	for _, row := range ml.native {
		for _, span := range row.Spans {
			if span.Action != nil && span.Action.ID == ChatCopyCodeActionID {
				payload, found = span.Action.Payload.(ChatCopyCodePayload), true
			}
		}
	}
	if !found {
		t.Fatal("assistant code block has no Copy code chip")
	}
	if payload.Code != raw+"\n" {
		t.Fatalf("chip code = %q, want exact source %q", payload.Code, raw)
	}
	if payload.MessageID != "a1" || payload.Language != "go" {
		t.Fatalf("chip identity = %+v", payload)
	}
}

func TestCodeChipKeepsFollowingProse(t *testing.T) {
	th := theme.DefaultDark()
	md := rendering.MakeMdRenderer(80, th)
	ctx := renderContext{th: th, tokens: ui.NewTokenSet(th), contentWidth: 76, innerW: 80, width: 80, mdRenderer: md}
	msg := types.ChatMessage{InternalID: "a1", Role: types.RoleAssistant, Content: "```go\nx := 1\n```\nAfter the block."}
	ml := renderAssistantMessage(msg, ctx, true)
	paint := strings.Join(ml.lines, "\n")
	if !strings.Contains(paint, "After the block.") {
		t.Fatalf("prose after a fence was lost:\n%s", paint)
	}
	if !strings.Contains(paint, "Copy code") {
		t.Fatalf("code chip was lost:\n%s", paint)
	}
}

func TestNestedCodeCopyUsesMarkdownCodeNotContainerIndent(t *testing.T) {
	for _, input := range []string{
		"  ```python\n  if ready:\n      run()\n  ```",
		"- Example:\n\n  ```python\n  if ready:\n      run()\n  ```",
		"> ```python\n> if ready:\n>     run()\n> ```",
		"    if ready:\n        run()\n",
	} {
		th := theme.DefaultDark()
		ctx := renderContext{th: th, tokens: ui.NewTokenSet(th), contentWidth: 76, innerW: 80, width: 80, mdRenderer: rendering.MakeMdRenderer(80, th)}
		ml := renderAssistantMessage(types.ChatMessage{InternalID: "nested", Role: types.RoleAssistant, Content: input}, ctx, true)
		found := 0
		for _, row := range ml.native {
			for _, span := range row.Spans {
				if span.Action != nil && span.Action.ID == ChatCopyCodeActionID {
					got := span.Action.Payload.(ChatCopyCodePayload).Code
					if got != "if ready:\n    run()\n" {
						t.Errorf("copy from %q = %q", input, got)
					}
					found++
				}
			}
		}
		if found != 1 {
			t.Errorf("copy chips from %q = %d", input, found)
		}
	}
}

func TestStreamingCopySourceSurvivesRewriteCompletionAndReflow(t *testing.T) {
	th := theme.DefaultDark()
	r := NewRenderer(th)
	r.SetSize(80, 40)
	r.SetChatAreaBounds(0, 40)
	source := "```text\nfirst\n```\n"
	message := types.ChatMessage{InternalID: "stream", Role: types.RoleAssistant, Content: source}
	r.AppendMessage(message)
	r.SetStreaming(true)
	check := func(want string) {
		t.Helper()
		rows, _, _ := r.ExportMessageRows(r.width)
		copies := 0
		for _, row := range rows {
			for _, line := range row.Native {
				for _, span := range line.Spans {
					if span.Action != nil && span.Action.ID == ChatCopyCodeActionID {
						copies++
						if got := span.Action.Payload.(ChatCopyCodePayload).Code; got != want {
							t.Fatalf("stream copy=%q want=%q", got, want)
						}
					}
				}
			}
		}
		if copies != 1 {
			t.Fatalf("stream copy count=%d", copies)
		}
	}
	check("first\n")
	// Same-length replacement must not qualify a completed-prefix cache.
	message.Content = strings.Replace(source, "first", "other", 1)
	r.SyncMessages([]types.ChatMessage{message}, 2, 0)
	check("other\n")
	r.SetSize(40, 40)
	check("other\n")
	r.SetStreaming(false)
	check("other\n")
}

func TestStreamingTailSelectionKeepsSourceTab(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(80, 40)
	r.SetChatAreaBounds(0, 40)
	r.AppendMessage(types.ChatMessage{InternalID: "tail", Role: types.RoleAssistant, Content: "```text\n\talpha"})
	r.SetStreaming(true)
	rows, _, _ := r.ExportMessageRows(80)
	global := 0
	for _, row := range rows {
		for _, line := range row.Lines {
			if strings.Contains(line, "alpha") {
				if got := CopySelection(rows, global, 0, global, 1<<20); got != "\talpha" {
					t.Fatalf("live source copy=%q", got)
				}
				return
			}
			global++
		}
	}
	t.Fatal("streaming tail not painted")
}

func TestCodeInfoCannotPutSourceControlsInTheCopyChip(t *testing.T) {
	th := theme.DefaultDark()
	ctx := renderContext{th: th, tokens: ui.NewTokenSet(th), contentWidth: 76, innerW: 80, width: 80, mdRenderer: rendering.MakeMdRenderer(80, th)}
	message := types.ChatMessage{InternalID: "control-info", Role: types.RoleAssistant, Content: "```text\x1b_fake\x1b\\\nx\n```"}
	rendered := renderAssistantMessage(message, ctx, true)
	found := false
	for _, line := range rendered.native {
		for _, span := range line.Spans {
			if strings.ContainsRune(span.Text, '\x1b') {
				t.Fatalf("source control in chip text %q", span.Text)
			}
			if span.Action != nil && span.Action.ID == ChatCopyCodeActionID {
				found = true
				if payload := span.Action.Payload.(ChatCopyCodePayload); payload.Code != "x\n" || strings.ContainsRune(payload.Language, '\x1b') {
					t.Fatalf("copy action=%+v", payload)
				}
			}
		}
	}
	if !found {
		t.Fatal("real parsed block lost its copy chip")
	}
}
