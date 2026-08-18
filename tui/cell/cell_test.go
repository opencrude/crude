package cell

import (
	"math"
	"strings"
	"testing"
)

func TestColorConstructors(t *testing.T) {
	tc := NewTrueColor(1, 2, 3)
	if tc.Type != ColorTrueColor || tc.R != 1 || tc.G != 2 || tc.B != 3 {
		t.Fatalf("truecolor: %+v", tc)
	}
	pc := NewPaletteColor(196)
	if pc.Type != ColorPalette || pc.Palette != 196 {
		t.Fatalf("palette: %+v", pc)
	}
	if !NoColor.IsDefault() {
		t.Fatal("NoColor should be default")
	}
}

func TestColorEqual(t *testing.T) {
	cases := []struct {
		a, b Color
		eq   bool
	}{
		{NoColor, NoColor, true},
		{NoColor, Black, false},
		{NewPaletteColor(7), NewPaletteColor(7), true},
		{NewPaletteColor(7), NewPaletteColor(8), false},
		{NewTrueColor(1, 2, 3), NewTrueColor(1, 2, 3), true},
		{NewTrueColor(1, 2, 3), NewTrueColor(1, 2, 4), false},
		{NewPaletteColor(1), NewTrueColor(205, 0, 0), false},
	}
	for i, c := range cases {
		if got := c.a.Equal(c.b); got != c.eq {
			t.Fatalf("case %d: Equal=%v want %v", i, got, c.eq)
		}
	}
}

func TestColorANSIFG_BG(t *testing.T) {
	if NoColor.ANSIFG() != "" || NoColor.ANSIBG() != "" {
		t.Fatal("default color must emit empty ANSI")
	}
	fg := NewPaletteColor(196).ANSIFG()
	if fg != "\x1b[38;5;196m" {
		t.Fatalf("palette FG: %q", fg)
	}
	bg := NewPaletteColor(196).ANSIBG()
	if bg != "\x1b[48;5;196m" {
		t.Fatalf("palette BG: %q", bg)
	}
	tfg := NewTrueColor(10, 20, 30).ANSIFG()
	if tfg != "\x1b[38;2;10;20;30m" {
		t.Fatalf("truecolor FG: %q", tfg)
	}
	tbg := NewTrueColor(10, 20, 30).ANSIBG()
	if tbg != "\x1b[48;2;10;20;30m" {
		t.Fatalf("truecolor BG: %q", tbg)
	}
}

func TestColorBlend(t *testing.T) {
	base := NewTrueColor(0, 0, 0)
	over := NewTrueColor(255, 0, 0)

	if got := base.Blend(NoColor, 0.5); !got.Equal(base) {
		t.Fatalf("default over should keep base: %+v", got)
	}
	if got := base.Blend(over, 0); !got.Equal(base) {
		t.Fatalf("alpha=0 should keep base")
	}
	if got := base.Blend(over, -1); !got.Equal(base) {
		t.Fatalf("negative alpha should keep base")
	}
	if got := base.Blend(over, float32(math.NaN())); !got.Equal(base) {
		t.Fatalf("NaN alpha should keep base")
	}
	if got := base.Blend(over, 1); !got.Equal(over) {
		t.Fatalf("alpha=1 should return over: %+v", got)
	}
	if got := base.Blend(over, 2); !got.Equal(over) {
		t.Fatalf("alpha>1 should return over")
	}

	mid := base.Blend(over, 0.5)
	if mid.Type != ColorTrueColor {
		t.Fatalf("blend must return truecolor, got %v", mid.Type)
	}
	if mid.R != 127 || mid.G != 0 || mid.B != 0 {
		// 255*0.5 = 127.5 → 127 as uint8 truncation of float
		if mid.R < 126 || mid.R > 128 {
			t.Fatalf("expected ~127 red, got %+v", mid)
		}
	}

	// Default bottom treated as black.
	def := NoColor.Blend(over, 0.5)
	if def.Type != ColorTrueColor || def.R == 0 {
		t.Fatalf("default bottom should blend as black: %+v", def)
	}

	// Palette over truecolor produces truecolor.
	pal := NewTrueColor(0, 0, 0).Blend(Red, 1.0)
	if !pal.Equal(Red) {
		t.Fatalf("opaque palette over: %+v", pal)
	}
}

func TestContrastColorPicksReadableForeground(t *testing.T) {
	onLight := ContrastColor(NewTrueColor(0xf0, 0xf6, 0xfc))
	if int(onLight.R)+int(onLight.G)+int(onLight.B) > 120 {
		t.Fatalf("light fill should get a dark fg: %+v", onLight)
	}
	onDark := ContrastColor(NewTrueColor(0x0d, 0x11, 0x17))
	if int(onDark.R)+int(onDark.G)+int(onDark.B) < 400 {
		t.Fatalf("dark fill should get a light fg: %+v", onDark)
	}
}

