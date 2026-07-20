package uv

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestPaddedEmojiLineNoOverflow reproduces the app's exact scenario: the
// compositor builds a line, pads it to a fixed innerW with trailing
// spaces, and feeds it to UV. If UV's width calc disagrees with the
// compositor's (rendering.VisibleWidth), UV will either wrap (leaving
// stale cells on the next line) or underfill (leaving stale cells at the
// right edge). The line UV places must be EXACTLY innerW columns wide,
// matching what the compositor measured.
func TestPaddedEmojiLineNoOverflow(t *testing.T) {
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

			// Content the compositor rendered (emoji + text). The
			// compositor measured its width with VisibleWidth and
			// padded with trailing spaces to innerW.
			innerW := 30
			content := "✅ DONE ☰ 🧑\u200d🌾 end"
			contentW := ansi.StringWidth(content)
			if contentW >= innerW {
				t.Fatalf("test content too wide: %d >= %d", contentW, innerW)
			}
			padded := content + strings.Repeat(" ", innerW-contentW)

			// Sanity: the compositor's measured width of the padded line
			// must be exactly innerW.
			if w := ansi.StringWidth(padded); w != innerW {
				t.Fatalf("compositor width %d != innerW %d", w, innerW)
			}

			// Draw into a buffer exactly innerW wide (no room to
			// overflow without wrapping to a second line).
			buf := NewScreenBuffer(innerW, 2)
			buf.Method = ansi.ActiveMethod()
			NewStyledString(padded).Draw(buf, Rect(0, 0, innerW, 2))

			// Row 0 must be fully populated (no wrap). Row 1 must be
			// empty (no overflow). If UV wrapped, content spilled to
			// row 1 — that's the bug.
			row1Content := false
			for x := 0; x < innerW; x++ {
				c := buf.CellAt(x, 1)
				if c == nil {
					continue
				}
				if !c.IsZero() && !c.Equal(&EmptyCell) {
					row1Content = true
					break
				}
			}
			if row1Content {
				t.Errorf("UV wrapped the padded line to row 1 — UV's width exceeds compositor's by at least 1 column. "+
					"content=%q contentW(compositor)=%d innerW=%d",
					content, contentW, innerW)
			}

			// Row 0's rightmost cell must be a space (the padding
			// reached the end). If UV placed fewer columns, the last
			// cell would be a stale/empty fill — but more importantly,
			// the LAST content cell (the 'd' of "end") must be at
			// column contentW-1, not later.
			lastContentCol := -1
			for x := innerW - 1; x >= 0; x-- {
				c := buf.CellAt(x, 0)
				if c == nil || c.IsZero() || c.Equal(&EmptyCell) {
					continue
				}
				lastContentCol = x
				break
			}
			// lastContentCol is 0-indexed; contentW is the width, so
			// the last content cell is at column contentW-1.
			if lastContentCol != contentW-1 {
				t.Errorf("UV placed last content at column %d, compositor says content ends at column %d — UV's width disagrees with compositor (ghost artifact source)",
					lastContentCol, contentW-1)
			}
		})
	}
}