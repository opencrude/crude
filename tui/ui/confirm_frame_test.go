package ui

import (
	"strings"
	"testing"
	"unicode"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/wm"
)

func TestBuildDirtyCloseFrame_PaintsAndFocusesCancel(t *testing.T) {
	th := theme.DefaultDark()
	cw := BuildDirtyCloseFrame(th, "doc.go", 2)
	w, h := cw.Measure(80, 40)
	if w < 50 || h < 6 {
		t.Fatalf("unexpected size %dx%d", w, h)
	}
	surf := surface.NewSurface("dirty", w, h, cell.TransparentBlank())
	cw.Render(surf, &wm.RenderContext{Theme: th, Focused: true})
	buf := surf.Buffer()
	nonBlank := 0
	for x := 0; x < w; x++ {
		c := buf.Get(x, 0)
		if c.Char != 0 && c.Char != ' ' {
			nonBlank++
		}
	}
	if nonBlank == 0 {
		t.Fatal("expected title/border on row 0")
	}
	if cw.Host().Focused() != ConfirmButtonID(2) {
		t.Fatalf("expected Cancel focused, got %q", cw.Host().Focused())
	}
}

func TestBuildTreeDeleteFrame_DefaultNo(t *testing.T) {
	th := theme.DefaultDark()
	cw := BuildTreeDeleteFrame(th, `Delete "x"?`, "/tmp/x", 1)
	if cw.Host().Focused() != ConfirmButtonID(1) {
		t.Fatalf("expected No focused, got %q", cw.Host().Focused())
	}
	// Hit-test Yes button region exists
	w, h := cw.Measure(80, 40)
	cw.Host().Resize(w, h)
	cw.Host().Layout()
	found := false
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if ConfirmButtonIndexFromID(cw.Host().HitTest(x, y)) == 0 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("expected Yes button hit region")
	}
}

func TestConfirmButtonIndexFromID(t *testing.T) {
	if ConfirmButtonIndexFromID(ConfirmButtonID(0)) != 0 {
		t.Fatal("btn 0")
	}
	if ConfirmButtonIndexFromID(ComponentID("confirm/btn-2")) != 2 {
		t.Fatal("btn 2")
	}
	if ConfirmButtonIndexFromID(ComponentID("other")) != -1 {
		t.Fatal("other")
	}
}

// Regression for the full-width accent slab: buttons in the confirm row must
// size to their labels, not stretch to the dialog width.
func TestConfirmButtonRowStaysLabelSized(t *testing.T) {
	th := theme.DefaultDark()
	cw := BuildTreeDeleteFrame(th, `Delete "vm"?`, "/tmp/vm", 1)
	w, h := cw.Measure(80, 40)
	cw.Host().Resize(w, h)
	cw.Host().Layout()
	no := cw.Host().registry[ConfirmButtonID(1)].(*Button)
	// "[ No ]" measures 6; anything near dialog width means the old
	// last-child stretch leaked back in.
	if bw := no.Bounds().W; bw > len("[ No ]")+2 {
		t.Fatalf("No button stretched to %d of %d dialog width", bw, w)
	}
	yes := cw.Host().registry[ConfirmButtonID(0)].(*Button)
	if yes.Bounds().W != len("[ Yes ]") {
		t.Fatalf("Yes width=%d want 7", yes.Bounds().W)
	}
}

// The tool confirmation's four-button row (with Auto Review) fits inside its
// 70-column dialog: every button keeps its full label and no button leaves the
// frame, so the last choice is never clipped.
func TestConfirmFourButtonRowFitsToolConfirmation(t *testing.T) {
	th := theme.DefaultDark()
	cw := BuildConfirmFrame(th, ConfirmFrameSpec{
		Title: "Confirmation required", Question: "Allow this operation?",
		Buttons: []ConfirmButtonSpec{
			{Label: "Allow", ActionID: ConfirmActionYes}, {Label: "Always allow", ActionID: ConfirmActionSave},
			{Label: "Deny", ActionID: ConfirmActionNo}, {Label: "Auto Review…", ActionID: ConfirmActionAuto},
		},
		Width: 70,
	})
	w, h := cw.Measure(80, 40)
	cw.Host().Resize(w, h)
	cw.Host().Layout()
	labels := []string{"Allow", "Always allow", "Deny", "Auto Review…"}
	for i, label := range labels {
		btn := cw.Host().registry[ConfirmButtonID(i)].(*Button)
		b := btn.Bounds()
		if b.W < len([]rune("[ "+label+" ]")) {
			t.Fatalf("button %d %q clipped to width %d", i, label, b.W)
		}
		if b.X+b.W > w {
			t.Fatalf("button %d %q ends at %d past dialog width %d", i, label, b.X+b.W, w)
		}
	}
}

