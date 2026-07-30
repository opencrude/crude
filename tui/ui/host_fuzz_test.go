package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
)

// FuzzHostHitTest verifies Host.HitTest never panics and returns a registered
// component whose screen bounds contain the point (or RootID outside viewport).
func FuzzHostHitTest(f *testing.F) {
	f.Add(40, 5, 1, 0)
	f.Add(1, 1, 0, 0)
	f.Add(200, 50, 99, 49)

	f.Fuzz(func(t *testing.T, width, height, x, y int) {
		if width < 0 {
			width = -width
		}
		if height < 0 {
			height = -height
		}
		width = width % 201
		height = height % 51

		th := theme.DefaultDark()
		save := NewButton("save", "Save", "save")
		cancel := NewButton("cancel", "Cancel", "cancel")
		root := NewStack("root", Horizontal, 2, save, cancel)
		h := NewHost(th, root)
		h.RegisterTree(root)
		if width > 0 && height > 0 {
			h.Resize(width, height)
		}

		id := h.HitTest(x, y)

		if width <= 0 || height <= 0 {
			if id != RootID && h.root != nil && id != h.root.ID() {
				// degenerate viewport: only root or RootID is valid
				if _, ok := h.registry[id]; !ok {
					t.Fatalf("HitTest(%d,%d)=%q not registered with size %dx%d", x, y, id, width, height)
				}
			}
			return
		}

		h.Layout()
		if _, ok := h.registry[id]; id != RootID && !ok {
			t.Fatalf("HitTest(%d,%d)=%q not in registry", x, y, id)
		}

		rootR := h.root.Bounds()
		if !rootR.Contains(x, y) {
			if id != RootID && id != h.root.ID() {
				t.Fatalf("point (%d,%d) outside root %v but hit %q", x, y, rootR, id)
			}
			return
		}

		if id == RootID {
			return
		}

		r, ok := h.screen[id]
		if !ok {
			t.Fatalf("HitTest(%d,%d)=%q missing from screen map", x, y, id)
		}
		if !r.Contains(x, y) {
			t.Fatalf("HitTest(%d,%d)=%q bounds %v do not contain point", x, y, id, r)
		}

		// Handle must not panic for arbitrary coordinates inside viewport.
		switch {
		case x >= 0 && y >= 0 && x < width && y < height:
			_ = h.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		}
	})
}

// FuzzWindowFrameCloseBounds verifies close hit regions stay within frame bounds.
func FuzzWindowFrameCloseBounds(f *testing.F) {
	f.Add(20, 10, 15, 0)
	f.Add(5, 3, 0, 0)

	f.Fuzz(func(t *testing.T, width, height, lx, ly int) {
		if width < 0 {
			width = -width
		}
		if height < 0 {
			height = -height
		}
		width = width%120 + 3
		height = height%40 + 3

		th := theme.DefaultDark()
		ts := NewTokenSet(th)
		frame := NewWindowFrame("fuzz/frame", FrameWindow, ts)
		frame.SetTitle("Test")
		frame.SetClosable(true)
		frame.Layout(LayoutContext{Theme: th}, Rect{W: width, H: height})

		closeR := frame.CloseBounds()
		if closeR.Empty() {
			return
		}
		b := frame.Bounds()
		if closeR.X < 0 || closeR.Y < 0 || closeR.X+closeR.W > b.W || closeR.Y+closeR.H > b.H {
			t.Fatalf("closeR=%v outside frame bounds %v", closeR, b)
		}

		onClose := frame.IsOnClose(lx, ly)
		if onClose && !closeR.Contains(lx, ly) {
			t.Fatalf("IsOnClose(%d,%d)=true but closeR=%v", lx, ly, closeR)
		}
		if closeR.Contains(lx, ly) && !onClose {
			t.Fatalf("IsOnClose(%d,%d)=false but closeR=%v contains point", lx, ly, closeR)
		}
	})
}
