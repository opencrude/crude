package ui

import (
	"strings"
	"testing"

	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
)

func TestWindowFrameTitleTruncatesByGraphemeColumnsGPT56(t *testing.T) {
	for _, tc := range []struct {
		name      string
		title     string
		first     string
		wantTitle string
	}{
		{name: "CJK", title: "界界界界", first: "界", wantTitle: "界界…"},
		{name: "emoji ZWJ", title: "🧑‍💻🧑‍💻🧑‍💻", first: "🧑‍💻", wantTitle: "🧑‍💻🧑‍💻…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			th := theme.DefaultDark()
			frame := NewWindowFrame("unicode-title", FrameWindow, NewTokenSet(th))
			frame.SetTitle(tc.title)
			bounds := Rect{X: 4, Y: 2, W: 12, H: 4}
			frame.Layout(LayoutContext{}, bounds)

			buf := surface.NewCellBuffer(24, 12)
			const originX, originY = 3, 1
			frame.Paint(buf, originX, originY, PaintContext{Theme: th})

			// W=12 with a close action leaves exactly five title columns.
			titleX := originX + bounds.X + 3
			titleY := originY + bounds.Y
			got := frameTitleClustersGPT56(buf, titleX, titleY, 5)
			if got != tc.wantTitle {
				t.Fatalf("painted title=%q, want %q", got, tc.wantTitle)
			}
			if rendering.VisibleWidth(got) != 5 {
				t.Fatalf("painted title width=%d, want 5", rendering.VisibleWidth(got))
			}
			if cell := buf.Get(titleX, titleY); cell.Content != tc.first || cell.Width != 2 {
				t.Fatalf("first grapheme cell=%+v, want full %q width 2", cell, tc.first)
			}
			if continuation := buf.Get(titleX+1, titleY); continuation.Width != 0 {
				t.Fatalf("wide grapheme continuation width=%d, want 0", continuation.Width)
			}

			ox := originX + bounds.X
			if gotClose := buf.Get(ox+bounds.W-2, titleY).Char; gotClose != '✕' {
				t.Fatalf("close glyph=%q, want ✕ after truncated title", gotClose)
			}
			if gotCorner := buf.Get(ox, titleY).Char; gotCorner != '╭' {
				t.Fatalf("parent-local top-left=%q, want ╭", gotCorner)
			}
		})
	}
}

func TestWindowFrameTitlePreservesRightBorderWithoutControls(t *testing.T) {
	th := theme.DefaultDark()
	frame := NewWindowFrame("plain-title", FrameWindow, NewTokenSet(th))
	frame.SetClosable(false)
	frame.SetTitle("a title that must truncate")
	bounds := Rect{X: 2, Y: 1, W: 12, H: 4}
	frame.Layout(LayoutContext{}, bounds)

	buf := surface.NewCellBuffer(20, 8)
	frame.Paint(buf, 1, 1, PaintContext{Theme: th})

	ox := 1 + bounds.X
	oy := 1 + bounds.Y
	if got := buf.Get(ox+bounds.W-1, oy).Char; got != '╮' {
		t.Fatalf("long title overwrote top-right border: got %q, want ╮", got)
	}
	if got := frameTitleClustersGPT56(buf, ox+3, oy, 7); got != "a titl…" {
		t.Fatalf("painted title=%q, want %q", got, "a titl…")
	}
}

func frameTitleClustersGPT56(buf *surface.CellBuffer, x, y, width int) string {
	var out strings.Builder
	for col := x; col < x+width; col++ {
		c := buf.Get(col, y)
		if c.Width == 0 {
			continue
		}
		if c.Content != "" {
			out.WriteString(c.Content)
		} else if c.Char != 0 {
			out.WriteRune(c.Char)
		}
	}
	return out.String()
}
