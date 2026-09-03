package chatprojection

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

func TestMessageRowsSnapshotGenerationAndStableHeightDelta(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetChatAreaBounds(0, 10)
	r.SetSize(60, 20)
	messages := []types.ChatMessage{{InternalID: "a", Role: types.RoleAssistant, Content: "first", Time: time.Now()}, {InternalID: "b", Role: types.RoleAssistant, Content: "second", Time: time.Now()}}
	r.SyncMessages(messages, 1)
	first := r.ExportMessageRowsSnapshot(56)
	same := r.ExportMessageRowsSnapshot(56)
	if first.Generation == 0 || same.Generation != first.Generation {
		t.Fatalf("stable generations %d %d", first.Generation, same.Generation)
	}
	r.InvalidateItem("b")
	changed := r.ExportMessageRowsSnapshot(56)
	if changed.Generation == first.Generation || changed.StructureChanged || len(changed.ChangedIndices) != 1 || changed.ChangedIndices[0] != 1 {
		t.Fatalf("row delta=%+v", changed)
	}
	r.SetSize(50, 20)
	resized := r.ExportMessageRowsSnapshot(46)
	if !resized.StructureChanged || resized.Generation == changed.Generation {
		t.Fatalf("resize snapshot=%+v", resized)
	}
}

func TestWidthOnlyReflowLazilyUpdatesRowsAsTheyEnterViewport(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetChatAreaBounds(0, 4)
	r.SetSize(80, 20)
	messages := make([]types.ChatMessage, 20)
	for i := range messages {
		messages[i] = types.ChatMessage{InternalID: fmt.Sprintf("m-%d", i), Role: types.RoleAssistant, Content: strings.Repeat("wide content ", 8), Time: time.Now()}
	}
	r.SyncMessages(messages, 1)
	scroll := ui.NewScrollModel()
	r.BindScroll(scroll)
	initial := r.ExportMessageRowsSnapshot(76)
	scroll.SetContent(initial.ContentHeight)
	scroll.SetViewport(4)
	scroll.SetAutoScroll(false)
	scroll.ScrollTo(0)
	r.SetSize(32, 20)
	top := r.ExportMessageRowsSnapshot(28)
	oldTailHeight := len(top.Rows[len(top.Rows)-1].Lines)
	scroll.SetContent(top.ContentHeight)
	scroll.ScrollTo(scroll.MaxOffset())
	bottom := r.ExportMessageRowsSnapshot(28)
	if bottom.Generation == top.Generation {
		t.Fatal("scrolling into dirty rows did not advance projection")
	}
	if got := len(bottom.Rows[len(bottom.Rows)-1].Lines); got <= oldTailHeight {
		t.Fatalf("tail row remained at stale width: old=%d new=%d", oldTailHeight, got)
	}
}
