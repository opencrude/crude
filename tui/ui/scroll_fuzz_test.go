package ui

import "testing"

// FuzzScrollModelOffsetClamp verifies ScrollModel never panics and keeps offset
// within [0, MaxOffset()] under arbitrary viewport/content/delta sequences.
func FuzzScrollModelOffsetClamp(f *testing.F) {
	f.Add(0, 0, 0)
	f.Add(10, 5, 3)
	f.Add(100, 20, -50)
	f.Add(5, 100, 200)

	f.Fuzz(func(t *testing.T, viewport, content, delta int) {
		s := NewScrollModel()
		s.SetViewport(viewport)
		s.SetContent(content)

		s.ScrollBy(delta)
		s.Clamp()

		max := s.MaxOffset()
		if max < 0 {
			t.Fatalf("MaxOffset()=%d must be non-negative", max)
		}
		off := s.Offset()
		if off < 0 || off > max {
			t.Fatalf("offset=%d out of range [0,%d] after ScrollBy(%d)", off, max, delta)
		}

		s.ScrollTo(delta)
		s.Clamp()
		off = s.Offset()
		max = s.MaxOffset()
		if off < 0 || off > max {
			t.Fatalf("offset=%d out of range [0,%d] after ScrollTo(%d)", off, max, delta)
		}

		s.EnsureVisible(delta)
		off = s.Offset()
		if off < 0 || off > max {
			t.Fatalf("offset=%d out of range [0,%d] after EnsureVisible(%d)", off, max, delta)
		}

		top, h := s.ThumbBounds(s.Viewport())
		if h < 0 || top < 0 {
			t.Fatalf("ThumbBounds(%d)=(%d,%d) invalid", s.Viewport(), top, h)
		}
		if s.Viewport() > 0 && h > s.Viewport() {
			t.Fatalf("thumb height %d exceeds track %d", h, s.Viewport())
		}
	})
}
