package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestDecorateCodeBlocksCapturesExactSource(t *testing.T) {
	content := "Intro\n\n```go\nfunc main() {\n\tx := 1\n}\n```\n\nMiddle\n\n~~~python\nprint('hi')\n~~~\n\nDone"
	annotated, chips := decorateCodeBlocks(content)
	if len(chips) != 2 {
		t.Fatalf("expected two code chips, got %d", len(chips))
	}
	if chips[0].language != "go" || chips[0].code != "func main() {\n\tx := 1\n}" {
		t.Fatalf("first block changed: %+v", chips[0])
	}
	if chips[1].language != "python" || chips[1].code != "print('hi')" {
		t.Fatalf("second block changed: %+v", chips[1])
	}
	if !strings.Contains(annotated, "```go") || !strings.Contains(annotated, "~~~python") {
		t.Fatalf("fences changed: %q", annotated)
	}
}

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
	if payload.Code != raw {
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
