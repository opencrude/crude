package chatprojection

import (
	"fmt"
	"strings"
	"testing"

	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/attachment"
)

func TestAttachmentSourceSpansSurviveMarkdownAndWrapping(t *testing.T) {
	const label = "[Pasted Text1]"
	for _, text := range []string{
		"Literal " + label + " then **attached** " + label + " end",
		"Literal " + label + " then `" + label + "` end",
		"Literal " + label + "\n\n```text\n" + label + "\n```",
		"Literal " + label + " then " + "\ue000\ue001 " + label + " 界 tail",
	} {
		for _, width := range []int{8, 16, 40, 80} {
			t.Run(fmt.Sprintf("%d/%s", width, text), func(t *testing.T) {
				start := len([]rune(text[:strings.LastIndex(text, label)]))
				target := attachment.Target{Start: start, End: start + len([]rune(label)), Label: label, ReferenceID: "exact-id"}
				th := theme.DefaultDark()
				msg := types.ChatMessage{InternalID: "source", Role: types.RoleUser, Content: text, Attachments: []attachment.Target{target}}
				rendered := renderUserMessage(msg, renderContext{contentWidth: width, mdRenderer: rendering.MakeMdRenderer(width, th), tokens: ui.NewTokenSet(th)}, true)
				var actionText strings.Builder
				for _, row := range rendered.native {
					if row.VisibleWidth() > width {
						t.Fatalf("unbounded row width %d > %d: %q", row.VisibleWidth(), width, row.PlainText())
					}
					for _, span := range row.Spans {
						if span.Action != nil {
							payload, ok := span.Action.Payload.(ChatAttachmentOpenPayload)
							if !ok || payload.Target != target {
								t.Fatalf("wrong identity %+v", span.Action)
							}
							actionText.WriteString(span.Text)
						}
					}
				}
				if got := actionText.String(); got != label && strings.ReplaceAll(got, " ", "") != strings.ReplaceAll(label, " ", "") {
					t.Fatalf("lost or falsely linked source span: %q", got)
				}
				plain := strings.Join(rendered.lines, "\n")
				if strings.ContainsRune(plain, '\ue002') {
					t.Fatal("internal marker escaped into rendered/copy text")
				}
			})
		}
	}
}

func TestLiteralAttachmentLabelsHaveNoInlineActions(t *testing.T) {
	msg := types.ChatMessage{Role: types.RoleUser, Content: "[Pasted Text1] [Pasted IMG1] literal prose"}
	for _, row := range renderUserMessage(msg, renderContext{contentWidth: 80}, true).native {
		for _, span := range row.Spans {
			if span.Action != nil {
				t.Fatal("literal label became attachment")
			}
		}
	}
}
