package widgets

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/widget"
)

// TestFindReplaceFramePaintsValueViaNativeTextField asserts the find/replace
// popup's editable surfaces paint through the native ui.TextField surface path:
// the find/replace values written into the frame spec appear in the painted
// cell buffer (no ANSI bridge), and updating the mounted TextField repaints
// the new value on the next frame.
func TestFindReplaceFramePaintsValueViaNativeTextField(t *testing.T) {
	th := theme.DefaultDark()
	cw := BuildFindReplaceFrame(th, FindReplaceFrameSpec{
		FindValue:    "needle",
		ReplaceValue: "hay",
		ReplaceMode:  true,
		Width:        60,
		Height:       8,
	})

	w, h := cw.Measure(60, 8)
	surf := surface.NewSurface("fr", w, h, cell.TransparentBlank())
	ctx := &widget.RenderContext{Theme: th, Focused: true}
	cw.Render(surf, ctx)

	got := plainAll(surf)
	if !strings.Contains(got, "needle") {
		t.Fatalf("painted find surface missing value %q:\n%s", "needle", got)
	}
	if !strings.Contains(got, "hay") {
		t.Fatalf("painted replace surface missing value %q:\n%s", "hay", got)
	}

	// Repaint after mutating the mounted TextField: the next frame must show
	// the new value (proving the TextField is the live paint source).
	find := FindReplaceFrameFindField(cw)
	if find == nil {
		t.Fatal("find TextField not mounted in frame tree")
	}
	find.SetValue("updated-query")
	cw.Render(surf, ctx)
	got = plainAll(surf)
	if !strings.Contains(got, "updated-query") {
		t.Fatalf("repainted find surface missing updated value %q:\n%s", "updated-query", got)
	}
	if strings.Contains(got, "needle") {
		t.Fatalf("repainted find surface still shows stale value %q:\n%s", "needle", got)
	}
}

// plainAll joins every row of the surface into one plain-text blob.
func plainAll(surf *surface.Surface) string {
	buf := surf.Buffer()
	var b strings.Builder
	for y := 0; y < surf.Height(); y++ {
		for x := 0; x < surf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Char != 0 {
				b.WriteRune(c.Char)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
