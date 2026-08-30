package compositor

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
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
// ../../docs/reference/terminal-width.md, not synthetic strings.
func TestEndToEndWidthAgreement(t *testing.T) {
	lines := []string{
		"| Layout and UV both measure ☰ as 2 | Settings stay aligned |",
		"✅ DONE (partial)",
		"- [ ] Launcher lists workspaces",
		"emoji: 🧑\u200d🌾 👨\u200d👩\u200d👧\u200d👦 ⭐️ ♻️ ⚡️ 🇯🇵",
		"☰ Interface  ⚡ Providers  ◆ Appearance",
	}

	for _, line := range lines {
		const w = 200
		// Step 1: compositor path — RenderToSurface (the SAME
		// decoder ultraviolet uses) packs cells, then GenerateANSI emits
		// the ANSI string the app feeds to Bubble Tea.
		surf := surface.NewSurface("test", w, 1, cell.TransparentBlank())
		rendering.RenderToSurface(surf, line)
		emitted := GenerateANSI(surf.Buffer())

		// The compositor CONTENT width — what layout uses to
		// pad, truncate, and position the actual text.
		compW := rendering.VisibleWidth(line)

		// Step 2: UV re-parses the emitted ANSI as Bubble Tea does.
		buf := uv.NewScreenBuffer(w, 1)
		buf.Method = ansi.GraphemeWidth
		uv.NewStyledString(emitted).Draw(buf, uv.Rect(0, 0, w, 1))

		// Every content column must match the compositor. A mismatch leaves
		// stale cells visible at the right edge after a frame update.
		mismatch := 0
		for x := 0; x < compW && x < w; x++ {
			sc := surf.Buffer().Get(x, 0)
			uc := buf.CellAt(x, 0)
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
					t.Errorf("col %d: compositor=%q UV=%q", x, string(sChar), string(uChar))
				}
				mismatch++
			}
		}
		if mismatch > 0 {
			t.Errorf("%q: %d/%d content columns disagree between compositor and UV (compW=%d)",
				line, mismatch, compW, compW)
		}
	}
}

func TestFloatingBorderOccludesEmojiWithoutHalfGrapheme(t *testing.T) {
	for _, borderX := range []int{1, 2} {
		background := surface.NewCellBuffer(6, 1)
		background.Fill(cell.NewCell(' ', cell.White, cell.Black, 0))
		background.WriteString(1, 0, "😀", cell.White, cell.Black, 0)
		border := surface.NewCellBuffer(1, 1)
		border.Set(0, 0, cell.NewCell('│', cell.Yellow, cell.Black, 0))
		surface.Blit(background, borderX, 0, border, nil)

		emitted := GenerateANSI(background)
		uvBuffer := uv.NewScreenBuffer(6, 1)
		uvBuffer.Method = ansi.GraphemeWidth
		uv.NewStyledString(emitted).Draw(uvBuffer, uv.Rect(0, 0, 6, 1))

		for column := 0; column < 6; column++ {
			cellAt := uvBuffer.CellAt(column, 0)
			if cellAt != nil && cellAt.Content == "😀" {
				t.Fatalf("border x=%d left underlying emoji at column %d", borderX, column)
			}
		}
		if got := uvBuffer.CellAt(borderX, 0); got == nil || got.Content != "│" || got.Width != 1 {
			t.Fatalf("border x=%d reparsed as %#v", borderX, got)
		}
	}
}
