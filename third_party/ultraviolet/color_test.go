package uv

import (
	"image/color"
	"testing"
	"unsafe"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

func TestTerminalColorIdentityAndSequences(t *testing.T) {
	for _, tt := range []struct {
		name string
		c    Color
		want string
	}{
		{"default", 0, "\x1b[m"},
		{"RGB black", RGBColor(0, 0, 0), "\x1b[38;2;0;0;0;48;2;0;0;0;58;2;0;0;0m"},
		{"RGB white", RGBColor(255, 255, 255), "\x1b[38;2;255;255;255;48;2;255;255;255;58;2;255;255;255m"},
		{"basic", BasicColor(1), "\x1b[31;41;58;5;1m"},
		{"bright", BasicColor(15), "\x1b[97;107;58;5;15m"},
		{"indexed", IndexedColor(1), "\x1b[38;5;1;48;5;1;58;5;1m"},
		{"indexed max", IndexedColor(255), "\x1b[38;5;255;48;5;255;58;5;255m"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := Style{Fg: tt.c, Bg: tt.c, UnderlineColor: tt.c}
			if got := s.String(); got != tt.want {
				t.Fatalf("String = %q, want %q", got, tt.want)
			}
		})
	}
	// All represent black under the default palette, but palette mutation and
	// SGR type are terminal-owned semantics and must not be discarded by RGBA equality.
	colors := []Color{0, RGBColor(0, 0, 0), IndexedColor(0), BasicColor(0)}
	for i, a := range colors {
		for j, b := range colors {
			from, to := Style{Fg: a}, Style{Fg: b}
			if i != j && (from.Equal(&to) || StyleDiff(&from, &to) == "") {
				t.Fatalf("lost distinct terminal color identity: %v and %v", a, b)
			}
		}
	}
	if unsafe.Sizeof(Color(0)) != 4 || unsafe.Sizeof(Style{}) > 20 {
		t.Fatalf("color/style stopped being compact inline values: %d/%d", unsafe.Sizeof(Color(0)), unsafe.Sizeof(Style{}))
	}
}

func TestExternalColorNormalizedAndSnapshotImmutable(t *testing.T) {
	external := color.RGBA{R: 12, G: 34, B: 56, A: 255}
	s := Style{Fg: NewColor(&external), Bg: NewColor(ansi.IndexedColor(0)), UnderlineColor: NewColor(ansi.BrightWhite)}
	external.R, external.G, external.B = 200, 100, 50
	if s.Fg != RGBColor(12, 34, 56) || s.Bg != IndexedColor(0) || s.UnderlineColor != BasicColor(15) || NewColor(nil) != 0 {
		t.Fatalf("normalization lost terminal identity or retained mutable input: %#v", s)
	}
	r, g, b, a := s.Fg.RGBA()
	if r != 12*257 || g != 34*257 || b != 56*257 || a != 65535 {
		t.Fatalf("RGBA = %d,%d,%d,%d", r, g, b, a)
	}
	for _, p := range []colorprofile.Profile{colorprofile.ANSI, colorprofile.ANSI256} {
		got := ConvertStyle(s, p)
		want := NewColor(p.Convert(color.RGBA{R: 12, G: 34, B: 56, A: 255}))
		if got.Fg != want {
			t.Fatalf("profile %v conversion = %v, want %v", p, got.Fg, want)
		}
	}
}
