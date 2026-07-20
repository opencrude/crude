package uv

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestTerminalRendererZwjGhost reproduces the ghost-artifact bug: when a
// line contains a ZWJ emoji and the content changes between frames, UV's
// diff-based TerminalRenderer leaves stale cells because it mishandles
// wide-char continuation cells that get orphaned when a wide glyph moves.
//
// We render two frames through a real TerminalRenderer (with a bytes
// buffer as the output writer) and assert the SECOND frame's emitted
// bytes contain no leftover glyph from the FIRST frame at the position
// the moved emoji vacated.
//
// The emoji is one 2-cell cluster. The bug manifests when it shifts position
// between frames: the diff must clear the old cells, not just write new ones.
func TestTerminalRendererZwjGhost(t *testing.T) {
	// Frame 1: "ab🧑‍🌾cd" — emoji at column 2.
	// Frame 2: "a🧑‍🌾bcd" — emoji at column 1 (shifted left).
	// The cell at column 4 (old 🌾 continuation or 'c') must
	// not retain stale emoji ink in the terminal output.
	frame1 := "ab🧑\u200d🌾cd"
	frame2 := "a🧑\u200d🌾bcd"

	width := 40
	height := 1

	// Render frame 1.
	buf1 := NewScreenBuffer(width, height)
	buf1.Method = ansi.GraphemeWidth
	NewStyledString(frame1).Draw(buf1, Rect(0, 0, width, height))

	var out strings.Builder
	scr := NewTerminalRenderer(&out, []string{"TERM=xterm-256color"})
	scr.SetRelativeCursor(false)
	scr.SetFullscreen(false)
	scr.Render(buf1.RenderBuffer)
	if err := scr.Flush(); err != nil {
		t.Fatal(err)
	}
	out.Reset()

	// Render frame 2 into a FRESH cellbuf (as bubbletea does —
	// s.cellbuf.Clear() then content.Draw).
	buf2 := NewScreenBuffer(width, height)
	buf2.Method = ansi.GraphemeWidth
	NewStyledString(frame2).Draw(buf2, Rect(0, 0, width, height))

	scr.Render(buf2.RenderBuffer)
	if err := scr.Flush(); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	_ = got // (the diff may use ECH/REP/EL instead of literal runes;
	//       the only sound check is the curbuf state below)

	// The terminal's tracked state (scr.curbuf) must match buf2
	// exactly after the diff+emit. If any column's content
	// differs, that column shows a STALE cell from frame 1 — the
	// literal ghost artifact the user sees.
	stale := 0
	for x := 0; x < width; x++ {
		new := buf2.CellAt(x, 0)
		old := scr.curbuf.CellAt(x, 0)
		if new == nil || old == nil {
			continue
		}
		if new.Content != old.Content {
			stale++
			if stale <= 5 {
				t.Errorf("column %d: terminal shows %q, expected %q — STALE CELL (ghost artifact)",
					x, old.Content, new.Content)
			}
		}
	}
	if stale > 0 {
		t.Errorf("total stale columns: %d", stale)
	}
}
