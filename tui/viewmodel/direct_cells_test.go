package viewmodel

import (
	"strings"
	"testing"

	"crude/tui/compositor"
	"crude/tui/perf"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func TestAppViewUsesDirectCells(t *testing.T) {
	app := newFullTestApp(t)
	for _, size := range [][2]int{{80, 24}, {45, 12}, {120, 40}} {
		app.width, app.height = size[0], size[1]
		app.compositor.Resize(size[0], size[1])
		app.forceFrame()
		before := perf.Snapshot()
		view := app.View()
		if view.Content != "" || view.Surface == nil {
			t.Fatal("App constructed an ANSI frame instead of direct cells")
		}
		if delta := perf.Snapshot().Sub(before); delta.ANSIFrames != 0 || delta.ANSICells != 0 {
			t.Fatalf("App generated full-frame ANSI: %+v", delta)
		}
		if !view.AltScreen || !view.ReportFocus || view.MouseMode == 0 || view.IconName != view.WindowTitle || view.WindowTitle == "" {
			t.Fatal("direct path lost terminal interaction/identity metadata")
		}
		direct := uv.NewScreenBuffer(size[0], size[1])
		view.Surface.Draw(direct, direct.Bounds())
		// Independently composite root and retained overlays into a reference
		// full frame, then parse its legacy ANSI representation for comparison.
		referenceComp := compositor.NewCompositor(size[0], size[1])
		referenceComp.BeginFrame()
		referenceComp.Root().Buffer().Fill(app.compositor.Root().Buffer().Get(0, 0))
		referenceComp.Root().AddChild(app.mainSurf)
		app.windowManager.Render(referenceComp.Root(), app.renderCtx)
		referenceComp.Composite()
		reference := uv.NewScreenBuffer(size[0], size[1])
		reference.Method = ansi.GraphemeWidth
		uv.NewStyledString(referenceComp.Present()).Draw(reference, reference.Bounds())
		for y := 0; y < size[1]; y++ {
			for x := 0; x < size[0]; x++ {
				if !direct.CellAt(x, y).Equal(reference.CellAt(x, y)) {
					t.Fatalf("%v cell (%d,%d) direct %#v reference %#v", size, x, y, direct.CellAt(x, y), reference.CellAt(x, y))
				}
			}
		}
		if strings.TrimSpace(ansi.Strip(view.RenderContent())) == "" {
			t.Fatal("diagnostic rendering lost the shell")
		}
	}
}

func TestAppDeferredFrameReusesDirectSurface(t *testing.T) {
	app := newFullTestApp(t)
	first := app.View()
	firstText := first.RenderContent()
	presented := app.frameGate.presented
	app.frameGate.deferred, app.frameGate.due = true, false
	app.Input.SetInput("not presented until deadline")
	deferred := app.View()
	if deferred.Surface == nil || deferred.Content != "" || deferred.RenderContent() != firstText || app.frameGate.presented != presented {
		t.Fatal("deferred presentation did not reuse last complete cell frame")
	}
	app.forceFrame()
	app.View()
	if app.frameGate.presented != presented+1 {
		t.Fatal("frame deadline did not present")
	}
	app.width, app.height = 0, 0
	empty := app.View()
	if empty.Content != "" || empty.Surface != nil {
		t.Fatal("zero-size terminal retained a stale frame")
	}
}
