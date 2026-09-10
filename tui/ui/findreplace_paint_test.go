package ui

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/wm"
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
	})

	w, h := cw.Measure(60, 8)
	surf := surface.NewSurface("fr", w, h, cell.TransparentBlank())
	ctx := &wm.RenderContext{Theme: th, Focused: true}
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

// TestFindReplaceFramePaintsVSCodeLayout pins the chrome-clean composition:
// no in-field "Find: "/"Replace: " labels, no chevron affordances, no hint
// row, no bordered filter row — the find value leads its line ("needle ⌕
// Aa ab .*": input first, glyph + pills at the trailing edge), the replace
// input is full width, and replace mode gives the counter its own row above
// the action bar.
func TestFindReplaceFramePaintsVSCodeLayout(t *testing.T) {
	th := theme.DefaultDark()
	cw := BuildFindReplaceFrame(th, FindReplaceFrameSpec{
		FindValue:    "trimmed",
		ReplaceValue: "re",
		ReplaceMode:  true,
		MatchCount:   7,
		CurrentIdx:   1,
		Width:        60,
	})

	w, h := cw.Measure(60, 12)
	surf := surface.NewSurface("fr-vscode", w, h, cell.TransparentBlank())
	cw.Render(surf, &wm.RenderContext{Theme: th, Focused: true})
	got := plainAll(surf)

	for _, banned := range []string{"Find:", "Replace:", "›", "⌄", "enter next", "esc close", "type to search"} {
		if strings.Contains(got, banned) {
			t.Fatalf("VSCode layout must not paint %q:\n%s", banned, got)
		}
	}
	for _, want := range []string{"trimmed", "re", "Aa", "ab", ".*", "2 of 7", "[ Replace ]", "[ Replace All ]", "[ ↑ ]", "[ ↓ ]", "⌕", "⇄"} {
		if !strings.Contains(got, want) {
			t.Fatalf("VSCode layout missing %q:\n%s", want, got)
		}
	}

	// Structural pins: the find value leads its line with the ⌕ glyph and
	// Aa/ab/.* pills at the trailing edge; the replace value starts the
	// next content line (full width); the counter has its own row above
	// the action bar in replace mode.
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) < 8 {
		t.Fatalf("replace form too short:\n%s", got)
	}
	findLine := lines[1]
	if !strings.Contains(findLine, "Aa") || !strings.Contains(findLine, "trimmed") {
		t.Fatalf("find row must paint the Aa/ab/.* pills after the find value:\n%s", got)
	}
	if i, j := strings.Index(findLine, "trimmed"), strings.Index(findLine, "Aa"); j < i {
		t.Fatalf("pills must trail the find value on the same line:\n%s", got)
	}
	replaceLine := lines[3]
	if !strings.Contains(replaceLine, "re") || strings.Contains(replaceLine, "Aa") {
		t.Fatalf("replace input must be full width (no pills on its line):\n%s", got)
	}
	// Counter row: its own line above the bar (two rows above the border).
	counterLine := lines[len(lines)-4]
	if !strings.Contains(counterLine, "2 of 7") {
		t.Fatalf("replace mode counter must own its row:\n%s", got)
	}
	if strings.Contains(counterLine, "[ Replace ]") || strings.Contains(counterLine, "[ ↑ ]") {
		t.Fatalf("counter row must not carry buttons:\n%s", got)
	}
	buttonsLine := lines[len(lines)-2]
	if !strings.Contains(buttonsLine, "[ Replace ]") || !strings.Contains(buttonsLine, "[ ↑ ]") {
		t.Fatalf("replace mode action bar must paint below the counter row:\n%s", got)
	}

	// Find mode drops the replace field, its buttons, and the counter row
	// (the counter shares the action row there).
	findOnly := BuildFindReplaceFrame(th, FindReplaceFrameSpec{
		FindValue: "x", MatchCount: 1, CurrentIdx: 0, Width: 44,
	})
	w2, h2 := findOnly.Measure(44, 12)
	surf2 := surface.NewSurface("fr-find", w2, h2, cell.TransparentBlank())
	findOnly.Render(surf2, &wm.RenderContext{Theme: th, Focused: true})
	gotFind := plainAll(surf2)
	if h2 != 5 {
		t.Fatalf("find form measured h=%d, want 5 for find-row+gap+buttons", h2)
	}
	for _, banned := range []string{"Replace", "[ Replace All ]"} {
		if strings.Contains(gotFind, banned) {
			t.Fatalf("find mode must not paint %q:\n%s", banned, gotFind)
		}
	}
	if !strings.Contains(gotFind, "[ ↑ ]") || !strings.Contains(gotFind, "[ ↓ ]") {
		t.Fatalf("find mode must keep the ↑/↓ pair:\n%s", gotFind)
	}
	// The find-mode counter shares the action row (compact bar leaves room).
	if !strings.Contains(gotFind, "1 of 1") {
		t.Fatalf("find mode counter must share the action row:\n%s", gotFind)
	}
}

// TestFindReplaceFrameMeasuresHeightPerMode pins the smooth Ctrl+F ↔ Ctrl+H
// transform: the frame measures its own height from the tree, so replace
// is exactly four rows taller than find (the replace row and the dedicated
// counter row, each with its gap). The window must adopt the measured
// height, never clip the button row or leave blank rows.
func TestFindReplaceFrameMeasuresHeightPerMode(t *testing.T) {
	th := theme.DefaultDark()
	find := BuildFindReplaceFrame(th, FindReplaceFrameSpec{
		FindValue: "x", MatchCount: 1, CurrentIdx: 0, Width: 60,
	})
	replace := BuildFindReplaceFrame(th, FindReplaceFrameSpec{
		FindValue: "x", ReplaceValue: "y", ReplaceMode: true,
		MatchCount: 1, CurrentIdx: 0, Width: 60,
	})
	fw, fh := find.Measure(60, 14)
	rw, rh := replace.Measure(60, 14)
	if fw != rw {
		t.Fatalf("widths differ across transform: find=%d replace=%d", fw, rw)
	}
	if rh-fh != 4 {
		t.Fatalf("replace must be 4 rows taller than find (replace+gap, counter+gap): find=%d replace=%d", fh, rh)
	}

	// Both painted surfaces end on the button row: no clipped border text,
	// no trailing blank row.
	for name, cw := range map[string]*ComponentWidget{"find": find, "replace": replace} {
		w, h := cw.Measure(60, 14)
		surf := surface.NewSurface("fr-"+name, w, h, cell.TransparentBlank())
		cw.Render(surf, &wm.RenderContext{Theme: th, Focused: true})
		got := plainAll(surf)
		if !strings.Contains(got, "[ ↓ ]") {
			t.Fatalf("%s form clipped its button row:\n%s", name, got)
		}
		lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
		if last := lines[len(lines)-1]; !strings.HasPrefix(last, "╰") {
			t.Fatalf("%s form last row is not the border (blank-row leak):\n%s", name, got)
		}
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