// Danger dialogs get the error-colored border accent via the severity path.
func TestConfirmFrameSeverityAccent(t *testing.T) {
	th := theme.DefaultDark()
	cw := BuildTreeDeleteFrame(th, `Delete "x"?`, "/tmp/x", 1)
	frame, ok := cw.Root().(*WindowFrame)
	if !ok {
		t.Fatal("expected WindowFrame root")
	}
	if !frame.hasBorderAccent || frame.borderAccent != ColorError {
		t.Fatalf("danger frame should carry the error accent: %v %v", frame.hasBorderAccent, frame.borderAccent)
	}
	w, h := cw.Measure(80, 40)
	surf := surface.NewSurface("danger", w, h, cell.TransparentBlank())
	cw.Render(surf, &wm.RenderContext{Theme: th, Focused: true})
	if got, want := surf.Buffer().Get(0, 0).FG, rendering.ThemeColorToCell(th.Colors.Error); got != want {
		t.Fatalf("border=%v want error %v", got, want)
	}
}

// Switching the theme while a dialog is open painted the old theme's colors
// inside the new frame: every text row had the old popup background and ink
// baked in. All of the dialog's colors are roles of the host's tokens.
func TestConfirmFrameFollowsThemeSwitch(t *testing.T) {
	dark, light := theme.DefaultDark(), theme.DefaultLight()
	cw := BuildTreeDeleteFrame(dark, `Delete "x"?`, "/tmp/x", 1)
	cw.Host().SetTheme(light)
	w, h := cw.Measure(80, 40)
	surf := surface.NewSurface("switched", w, h, cell.TransparentBlank())
	cw.Render(surf, &wm.RenderContext{Theme: light, Focused: true})
	buf := surf.Buffer()
	tokens := NewTokenSet(light)
	if got, want := buf.Get(0, 0).FG, tokens.Color(ColorError); got != want {
		t.Fatalf("border keeps the old theme: %v want %v", got, want)
	}
	question := -1
	for y := range h {
		if strings.Contains(rowText(buf, y), `"x"?`) {
			question = y
			break
		}
	}
	if question < 0 {
		t.Fatalf("no question row:\n%s", testBufferText(buf))
	}
	fill := tokens.Color(ColorPopupBg)
	for x := 1; x < w-1; x++ {
		c := buf.Get(x, question)
		if c.BG != fill {
			t.Fatalf("question row cell %d has background %v, want the light popup %v", x, c.BG, fill)
		}
		if unicode.IsLetter(c.Char) && c.FG != tokens.Color(ColorFg) {
			t.Fatalf("question ink %v at %d, want the light foreground %v", c.FG, x, tokens.Color(ColorFg))
		}
	}
}

// Long paths wrap instead of clipping mid-word.
func TestConfirmFrameDetailWraps(t *testing.T) {
	th := theme.DefaultDark()
	long := "/very/long/path/that/overflows/any/reasonable/dialog/width/deeply/nested/file.txt"
	cw := BuildTreeDeleteFrame(th, `Delete "file.txt"?`, long, 1)
	w, _ := cw.Measure(80, 40)
	if w != 60 {
		t.Fatalf("width=%d", w)
	}
	lines := wrapDetailLines(long, 60-confirmDetailWrapWidth, 0)
	if len(lines) < 2 {
		t.Fatalf("expected wrapped detail, got %v", lines)
	}
	for _, line := range lines {
		if len(line) > 60-confirmDetailWrapWidth {
			t.Fatalf("line exceeds width: %q", line)
		}
	}
}

// Hint row is the standard dialog navigation idiom.
func TestConfirmHintText(t *testing.T) {
	th := theme.DefaultDark()
	cw := BuildDirtyCloseFrame(th, "a.go", 2)
	w, h := cw.Measure(80, 40)
	surf := surface.NewSurface("hint", w, h, cell.TransparentBlank())
	cw.Render(surf, &wm.RenderContext{Theme: th, Focused: true})
	plain := testBufferText(surf.Buffer())
	if !strings.Contains(plain, "←→ switch") || !strings.Contains(plain, "esc cancel") {
		t.Fatalf("hint row missing standard navigation: %q", plain)
	}
}

