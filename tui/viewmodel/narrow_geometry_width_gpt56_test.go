package viewmodel

import (
	"testing"

	"crude/tui/chatprojection"
	"crude/tui/rendering"
	"crude/tui/screens/shellchat"
)

func TestNarrowContentLayoutNeverFabricatesInnerColumnsGPT56(t *testing.T) {
	app := newTestApp(t)
	app.launcherScreen = nil
	app.Tree.Visible = false
	app.Win.OpenTab("narrow", "", true)

	requestedPad := max(0, app.Theme.Spacing.PadLR)
	activityW := app.activityChromeWidth()
	contentWidths := make([]int, 0, 2*requestedPad+3)
	for contentW := 0; contentW <= 2*requestedPad; contentW++ {
		contentWidths = append(contentWidths, contentW)
	}
	contentWidths = append(contentWidths, 2*requestedPad+1, 2*requestedPad+19)

	for _, wantContent := range contentWidths {
		terminalW := activityW + wantContent
		app.width = terminalW
		layout := app.computeContentLayout()
		wantPad := min(requestedPad, wantContent/2)
		wantInner := wantContent - 2*wantPad
		if layout.ContentW != wantContent || layout.PadLR != wantPad || layout.InnerW != wantInner {
			t.Fatalf(
				"content=%d layout content/pad/inner=(%d,%d,%d), want (%d,%d,%d)",
				wantContent, layout.ContentW, layout.PadLR, layout.InnerW, wantContent, wantPad, wantInner,
			)
		}
		if used := 2*layout.PadLR + layout.InnerW; used > layout.ContentW {
			t.Fatalf("content=%d padding and inner width consume %d columns", layout.ContentW, used)
		}
		if wantContent >= 2*requestedPad && layout.PadLR != requestedPad {
			t.Fatalf("content=%d changed normal theme padding: got %d want %d", wantContent, layout.PadLR, requestedPad)
		}

		app.configureShell()
		for id, slot := range app.shell.slots {
			r := slot.rect
			if r.X < 0 || r.W < 0 || r.X+r.W > terminalW {
				t.Fatalf("content=%d slot %q escapes terminal width %d: %+v", wantContent, id, terminalW, r)
			}
		}

		line := shellchat.ComposeRow(shellchat.ComposeParams{
			InnerW: layout.InnerW,
			PadLR:  layout.PadLR,
			Theme:  app.Theme,
		}, 0, 0, "界🧑‍💻abcdefghijklmnopqrstuvwxyz", chatprojection.PaneLineInfo{})
		if got := rendering.VisibleWidth(line); got > layout.ContentW {
			t.Fatalf("content=%d composed row consumes %d columns", layout.ContentW, got)
		}
	}
}
