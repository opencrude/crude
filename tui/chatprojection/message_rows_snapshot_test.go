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

func TestRenderDeclaredFrameDependenciesAnimateWithoutStaticInvalidation(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetChatAreaBounds(0, 30)
	r.SetSize(80, 30)
	messages := []types.ChatMessage{{InternalID: "static", Role: types.RoleAssistant, Content: "static"}, {InternalID: "status", Role: types.RoleStatus, LifecyclePhase: "preparing"}, {InternalID: "thinking", Role: types.RoleThinking, Content: "thinking", ThinkingActive: true}, {InternalID: "tool", Role: types.RoleTool, ToolName: "read_file", ToolStatus: "streaming", ToolPreviewPhase: types.ToolPreviewStreaming}, {InternalID: "btw", Role: types.RoleUser, Content: "later", PendingBtw: true}}
	r.SetSpinFrame(0)
	r.SyncMessages(messages, 1)
	first := r.ExportMessageRowsSnapshot(76)
	r.SetSpinFrame(ui.ActivityBlinkPhaseMillis)
	second := r.ExportMessageRowsSnapshot(76)
	if second.Generation == first.Generation || second.StructureChanged {
		t.Fatalf("animation snapshot=%+v", second)
	}
	changed := map[int]bool{}
	for _, index := range second.ChangedIndices {
		changed[index] = true
	}
	if changed[0] || !changed[1] || !changed[2] || !changed[3] || !changed[4] {
		t.Fatalf("animated indices=%v", second.ChangedIndices)
	}
	for _, index := range []int{1, 2, 3, 4} {
		if !strings.Contains(strings.Join(second.Rows[index].Lines, "\n"), "○") {
			t.Fatalf("row %d did not advance hollow phase: %v", index, second.Rows[index].Lines)
		}
	}
}

func TestLifecycleMetricSourceVersionUpdatesExactRow(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetChatAreaBounds(0, 10)
	r.SetSize(80, 20)
	base := types.ChatMessage{InternalID: "status", Role: types.RoleStatus, LifecyclePhase: "preparing", LifecycleProgress: 10, LifecycleUploadedBytes: 10, LifecycleTotalBytes: 100}
	r.SyncMessages([]types.ChatMessage{base}, 1)
	first := r.ExportMessageRowsSnapshot(76)
	base.LifecycleProgress = 75
	base.LifecycleUploadedBytes = 75
	r.SyncMessages([]types.ChatMessage{base}, 2)
	second := r.ExportMessageRowsSnapshot(76)
	if second.Generation == first.Generation || len(second.ChangedIndices) != 1 || second.ChangedIndices[0] != 0 {
		t.Fatalf("metric delta=%+v", second)
	}
	plain := strings.Join(second.Rows[0].Lines, " ")
	if !strings.Contains(plain, "75%") || !strings.Contains(plain, "75 B / 100 B") {
		t.Fatalf("metric row=%q", plain)
	}
}

func TestStreamingCursorDependencyChangesWithTypingState(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetChatAreaBounds(0, 8)
	r.SetSize(60, 20)
	r.SetStreaming(true)
	r.SetActiveTyping(true)
	r.SyncMessages([]types.ChatMessage{{InternalID: "assistant", Role: types.RoleAssistant, Content: "working"}}, 1)
	r.SetSpinFrame(ui.ActivityBlinkPhaseMillis)
	solid := r.ExportMessageRowsSnapshot(56)
	if !strings.Contains(strings.Join(solid.Rows[0].Lines, ""), "█") {
		t.Fatal("active typing cursor is not solid")
	}
	r.SetActiveTyping(false)
	hollow := r.ExportMessageRowsSnapshot(56)
	if strings.Contains(strings.Join(hollow.Rows[0].Lines, ""), "█") {
		t.Fatal("idle cursor ignored hollow frame")
	}
	r.SetSpinFrame(0)
	filled := r.ExportMessageRowsSnapshot(56)
	if !strings.Contains(strings.Join(filled.Rows[0].Lines, ""), "█") || filled.Generation == hollow.Generation {
		t.Fatal("frame dependency did not restore filled cursor")
	}
}

func TestRenderVisibleIndexesRowsExposedByHeightCorrection(t *testing.T) {
	list := newItemList()
	list.SetViewport(4)
	items := make([]renderItem, 12)
	for index := range items {
		items[index] = renderItem{Height: 20, Dirty: true}
	}
	list.SetItems(items)
	rendered := 0
	list.RenderVisible(func(_ int, _ *renderItem, _ int) []string {
		rendered++
		return []string{"row"}
	}, 80)
	if rendered <= 5 || rendered >= len(items) {
		t.Fatalf("expected newly exposed rows but not a whole-list reflow; rendered %d", rendered)
	}
	height := 0
	for _, item := range list.Items() {
		height += item.Height
	}
	if indexed := list.TotalHeight(); indexed != height {
		t.Fatalf("second-pass heights were not indexed: total=%d actual=%d", indexed, height)
	}
}