// testBufferText renders a cell buffer to plain text (test-local helper).
func testBufferText(buf *surface.CellBuffer) string {
	var b strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			r := buf.Get(x, y).Char
			if r == 0 {
				b.WriteByte(' ')
			} else {
				b.WriteRune(r)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// Action semantics drive button variants: destructive = danger (error fill),
// affirmative = primary (accent fill), neutral = secondary (accent2 fill).
func TestConfirmButtonVariants(t *testing.T) {
	th := theme.DefaultDark()
	cw := BuildDirtyCloseFrame(th, "a.go", 2)
	w, h := cw.Measure(80, 40)
	cw.Host().Resize(w, h)
	cw.Host().Layout()
	want := []ButtonVariant{VariantPrimary, VariantDanger, VariantSecondary}
	for i, v := range want {
		btn := cw.Host().registry[ConfirmButtonID(i)].(*Button)
		if btn.variant != v {
			t.Fatalf("btn %d variant=%d want %d", i, btn.variant, v)
		}
	}
}

// Focus moves in place without rebuilding: MoveConfirmFocus wraps at both ends.
func TestMoveConfirmFocusWraps(t *testing.T) {
	th := theme.DefaultDark()
	cw := BuildDirtyCloseFrame(th, "a.go", 2)
	if cw.Host().Focused() != ConfirmButtonID(2) {
		t.Fatalf("start focus=%q", cw.Host().Focused())
	}
	if n := MoveConfirmFocus(cw, 1, 3); n != 0 {
		t.Fatalf("wrap forward: n=%d", n)
	}
	if cw.Host().Focused() != ConfirmButtonID(0) {
		t.Fatalf("focus after forward wrap=%q", cw.Host().Focused())
	}
	if n := MoveConfirmFocus(cw, -1, 3); n != 2 {
		t.Fatalf("wrap backward: n=%d", n)
	}
	if cw.Host().Focused() != ConfirmButtonID(2) {
		t.Fatalf("focus after backward wrap=%q", cw.Host().Focused())
	}
}

// Visual changes bridge to the compositor: the WM wires the dirty callback,
// and focus/hover moves mark window content dirty (the stale-surface bug).
func TestConfirmVisualChangeMarksWindowDirty(t *testing.T) {
	th := theme.DefaultDark()
	cw := BuildDirtyCloseFrame(th, "a.go", 2)
	dirty := 0
	cw.SetMarkContentDirty(func() { dirty++ })
	MoveConfirmFocus(cw, 1, 3)
	if dirty == 0 {
		t.Fatal("focus move did not mark window content dirty")
	}
	SetConfirmHover(cw, 1)
	if dirty < 2 {
		t.Fatal("hover move did not mark window content dirty")
	}
	// Same target again: no redundant dirty marks.
	SetConfirmHover(cw, 1)
	if dirty != 2 {
		t.Fatalf("redundant hover marked dirty %d times", dirty)
	}
}

// Hover and keyboard focus paint differently: focused button takes the
// variant accent fill, hovered (unfocused) takes the subtle ColorHover tint.
func TestConfirmHoverDistinctFromFocus(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	cw := BuildDirtyCloseFrame(th, "a.go", 2)
	w, h := cw.Measure(80, 40)
	cw.Host().Resize(w, h)
	cw.Host().Layout()
	cw.Host().Focus(ConfirmButtonID(0)) // keyboard focus on Save
	SetConfirmHover(cw, 1)              // mouse over Discard
	surf := surface.NewSurface("hv", w, h, cell.TransparentBlank())
	cw.Render(surf, &wm.RenderContext{Theme: th, Focused: true})
	buf := surf.Buffer()
	// screen rects are surface-absolute (bounds are parent-relative per the
	// paint contract); probe via Host hit-test geometry.
	fr, okF := cw.Host().ScreenRect(ConfirmButtonID(0))
	hr, okH := cw.Host().ScreenRect(ConfirmButtonID(1))
	if !okF || !okH {
		t.Fatal("button screen rects missing")
	}
	focusCell := buf.Get(fr.X+2, fr.Y)
	hoverCell := buf.Get(hr.X+2, hr.Y)
	if !focusCell.BG.Equal(tokens.Color(ColorAccent)) {
		t.Fatalf("focused fill=%v want accent", focusCell.BG)
	}
	if !hoverCell.BG.Equal(tokens.Color(ColorHover)) {
		t.Fatalf("hovered fill=%v want hover", hoverCell.BG)
	}
}
