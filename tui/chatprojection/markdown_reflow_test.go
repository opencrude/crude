package chatprojection

import (
	"fmt"
	"strings"
	"testing"

	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/types/conversation"
)

func TestMarkdownParagraphReflowKeepsPaintAndSource(t *testing.T) {
	const body = "שלום abcdefghijklm ! Example **bold** and `go test` at ./src/main.go:12.\n" +
		"Soft break with 界 and café.\n\nRepeat repeat repeat and [Docs](https://example.org/guide)."
	th := theme.DefaultDark()
	r := NewRenderer(th)
	r.AppendMessage(conversation.ChatMessage{InternalID: "paragraph", Role: conversation.RoleAssistant, Content: body})
	msg := r.sync.MsgByID["paragraph"]
	if msg == nil {
		t.Fatal("assistant message was not retained")
	}
	var retainedText *rendering.CopyText
	for _, width := range []int{20, 27, 34, 53, 24} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			r.SetSize(width+2*th.Spacing.PadLR+scrollbarFootprint(r.ScrollbarWidth()), 40)
			plain, native := r.renderSingleMessage(msg)
			got := native[2:] // transcript spacer and assistant header
			want, ok := renderMarkdownRows(body, rendering.MakeMdRenderer(width, th), r.Tokens(), msg.InternalID, true)
			if !ok || len(got) != len(want) {
				t.Fatalf("first visit to width %d: got %d rows, want %d", width, len(got), len(want))
			}
			gotGrid, wantGrid := surface.NewCellBuffer(width, len(got)), surface.NewCellBuffer(width, len(want))
			foundLink, foundCode, foundRTL := false, false, false
			for y, row := range got {
				if row.PlainText() != want[y].PlainText() {
					t.Fatalf("row %d: got %q, want %q", y, row.PlainText(), want[y].PlainText())
				}
				if line := ArrangedLine(row, width); line.RTL() {
					foundRTL = true
				}
				PaintLineAt(gotGrid, 0, y, width, row, r.Tokens(), 0, 0, false)
				PaintLineAt(wantGrid, 0, y, width, want[y], r.Tokens(), 0, 0, false)
				for x := 0; x < width; x++ {
					a, b := gotGrid.Get(x, y), wantGrid.Get(x, y)
					if a.Char != b.Char || a.Attr != b.Attr || !a.FG.Equal(b.FG) || !a.BG.Equal(b.BG) || !a.Link.Equal(b.Link) {
						t.Fatalf("different cell (%d,%d): got %+v, want %+v", x, y, a, b)
					}
				}
				for _, span := range row.Spans {
					if span.Link.File() {
						if span.Action == nil || span.Action.ID != ChatFileOpenActionID || !span.Copy.Valid() {
							t.Fatalf("file link lost its action or copy range: %+v", span)
						}
						foundLink = true
					}
					if !span.Copy.Valid() || span.Copy.Text.Block != 0 {
						continue
					}
					if retainedText == nil {
						retainedText = span.Copy.Text
					}
					if span.Copy.Text != retainedText {
						t.Fatal("width change rebuilt the semantic paragraph")
					}
					if span.Copy.Text.InlineCode(span.Copy.Start) >= 0 && strings.Contains(span.Copy.String(), "go") {
						foundCode = true
					}
				}
			}
			if !foundLink || !foundCode || !foundRTL {
				t.Fatalf("missing file link, inline code or RTL: %v %v %v", foundLink, foundCode, foundRTL)
			}
			projected := []MessageRow{{Lines: plain[2:], Native: got}}
			if text := CopySelection(projected, 0, 0, len(got)-1, width); !strings.Contains(text, "at ./src/main.go:12.\nSoft break") || !strings.Contains(text, "bold and go test") {
				t.Fatalf("semantic copy changed at width %d: %q", width, text)
			}
		})
	}
}

func TestMarkdownParagraphReflowUpdatesOnSourceChange(t *testing.T) {
	th := theme.DefaultDark()
	r := NewRenderer(th)
	r.SetSize(44, 40)
	msg := conversation.ChatMessage{InternalID: "m", Role: conversation.RoleAssistant, Content: "Original **paragraph**"}
	r.SyncMessages([]conversation.ChatMessage{msg}, 1, 1)
	old, _ := r.renderSingleMessage(r.sync.MsgByID["m"])
	msg.Content = "Updated **paragraph**"
	if !r.SyncChanges([]conversation.ChatMessage{msg}, nil, nil, 2, 1) {
		t.Fatal("same-message source update was rejected")
	}
	newLines, _ := r.renderSingleMessage(r.sync.MsgByID["m"])
	if !strings.Contains(strings.Join(old, "\n"), "Original paragraph") ||
		!strings.Contains(strings.Join(newLines, "\n"), "Updated paragraph") ||
		strings.Contains(strings.Join(newLines, "\n"), "Original") {
		t.Fatalf("source update returned stale rows: %q -> %q", old, newLines)
	}
}

func TestMarkdownParagraphReflowWorkingSet(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(44, 40)
	const body = "Identical **styled** paragraph"
	messages := make([]conversation.ChatMessage, markdownReflowEntries+1)
	for i := range messages {
		messages[i] = conversation.ChatMessage{InternalID: fmt.Sprint(i), Role: conversation.RoleAssistant, Content: body}
	}
	r.SyncMessages(messages, 1, 1)
	render := func(i int) *rendering.CopyText {
		t.Helper()
		_, rows := r.renderSingleMessage(r.sync.MsgByID[messages[i].InternalID])
		for _, row := range rows {
			for _, span := range row.Spans {
				if span.Copy.Valid() {
					return span.Copy.Text
				}
			}
		}
		t.Fatal("paragraph lost its copy source")
		return nil
	}
	first, second := render(0), render(1)
	if first == second {
		t.Fatal("identical messages shared a source identity")
	}
	for i := 2; i < markdownReflowEntries; i++ {
		render(i)
	}
	if render(0) != first {
		t.Fatal("prepared source was not retained while in the working set")
	}
	render(markdownReflowEntries)
	if render(1) == second {
		t.Fatal("least recently used paragraph was not released")
	}
	r.SetTheme(theme.DefaultLight())
	afterTheme := render(0)
	if afterTheme == first {
		t.Fatal("theme change retained the previous styled source")
	}
	r.SyncMessages(messages, 1, 2) // same identities and bytes, new conversation
	if render(0) == afterTheme {
		t.Fatal("source generation change retained the previous document")
	}
	beforeProfile := render(0)
	r.InvalidateWidthProfile()
	if render(0) == beforeProfile {
		t.Fatal("width profile change retained the previous prepared runs")
	}
}
