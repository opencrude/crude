package uv

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// recordingScreen is a minimal Screen that records every SetCell call,
// including the x position the caller requested and the cell's width. The
// cursor advance of a Draw call is the sum of the widths of cells placed,
// which is exactly what we need to compare against xansi.StringWidth.
type recordingScreen struct {
	bounds   Rectangle
	method   WidthMethod
	placed   []placedCell
	maxX     int
}

type placedCell struct {
	x     int
	y     int
	width int
}

func (s *recordingScreen) Bounds() Rectangle            { return s.bounds }
func (s *recordingScreen) WidthMethod() WidthMethod      { return s.method }
func (s *recordingScreen) CellAt(x, y int) *Cell         { return &EmptyCell }
func (s *recordingScreen) SetCell(x, y int, c *Cell) {
	// StyledString.Draw first clears the whole area with nil cells; we
	// ignore those — only real content placements count toward cursor
	// advance. A nil cell is a clear; an EmptyCell with empty content is
	// also a blank/space, not a real glyph. We record any cell that has
	// non-empty Content (the actual text the Draw call wrote).
	if c == nil {
		return
	}
	if c.Content == "" {
		return
	}
	w := c.Width
	if w <= 0 {
		w = 1
	}
	s.placed = append(s.placed, placedCell{x: x, y: y, width: w})
	if x+w > s.maxX {
		s.maxX = x + w
	}
}

// TestCellbufMatchesXansiWidths is the cross-layer agreement test that
// guards the ghost-artifact class of bugs. For every glyph class that
// matters, uv.NewStyledString(s).Draw into a recording Screen must
// advance the cursor by exactly as many columns as x/ansi reports via
// StringWidth / FirstGraphemeCluster / ActiveMethod().StringWidth —
// which is also what the layout layer (rendering.VisibleWidth) computes.
//
// If any row disagrees, the compositor emits one column count while UV
// advances another, and a vertical "ghost" of stale cells appears at
// the right edge of every line containing the offending glyph. This is
// the exact artifact that forced vendoring ultraviolet.
//
// Both width profiles are exercised; the profile is set explicitly so
// the test is hermetic regardless of WT_SESSION.
func TestCellbufMatchesXansiWidths(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"ascii", "hello"},
		{"spaces", "    "},
		{"CJK", "中文"},
		{"trigram burger", "☰"},
		{"trigram interface", "☰ Interface"},
		{"high voltage", "⚡"},
		{"recycle", "♻"},
		{"star", "⭐"},
		{"white circle", "⚪"},
		{"ballot box", "☐"},
		{"check green", "✅"},
		{"sparkles", "✨"},
		{"cross mark", "❌"},
		{"wrench", "🔧"},
		{"grinning", "😀"},
		{"star + VS16", "⭐️"},
		{"high voltage + VS16", "⚡️"},
		{"high voltage + VS15", "⚡︎"},
		{"recycle + VS16", "♻️"},
		{"zwj farmer", "🧑\u200d🌾"},
		{"zwj technologist", "👨\u200d💻"},
		{"zwj family 4", "👨\u200d👩\u200d👧\u200d👦"},
		{"rainbow flag", "🏳\ufe0f\u200d🌈"},
		{"flag JP", "🇯🇵"},
		{"flag US", "🇺🇸"},
		{"a + combining", "a\u0300"},
		{"mixed line", "✅ DONE (partial) ☰ ⚡ 🧑\u200d🌾"},
		{"box drawing line", "┌──┤ ☰ ├────┤⚡├──┐"},
		{"long ascii", strings.Repeat("x", 40)},
	}

	for _, profile := range []struct {
		name string
		p    ansi.WidthProfile
	}{
		{"Modern", ansi.WidthProfileModern},
		{"WindowsTerminal", ansi.WidthProfileWindowsTerminal},
	} {
		t.Run(profile.name, func(t *testing.T) {
			prev := ansi.SetWidthProfile(profile.p)
			defer ansi.SetWidthProfile(prev)

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					want := ansi.StringWidth(tc.input)
					if w2 := ansi.ActiveMethod().StringWidth(tc.input); w2 != want {
						t.Fatalf("%q: xansi self-inconsistency: StringWidth=%d, ActiveMethod().StringWidth=%d",
							tc.input, want, w2)
					}

					// Width large enough that no wrap occurs.
					width := want + 20
					if width < 40 {
						width = 40
					}
					scr := &recordingScreen{
						bounds: Rect(0, 0, width, 1),
						method: ansi.ActiveMethod(),
					}
					NewStyledString(tc.input).Draw(scr, Rect(0, 0, width, 1))

					// maxX is the cursor advance — the rightmost column
					// UV reached. This is exactly what the terminal will
					// advance and what layout must match.
					got := scr.maxX
					if got != want {
						// Detail: also count cells placed, to catch the
						// case where UV placed too few cells (underflow) vs
						// placed cells at wrong x positions (overflow).
						placed := 0
						for _, p := range scr.placed {
							placed += p.width
						}
						t.Errorf("%q: UV advanced to x=%d (cells placed sum=%d), xansi says %d — ghost artifact risk",
							tc.input, got, placed, want)
					}

					// Continuity check: cells must be placed at contiguous
					// x positions starting at 0. A gap means UV skipped a
					// column (leaving a stale cell from a prior frame) —
					// the literal cause of the right-edge │ ghost column.
					x := 0
					for _, p := range scr.placed {
						if p.x != x {
							t.Errorf("%q: UV placed a cell at x=%d but expected x=%d (gap/overlap) — ghost column",
								tc.input, p.x, x)
							break
						}
						x += p.width
					}
				})
			}
		})
	}
}