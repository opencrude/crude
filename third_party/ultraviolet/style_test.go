package uv

import (
	"bytes"
	"io"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

var benchmarkStyleString string

func BenchmarkTrueColorStyleDiff(b *testing.B) {
	from := Style{Fg: NewColor(ansi.RGBColor{R: 1, G: 2, B: 3}), Bg: NewColor(ansi.RGBColor{R: 4, G: 5, B: 6})}
	to := Style{Fg: NewColor(ansi.RGBColor{R: 17, G: 23, B: 251}), Bg: NewColor(ansi.RGBColor{R: 201, G: 99, B: 5}), Attrs: AttrBold}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchmarkStyleString = StyleDiff(&from, &to)
		from, to = to, from
	}
}

func BenchmarkAppendTrueColorStyleDiff(b *testing.B) {
	from := Style{Fg: RGBColor(1, 2, 3), Bg: RGBColor(4, 5, 6)}
	to := Style{Fg: RGBColor(17, 23, 251), Bg: RGBColor(201, 99, 5), Attrs: AttrBold}
	buf := make([]byte, 0, 128)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf = AppendStyleDiff(buf[:0], &from, &to)
		from, to = to, from
	}
}

func TestAppendStyleDiffPreservesPrefixAndAllocatesNothing(t *testing.T) {
	from := Style{Fg: RGBColor(1, 2, 3), Attrs: AttrBold | AttrBlink}
	to := Style{Fg: RGBColor(255, 254, 253), Bg: IndexedColor(255), UnderlineColor: BasicColor(15), Underline: UnderlineCurly, Attrs: AttrFaint | AttrRapidBlink}
	buf := make([]byte, 7, 128)
	copy(buf, "prefix:")
	const want = "prefix:\x1b[38;2;255;254;253;48;5;255;58;5;15;22;25;2;6;4:3m"
	if got := string(AppendStyleDiff(buf, &from, &to)); got != want {
		t.Fatalf("transition = %q, want %q", got, want)
	}
	if got := string(AppendStyleDiff(buf, &to, &to)); got != "prefix:" {
		t.Fatalf("equal styles changed prefix: %q", got)
	}
	if got := testing.AllocsPerRun(100, func() { buf = AppendStyleDiff(buf[:7], &from, &to) }); got != 0 {
		t.Fatalf("append allocated per transition: %v", got)
	}
}

func TestRendererPenTrueColorTransitions(t *testing.T) {
	r := NewTerminalRenderer(io.Discard, nil)
	r.SetColorProfile(colorprofile.TrueColor)
	r.buf.Grow(128)
	from := Cell{Style: Style{Fg: RGBColor(1, 2, 3), Bg: IndexedColor(2)}}
	to := Cell{Style: Style{Fg: RGBColor(239, 45, 201), Bg: RGBColor(0, 255, 0)}}
	r.cur.Style = from.Style
	r.updatePen(&to)
	if got, want := r.buf.String(), "\x1b[38;2;239;45;201;48;2;0;255;0m"; got != want {
		t.Fatalf("renderer emitted %q, want %q", got, want)
	}
	if r.cur.Style != to.Style {
		t.Fatal("renderer retained stale pen")
	}
	if got := testing.AllocsPerRun(100, func() {
		r.buf.Reset()
		r.cur.Style = from.Style
		r.updatePen(&to)
	}); got != 0 {
		t.Fatalf("renderer boxes colors or style output per cell: %v allocations", got)
	}
	r.buf.Reset()
	r.updatePen(nil)
	if !bytes.Equal(r.buf.Bytes(), []byte("\x1b[m")) || r.cur.Style != (Style{}) {
		t.Fatal("renderer did not reset to terminal defaults")
	}
}
