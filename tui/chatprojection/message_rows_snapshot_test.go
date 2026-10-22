package chatprojection

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
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
	oldTailHeight := len(initial.Rows[len(initial.Rows)-1].Lines)
	r.SetSize(32, 20)
	top := r.ExportMessageRowsSnapshot(28)
	scroll.SetContent(top.ContentHeight)
	scroll.ScrollTo(scroll.MaxOffset())
	bottom := r.ExportMessageRowsSnapshot(28)
	if got := len(bottom.Rows[len(bottom.Rows)-1].Lines); got <= oldTailHeight {
		t.Fatalf("tail row remained at stale width: old=%d new=%d", oldTailHeight, got)
	}
}

func TestRenderDeclaredFrameDependenciesAnimateWithoutStaticInvalidation(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetChatAreaBounds(0, 30)
	r.SetSize(80, 30)
	messages := []types.ChatMessage{{InternalID: "static", Role: types.RoleAssistant, Content: "static"}, {InternalID: "status", Role: types.RoleStatus, LifecyclePhase: "preparing"}, {InternalID: "thinking", Role: types.RoleThinking, Content: "thinking", ThinkingActive: true}, {InternalID: "tool", Role: types.RoleTool, ToolName: "read_file", ToolStatus: "streaming", ToolPreviewPhase: types.ToolPreviewStreaming}, {InternalID: "btw", Role: types.RoleUser, Content: "later", PendingSteering: true}}
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
	if changed[0] || !changed[1] || !changed[2] || !changed[3] || len(second.Rows) != 4 {
		t.Fatalf("animated indices=%v", second.ChangedIndices)
	}
	for _, index := range []int{1, 2, 3} {
		if !strings.Contains(strings.Join(second.Rows[index].Lines, "\n"), "○") {
			t.Fatalf("row %d did not advance hollow phase: %v", index, second.Rows[index].Lines)
		}
	}
}

func TestTransientLifecycleMetricsDoNotInvalidateTranscript(t *testing.T) {
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
	if second.Generation != first.Generation || len(second.Rows) != 0 {
		t.Fatalf("metric delta=%+v", second)
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

// A long transcript must not render all of its history in one export: the
// visible rows render first and the rest settle within a per-export budget
// across later frames, with rows always matching the height index.
func TestLargeTranscriptMaterializesProgressivelyWithinBudget(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetChatAreaBounds(0, 20)
	r.SetSize(100, 20)
	r.MaterializeBudget = time.Millisecond
	messages := make([]types.ChatMessage, 400)
	for i := range messages {
		messages[i] = types.ChatMessage{InternalID: fmt.Sprintf("m-%d", i), Role: types.RoleAssistant,
			Content: fmt.Sprintf("## Heading %d\n\n%s\n\n- item\n- item", i, strings.Repeat("markdown *body* text ", 30)), Time: time.Now()}
	}
	r.SyncMessages(messages, 1)
	scroll := ui.NewScrollModel()
	scroll.SetAutoScroll(true)
	r.BindScroll(scroll)
	first := r.ExportMessageRowsSnapshot(96)
	rendered := 0
	for i := 0; i < r.vl.ItemCount(); i++ {
		if item := r.vl.Item(i); item != nil && !item.Dirty {
			rendered++
		}
	}
	if rendered > 60 {
		t.Fatalf("first export rendered %d items; history must not render synchronously", rendered)
	}
	if !r.Materializing() {
		t.Fatal("400 markdown messages cannot all render within a 1ms budget")
	}
	tail := first.Rows[len(first.Rows)-1]
	if strings.TrimSpace(strings.Join(tail.Lines, "")) == "" {
		t.Fatal("the visible tail must be rendered in the first export")
	}
	checkRows := func(s MessageRowsSnapshot) {
		total := 0
		for _, row := range s.Rows {
			total += len(row.Lines)
		}
		if total != s.ContentHeight {
			t.Fatalf("rows=%d lines, index=%d", total, s.ContentHeight)
		}
	}
	checkRows(first)
	for i := 0; r.Materializing(); i++ {
		if i > 100000 {
			t.Fatal("materialization never completed")
		}
		checkRows(r.ExportMessageRowsSnapshot(96))
	}
	settled := r.ExportMessageRowsSnapshot(96)
	if settled.ContentHeight <= first.ContentHeight {
		t.Fatalf("settled height %d should exceed estimated %d", settled.ContentHeight, first.ContentHeight)
	}
}
