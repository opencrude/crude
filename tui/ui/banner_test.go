package ui

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

func TestPaintBannerOwnsWordmarkIdentity(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	buf := surface.NewCellBuffer(60, BannerHeight)
	PaintBanner(buf, 0, 0, 60, tokens, cell.NoColor)

	rows := BannerText()
	if len(rows) != BannerHeight {
		t.Fatalf("banner rows=%d, want %d", len(rows), BannerHeight)
	}
	accent := tokens.Color(ColorAccent)
	for y, want := range rows {
		var got strings.Builder
		for x := 0; x < 60; x++ {
			c := buf.Get(x, y)
			if c.Width == 0 {
				continue
			}
			got.WriteRune(c.Char)
			if c.Char == ' ' {
				continue
			}
			if c.FG != accent || c.Attr&cell.AttrBold == 0 {
				t.Fatalf("row %d column %d is not the accent wordmark: %+v", y, x, c)
			}
		}
		if strings.TrimRight(got.String(), " ") != strings.TrimRight(want, " ") {
			t.Fatalf("row %d = %q, want %q", y, got.String(), want)
		}
	}
}

func TestPaintBannerTruncatesWithoutLeaking(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	buf := surface.NewCellBuffer(20, BannerHeight)
	PaintBanner(buf, 0, 0, 10, tokens, cell.NoColor)
	for y := 0; y < BannerHeight; y++ {
		var got strings.Builder
		for x := 0; x < 20; x++ {
			if c := buf.Get(x, y); c.Width > 0 {
				got.WriteRune(c.Char)
			}
		}
		line := strings.TrimRight(got.String(), " ")
		if len([]rune(line)) > 10 {
			t.Fatalf("row %d leaked past its width: %q", y, line)
		}
		if !strings.HasSuffix(line, "…") {
			t.Fatalf("row %d lost the truncation marker: %q", y, line)
		}
	}
}

func TestPaintBannerSkipsEmptyGeometry(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	buf := surface.NewCellBuffer(20, BannerHeight)
	PaintBanner(buf, 0, 0, 0, tokens, cell.NoColor)
	PaintBanner(nil, 0, 0, 20, tokens, cell.NoColor)
	for _, c := range buf.Cells() {
		if c.Char != 0 && c.Char != ' ' {
			t.Fatalf("empty geometry painted %q", string(c.Char))
		}
	}
}
