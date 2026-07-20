package compositor

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"

	"github.com/charmbracelet/x/ansi"
	uv "github.com/charmbracelet/ultraviolet"
)

// TestEndToEndWidthAgreement replicates the exact app pipeline:
//
//	ANSI string → RenderToSurface (compositor) → GenerateANSI →
//	UV NewStyledString.Draw (UV re-parse)
//
// and asserts the cell at every column matches between the compositor's
// surface and UV's cellbuf. If any column disagrees, UV places content
// where the compositor didn't (or vice versa) → the ghost artifacts the
// user sees.
//
// This is the definitive test for "bubble tea/ultra violet cant track
// that place" — it uses REAL UTF-8 emoji/markdown content from
// TERMINAL_WIDTH.md, not synthetic strings.
func TestEndToEndWidthAgreement(t *testing.T) {
	lines := []string{
		"| Layout pads ☰ as 2, terminal advances 1 | Settings “Interface” row unaligned |",
		"✅ DONE (partial)",
		"- [ ] Launcher lists workspaces",
		"emoji: 🧑\u200d🌾 👨\u200d👩\u200d👧\u200d👦 ⭐️ ♻️ ⚡️ 🇯🇵",
		"☰ Interface  ⚡ Providers  ◆ Appearance",
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

			for _, line := range lines {
				const w = 200
				// Step 1: compositor path — RenderToSurface (the SAME
				// decoder ultraviolet uses) packs cells, then
				// GenerateANSI emits the ANSI string the app feeds to
				// bubbletea. This is the real app data flow.
				surf := surface.NewSurface("test", w, 1, cell.TransparentBlank())
				rendering.RenderToSurface(surf, line)
				emitted := GenerateANSI(surf.Buffer())

				// The compositor CONTENT width — what layout uses to
				// pad/truncate/position the actual text.
				compW := rendering.VisibleWidth(line)

				// Step 2: UV re-parse of the EMITTED ANSI (as bubbletea
				// does). Draw into a real ScreenBuffer so we can inspect
				// cell-by-cell. The buffer is w wide; UV fills the rest
				// with EmptyCell (spaces).
				buf := uv.NewScreenBuffer(w, 1)
				buf.Method = ansi.ActiveMethod()
				uv.NewStyledString(emitted).Draw(buf, uv.Rect(0, 0, w, 1))

				// For each content column 0..compW-1, the cell UV placed
				// must match the cell the compositor packed. A mismatch
				// means UV and the compositor disagree on that column —
				// the literal ghost artifact (stale cell from a prior
				// frame shows through).
				mismatch := 0
				for x := 0; x < compW && x < w; x++ {
					sc := surf.Buffer().Get(x, 0)
					uc := buf.CellAt(x, 0)
					// Compare the visible character. Surface uses
					// Cell.Char (rune); UV uses Cell.Content (string).
					// A space in the surface is Char==0 or Char==' ';
					// in UV it's EmptyCell (Content==" "). Normalize.
					sChar := rune(sc.Char)
					if sChar == 0 {
						sChar = ' '
					}
					uChar := ' '
					if uc != nil && !uc.IsZero() && !uc.Equal(&uv.EmptyCell) {
						for _, r := range uc.Content {
							uChar = r
							break
						}
					}
					if sChar != uChar {
						if mismatch < 8 {
							t.Errorf("  col %d: compositor=%q UV=%q", x, string(sChar), string(uChar))
						}
						mismatch++
					}
				}
				if mismatch > 0 {
					t.Errorf("%q: %d/%d content columns disagree between compositor and UV — ghost artifact source (compW=%d)",
						line, mismatch, compW, compW)
				}
			}
		})
	}
}