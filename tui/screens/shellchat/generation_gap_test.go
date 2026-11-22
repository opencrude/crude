package shellchat_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"crude/tui/chatprojection"
	"crude/tui/screens/shellchat"
	"crude/tui/theme"
	"crude/types/conversation"
)

// A chat screen that did not install every export of its renderer (a hidden
// tab, or a frame that exported twice and installed only the second snapshot)
// must not apply the newest snapshot's changed indices as if they were the
// whole difference. Doing so kept the old height of a reply that grew while
// unseen: the reply was cut after its old last row and the rest of the
// viewport stayed blank until an unrelated structure change rebuilt the list.
func TestScreenRebuildsWhenItMissedAnExport(t *testing.T) {
	th := theme.DefaultDark()
	const width, height = 60, 40
	renderer := chatprojection.NewRenderer(th)
	renderer.SetSize(width, height)
	renderer.SetChatAreaBounds(0, height)
	user := conversation.ChatMessage{InternalID: "user", Role: conversation.RoleUser, Content: "first", Time: time.Unix(1000, 0)}
	reply := conversation.ChatMessage{InternalID: "reply", Role: conversation.RoleAssistant, Content: "Short start.", ModelID: "m", Time: time.Unix(1001, 0)}
	renderer.SyncMessages([]conversation.ChatMessage{user, reply}, 1, 0)

	screen := shellchat.New(th)
	install := func(snapshot chatprojection.MessageRowsSnapshot) {
		screen.SetMessages(snapshot.Rows, shellchat.PaintParams{
			SourceID: snapshot.SourceID, Generation: snapshot.Generation, BaseGeneration: snapshot.BaseGeneration,
			ChangedIndices: snapshot.ChangedIndices, StructureChanged: snapshot.StructureChanged,
			Compose: shellchat.ComposeParams{InnerW: width - 4, PadLR: 2, Theme: th}, PaneLineMap: snapshot.PaneLineMap,
		})
		screen.SyncScrollMetrics(height, snapshot.ContentHeight)
	}
	install(renderer.ExportMessageRowsSnapshot(width))

	// The reply keeps streaming while this screen is not installed.
	var grown strings.Builder
	grown.WriteString("Short start.\n\n")
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&grown, "- **Point %d:** later text the user never saw.\n", i)
	}
	reply.Content = grown.String()
	renderer.SyncChanges([]conversation.ChatMessage{reply}, nil, []string{"user", "reply"}, 2, 0)
	missed := renderer.ExportMessageRowsSnapshot(width)
	if !missed.StructureChanged {
		t.Fatal("the grown reply must change the transcript structure")
	}
	// A later same-height change produces a delta against the missed export.
	user.Content = "fixed"
	renderer.SyncChanges([]conversation.ChatMessage{user}, nil, []string{"user", "reply"}, 3, 0)
	latest := renderer.ExportMessageRowsSnapshot(width)
	if latest.StructureChanged {
		t.Fatal("test setup: the latest export should be a same-height delta")
	}
	if latest.BaseGeneration != missed.Generation {
		t.Fatalf("delta base=%d, want the previous export %d", latest.BaseGeneration, missed.Generation)
	}
	install(latest)

	buf := screen.PaintTestBuffer(width, latest.ContentHeight)
	var painted strings.Builder
	for y := 0; y < latest.ContentHeight; y++ {
		painted.WriteString(shellchat.PlainRow(buf, y, width))
		painted.WriteByte('\n')
	}
	for i := 1; i <= 8; i++ {
		if want := fmt.Sprintf("Point %d:", i); !strings.Contains(painted.String(), want) {
			t.Fatalf("reply row %q is not painted after a missed export:\n%s", want, painted.String())
		}
	}
}