func TestColorToRGB_PaletteCubeAndGray(t *testing.T) {
	// Standard red
	r, g, b := colorToRGB(Red)
	if r != 205 || g != 0 || b != 0 {
		t.Fatalf("Red RGB: %d %d %d", r, g, b)
	}
	// Grayscale ramp 232
	r, g, b = colorToRGB(GrayDark)
	if r != g || g != b || r != 8 {
		t.Fatalf("GrayDark: %d %d %d", r, g, b)
	}
	// Cube index 16 = first cube cell (black-ish)
	r, g, b = colorToRGB(NewPaletteColor(16))
	if r != 0 || g != 0 || b != 0 {
		t.Fatalf("cube 16: %d %d %d", r, g, b)
	}
	// Cube with non-zero components: index 196 is bright red in many palettes;
	// formula uses (idx/36)*40 etc. Just ensure it returns something non-default path.
	r, g, b = colorToRGB(NewPaletteColor(196))
	if r == 0 && g == 0 && b == 0 {
		t.Fatalf("cube 196 should not be pure black via formula")
	}
	// Default path
	r, g, b = colorToRGB(NoColor)
	if r != 0 || g != 0 || b != 0 {
		t.Fatalf("default RGB should be black")
	}
}

func TestAttrsANSI(t *testing.T) {
	if Attrs(0).ANSI() != "\x1b[0m" {
		t.Fatalf("zero attrs: %q", Attrs(0).ANSI())
	}
	a := AttrBold | AttrItalic | AttrUnderline
	s := a.ANSI()
	if !strings.HasPrefix(s, "\x1b[0") || !strings.HasSuffix(s, "m") {
		t.Fatalf("unexpected attrs ANSI: %q", s)
	}
	if !strings.Contains(s, ";1") || !strings.Contains(s, ";3") || !strings.Contains(s, ";4") {
		t.Fatalf("missing attr codes in %q", s)
	}
	curl := AttrUndercurl.ANSI()
	if !strings.Contains(curl, "4:3") {
		t.Fatalf("undercurl missing 4:3: %q", curl)
	}
	dot := AttrUnderlineDotted.ANSI()
	if !strings.Contains(dot, "4:4") {
		t.Fatalf("dotted missing 4:4: %q", dot)
	}
}

func TestAttrsHas(t *testing.T) {
	a := AttrBold | AttrItalic
	if !a.Has(AttrBold) || !a.Has(AttrItalic) {
		t.Fatal("Has failed for set bits")
	}
	if a.Has(AttrUnderline) {
		t.Fatal("Has true for unset bit")
	}
	if !a.Has(AttrBold | AttrItalic) {
		t.Fatal("Has failed for combined mask")
	}
	if a.Has(AttrBold | AttrUnderline) {
		t.Fatal("Has true when only partial mask matches")
	}
}

func TestCellConstructors(t *testing.T) {
	c := NewCell('a', Red, Blue, AttrBold)
	if c.Char != 'a' || c.Width != 1 || !c.FG.Equal(Red) || !c.BG.Equal(Blue) || c.Attr != AttrBold {
		t.Fatalf("NewCell: %+v", c)
	}
	w := NewWideCell('界', Red, Blue, 0)
	if w.Width != 2 || w.Char != '界' {
		t.Fatalf("NewWideCell: %+v", w)
	}
	if ContinuationCell.Width != 0 || ContinuationCell.Char != 0 {
		t.Fatalf("ContinuationCell: %+v", ContinuationCell)
	}
	blank := BlankCell(Blue)
	if blank.Char != ' ' || blank.Width != 1 || !blank.BG.Equal(Blue) {
		t.Fatalf("BlankCell: %+v", blank)
	}
	tb := TransparentBlank()
	if !tb.Transparent || tb.Char != ' ' {
		t.Fatalf("TransparentBlank: %+v", tb)
	}
}

func TestCellPredicates(t *testing.T) {
	if !BlankCell(NoColor).IsBlank() {
		t.Fatal("space should be blank")
	}
	if !ContinuationCell.IsBlank() {
		t.Fatal("continuation should be blank")
	}
	if NewCell('x', NoColor, NoColor, 0).IsBlank() {
		t.Fatal("letter should not be blank")
	}
	if TransparentBlank().IsVisible() {
		t.Fatal("transparent blank not visible")
	}
	if !NewCell('x', NoColor, NoColor, 0).IsVisible() {
		t.Fatal("letter should be visible")
	}
	if BlankCell(NoColor).IsVisible() {
		t.Fatal("space not visible")
	}
}

func TestCellEqual(t *testing.T) {
	a := NewCell('x', Red, Blue, AttrBold)
	b := NewCell('x', Red, Blue, AttrBold)
	if !a.Equal(b) {
		t.Fatal("identical cells should equal")
	}
	c := a
	c.Transparent = true
	if a.Equal(c) {
		t.Fatal("transparent difference")
	}
	d := a
	d.Width = 2
	if a.Equal(d) {
		t.Fatal("width difference")
	}
	e := a
	e.FG = Green
	if a.Equal(e) {
		t.Fatal("fg difference")
	}
}
