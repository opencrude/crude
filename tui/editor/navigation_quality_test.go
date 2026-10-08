package editor

import (
	tea "charm.land/bubbletea/v2"
	"crypto/sha256"
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestJumpBeforeFirstRenderKeepsShortFileAtTop(t *testing.T) {
	ed := NewPreview(theme.DefaultDark(), "short.txt", "first\nsecond\nthird\nfourth", 1)
	ed.SetCursorPosition(2, 0)
	ed.ScrollToCursor()
	surf := surface.NewSurface("jump", 60, 12, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{})
	if ed.scrollY != 0 || !strings.Contains(plainRow(surf.Buffer(), 0, 60), "first") {
		t.Fatalf("short file top=%d first row=%q", ed.scrollY, plainRow(surf.Buffer(), 0, 60))
	}
}

func TestJumpLongFileRevealsContextAndResize(t *testing.T) {
	ed := NewPreview(theme.DefaultDark(), "long.txt", strings.Repeat("line\n", 100), 1)
	ed.SetCursorPosition(70, 0)
	ed.ScrollToCursor()
	ed.Render(surface.NewSurface("jump", 60, 15, cell.TransparentBlank()), ui.PaintContext{})
	if ed.scrollY >= 70 || ed.scrollY+ed.bodyHeight() <= 70 {
		t.Fatalf("target lacks context: %d", ed.scrollY)
	}
	ed.Render(surface.NewSurface("jump", 40, 4, cell.TransparentBlank()), ui.PaintContext{})
	if ed.cursor.Line < ed.scrollY || ed.cursor.Line >= ed.scrollY+ed.bodyHeight() {
		t.Fatal("resize hid target")
	}
}

func TestLandingIsPaintedButTypingDoesNotReplaceTarget(t *testing.T) {
	ed := New(theme.DefaultDark())
	ed.OpenBuffer(NewBufferManager().CreateScratch())
	ed.ReplaceDocument("alpha target omega")
	ed.SetCursorPosition(0, 6)
	ed.SetLandingRange(0, 6, 12)
	surf := surface.NewSurface("landing", 60, 8, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{})
	if ed.HasSelection() {
		t.Fatal("jump left destructive selection")
	}
	cue := surf.Buffer().Get(ed.gutterWidth()+6, 0)
	if cue.BG != ed.searchHighlightBg(true) || cell.ContrastRatio(cue.FG, cue.BG) < 4.5 || cue.Attr.Has(cell.AttrReverse) {
		t.Fatal("landing is not visibly highlighted with readable text")
	}
	ed.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if ed.Value() != "alpha Xtarget omega" {
		t.Fatalf("typing destroyed target: %q", ed.Value())
	}
}

func TestSavedBytesOwnedAndLateSave(t *testing.T) {
	b := NewBufferManager().CreateScratch()
	b.ReplaceContents("accepted")
	snapshot := b.Snapshot()
	b.AcceptSave(snapshot.Revision, sha256.Sum256(snapshot.Data), snapshot.Data)
	b.ReplaceContents("later")
	baseline := b.SavedBytes()
	baseline[0] = 'X'
	if string(b.SavedBytes()) != "accepted" || string(b.Bytes()) != "later" {
		t.Fatal("snapshot aliases live baseline")
	}
	b.AcceptSave(snapshot.Revision, sha256.Sum256([]byte("new baseline")), []byte("new baseline"))
	if string(b.SavedBytes()) != "new baseline" || !b.IsDirty() {
		t.Fatal("late save baseline incorrect")
	}
}

func TestVisibleJumpPreservesViewport(t *testing.T) {
	ed := NewPreview(theme.DefaultDark(), "long.txt", strings.Repeat("line\n", 100), 1)
	ed.Render(surface.NewSurface("jump", 60, 12, cell.TransparentBlank()), ui.PaintContext{})
	ed.scrollY = 20
	ed.SetCursorPosition(20, 0)
	ed.ScrollToCursor()
	if ed.scrollY != 20 {
		t.Fatalf("already visible target moved viewport to %d", ed.scrollY)
	}
}

func TestStringMatchLandingKeepsReadableTextAcrossThemes(t *testing.T) {
	for _, th := range theme.AllPresets() {
		ed := NewPreview(th, "target.go", "var value = \"target\"\n", 1)
		ed.SetCursorPosition(0, 13)
		ed.SetLandingRange(0, 13, 19)
		surf := surface.NewSurface("jump", 60, 12, cell.TransparentBlank())
		ed.Render(surf, ui.PaintContext{})
		for x := 13; x < 19; x++ {
			c := surf.Buffer().Get(ed.gutterWidth()+x, 0)
			if c.Char == 0 || c.Char == ' ' || c.Attr.Has(cell.AttrReverse) || cell.ContrastRatio(c.FG, c.BG) < 4.5 {
				t.Fatalf("%s unreadable target cell: %+v", th.Name, c)
			}
		}
	}
}
