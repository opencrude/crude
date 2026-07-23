package rendering

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
)

func TestRenderToSurfaceClearsReusedFrame(t *testing.T) {
	surf := surface.NewSurface("ansi-frame", 12, 2, cell.TransparentBlank())
	RenderToSurface(surf, "stale content\nsecond row")
	if surf.Buffer().Get(0, 0).Char != 's' {
		t.Fatal("initial frame was not rendered")
	}

	RenderToSurface(surf, "new")
	if got := surf.Buffer().Get(0, 0).Char; got != 'n' {
		t.Fatalf("new frame starts with %q, want n", got)
	}
	if got := surf.Buffer().Get(4, 0); !got.Transparent {
		t.Fatalf("old trailing cell survived frame replacement: %#v", got)
	}
	if got := surf.Buffer().Get(0, 1); !got.Transparent {
		t.Fatalf("old second row survived frame replacement: %#v", got)
	}
}
