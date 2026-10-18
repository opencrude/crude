package chatprojection

import (
	"fmt"
	"reflect"
	"testing"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestNestedPaneRowsRetainExactAncestry(t *testing.T) {
	for _, width := range []int{1, 17, 53, 120} {
		for _, viewport := range []int{3, 12} {
			t.Run(fmt.Sprintf("width%d/height%d", width, viewport), func(t *testing.T) {
				r := NewRenderer(theme.DefaultDark())
				r.SetSize(width, viewport)
				r.SetChatAreaBounds(0, viewport)
				scroll := ui.NewScrollModel()
				scroll.SetAutoScroll(false)
				r.BindScroll(scroll)
				// Explicit independently specified row geometry. In particular the
				// grandchild's local body index is not its position in the callback.
				want := []PaneLineInfo{
					{}, // transcript-owned leading spacer
					{PaneID: "child", LineIdx: -1},
					{PaneID: "child", LineIdx: 0},
					{PaneID: "grandchild", LineIdx: -1, Ancestors: []PaneTarget{{PaneID: "child", LineIdx: 1}}},
					{PaneID: "leaf", LineIdx: 4, Ancestors: []PaneTarget{{PaneID: "child", LineIdx: 2}, {PaneID: "grandchild", LineIdx: 3}}},
					{PaneID: "child", LineIdx: 3},
				}
				r.SetSubPaneContentFn(func(_ string, _, _, _ int, _ bool) []SubPaneLine {
					lines := make([]SubPaneLine, len(want)-1)
					for i := range lines {
						lines[i] = NativeLine(buildPlainBodyLine(fmt.Sprintf("row-%d", i)))
						lines[i].PaneInfo = want[i+1]
					}
					return lines
				})
				r.AppendMessage(types.ChatMessage{InternalID: "nested", Role: types.RoleTool, SubPaneID: "child"})
				snapshot := r.ExportMessageRowsSnapshot(width)
				if !reflect.DeepEqual(snapshot.PaneLineMap, want) {
					t.Fatalf("global map lost exact recursive rows:\ngot  %+v\nwant %+v", snapshot.PaneLineMap, want)
				}
				if len(snapshot.Rows) != 1 || len(snapshot.Rows[0].Native) != len(want) || snapshot.Rows[0].Native[0].PaneBackground {
					t.Fatalf("spacer/native geometry = %+v", snapshot.Rows)
				}
				for row, line := range snapshot.Rows[0].Native[1:] {
					if !line.PaneBackground || line.PlainText() != fmt.Sprintf("row-%d", row) {
						t.Fatalf("body %d changed visible content/background: %+v", row, line)
					}
				}
				scroll.SetContent(snapshot.ContentHeight)
				scroll.SetViewport(viewport)
				for _, offset := range []int{0, 2, 3} {
					scroll.ScrollTo(offset)
					lines, _ := r.RenderVisible(width)
					got := r.PaneLineMap()
					for row := range lines {
						global := scroll.Offset() + row
						if len(want) < viewport {
							global = row - (viewport - len(want))
						}
						var target PaneLineInfo
						if global >= 0 && global < len(want) {
							target = want[global]
						}
						if !reflect.DeepEqual(got[row], target) {
							t.Fatalf("offset%d viewport row%d = %+v, want %+v", offset, row, got[row], target)
						}
					}
					if next := r.ExportMessageRowsSnapshot(width); !reflect.DeepEqual(next.PaneLineMap, want) {
						t.Fatal("viewport extraction changed global ancestry")
					}
				}
			})
		}
	}
}

func TestNestedPaneSameHeightUpdateClearsReplacedTargets(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(50, 20)
	r.SetChatAreaBounds(0, 20)
	renders := map[string]int{}
	leaf := "old-leaf"
	r.SetSubPaneContentFn(func(paneID string, _, _, _ int, _ bool) []SubPaneLine {
		renders[paneID]++
		line := NativeLine(buildPlainBodyLine(paneID))
		line.PaneInfo = PaneLineInfo{PaneID: leaf, LineIdx: 2, Ancestors: []PaneTarget{{PaneID: paneID, LineIdx: 0}}}
		return []SubPaneLine{line}
	})
	r.SyncMessages([]types.ChatMessage{
		{InternalID: "one", Role: types.RoleTool, SubPaneID: "pane-one"},
		{InternalID: "two", Role: types.RoleTool, SubPaneID: "pane-two"},
	}, 1)
	first := r.ExportMessageRowsSnapshot(50)
	before := renders["pane-two"]
	leaf = "replacement"
	r.InvalidateSubPane("pane-one")
	next := r.ExportMessageRowsSnapshot(50)
	if next.StructureChanged || len(next.ChangedIndices) != 1 || next.ChangedIndices[0] != 0 || renders["pane-two"] != before {
		t.Fatalf("same-height pane update rebuilt unrelated geometry: %+v renders%v", next, renders)
	}
	if next.PaneLineMap[1].PaneID != "replacement" || next.PaneLineMap[3].PaneID != "old-leaf" {
		t.Fatalf("dirty range metadata = %+v", next.PaneLineMap)
	}
	if first.ContentHeight != next.ContentHeight {
		t.Fatal("fixture did not keep height stable")
	}
	// Replace one pane placeholder with an equally tall ordinary message.
	r.SyncMessages([]types.ChatMessage{
		{InternalID: "one", Role: types.RoleTool, ToolName: "unknown_tool", ToolStatus: types.ToolStatusSuccess},
		{InternalID: "two", Role: types.RoleTool, SubPaneID: "pane-two"},
	}, 2)
	replaced := r.ExportMessageRowsSnapshot(50)
	for i := 0; i < len(replaced.Rows[0].Lines); i++ {
		if replaced.PaneLineMap[i].PaneID != "" || len(replaced.PaneLineMap[i].Ancestors) != 0 {
			t.Fatalf("ordinary replacement retained pane ownership at row%d: %+v", i, replaced.PaneLineMap[i])
		}
	}
}

func TestWithinPaneDoesNotMutateChildAncestry(t *testing.T) {
	child := PaneLineInfo{PaneID: "leaf", LineIdx: 2, Ancestors: []PaneTarget{{PaneID: "child", LineIdx: 3}}}
	left := child.WithinPane(PaneTarget{PaneID: "left", LineIdx: 4})
	right := child.WithinPane(PaneTarget{PaneID: "right", LineIdx: 5})
	left.Ancestors[1].LineIdx = 99
	if child.Ancestors[0].LineIdx != 3 || right.Ancestors[1].LineIdx != 3 || right.Ancestors[0].PaneID != "right" {
		t.Fatal("embedding mutated another owner's retained row ancestry")
	}
}
