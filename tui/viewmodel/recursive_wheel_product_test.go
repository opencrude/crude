package viewmodel

import (
	"strings"
	"testing"

	"crude/tui/screens/shellchat"
	"crude/tui/surface"
	"crude/tui/ui"

	tea "charm.land/bubbletea/v2"
)

func TestRecursiveWheelChangesRenderedLeafAcrossHydrationAndWindow(t *testing.T) {
	for _, hydrated := range []bool{false, true} {
		f := newRecursiveScrollFixture(t, 53, hydrated)
		screen := shellchat.NewWithID(f.registry.theme, "recursive/chat")
		frame := ui.NewWindowFrameWidget(f.registry.theme, "recursive/frame", ui.FrameWindow, "Recursive chat", screen, nil, false, 60, 20)
		host := frame.Host()
		host.Resize(60, 20)
		project := func() {
			snapshot := f.main.ExportMessageRowsSnapshot(53)
			screen.SetMessages(snapshot.Rows, shellchat.PaintParams{SourceID: snapshot.SourceID, Generation: snapshot.Generation, StructureChanged: snapshot.StructureChanged, ChangedIndices: snapshot.ChangedIndices, PaneLineMap: snapshot.PaneLineMap, Compose: shellchat.ComposeParams{InnerW: 53, PadLR: 1, NestingLevel: 1, PaneLineMap: snapshot.PaneLineMap, PaneGutters: f.registry.BuildPaneGutters(snapshot.PaneLineMap, f.panes)}, OnPaneScrollChain: func(ids []string) {
				f.registry.InvalidatePaneChain(ids)
				if len(ids) > 0 {
					f.main.InvalidateSubPane(ids[0])
				}
			}})
			host.MarkLayout()
			host.Layout()
		}
		project()
		body, _ := host.ScreenRect(screen.ID())
		paint := func() string { b := surface.NewCellBuffer(60, 20); host.Paint(b); return bufferPlain(b) }
		before := paint()
		leaf := f.registry.ScrollModel("leaf")
		child := f.registry.ScrollModel("child")
		childBefore, leafBefore := child.Offset(), leaf.Offset()
		row := -1
		for i, info := range screen.ViewportPaneLineMap() {
			if info.PaneID == "leaf" && info.LineIdx >= 0 {
				row = i
				break
			}
		}
		if row < 0 || !strings.Contains(before, "LEAF-00") {
			t.Fatalf("real recursive leaf not painted: %s", before)
		}
		// Scroll inside leaf body, then install the actual invalidated renderer
		// snapshot again; this catches stale containing-renderer caches.
		host.Handle(tea.MouseWheelMsg{X: body.X + 4, Y: body.Y + row, Button: tea.MouseWheelDown})
		project()
		after := paint()
		if leaf.Offset() != leafBefore+1 || child.Offset() != childBefore {
			t.Fatal("leaf wheel moved wrong authoritative model")
		}
		if before == after || !strings.Contains(after, "LEAF-01") {
			t.Fatalf("leaf model moved but rendered contents did not: %s", after)
		}
		// A title row belongs to the real enclosing child, never the leaf body.
		screen.ResetWheelGesture()
		host.ResetWheelGesture()
		project()
		for i, info := range screen.ViewportPaneLineMap() {
			if info.PaneID == "leaf" && info.LineIdx < 0 {
				row = i
				break
			}
		}
		leafBefore = leaf.Offset()
		host.Handle(tea.MouseWheelMsg{X: body.X + 4, Y: body.Y + row, Button: tea.MouseWheelDown})
		if leaf.Offset() != leafBefore || child.Offset() <= childBefore {
			t.Fatal("leaf header scrolled leaf rather than enclosing child")
		}
	}
}
