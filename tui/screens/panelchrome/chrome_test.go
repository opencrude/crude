package panelchrome

import (
	"strings"
	"testing"

	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestFieldRectStartsAfterIcon(t *testing.T) {
	title := Title{Icon: "◈"}
	r := title.FieldRect(20)
	wantX := PadLeft + rendering.VisibleWidth(title.Icon) + 1
	if r.X != wantX || r.Y != 0 || r.H != SearchRows {
		t.Fatalf("FieldRect=%+v want X=%d Y=0 H=%d", r, wantX, SearchRows)
	}
	if r.W != 20-wantX {
		t.Fatalf("FieldRect.W=%d want %d", r.W, 20-wantX)
	}
}

func TestFieldRectReservesBadge(t *testing.T) {
	title := Title{Icon: "◈", Badge: "12"}
	r := title.FieldRect(20)
	badgeEnd := rendering.VisibleWidth(title.Badge) + 1
	if r.X+r.W > 20-badgeEnd {
		t.Fatalf("field %+v overlaps badge reserved cols %d", r, badgeEnd)
	}
}

func TestFieldRectReservesBadgeParts(t *testing.T) {
	title := Title{Icon: "◈", BadgeParts: []BadgePart{{Text: "M"}, {Text: "✕2"}}}
	r := title.FieldRect(20)
	if r.X+r.W > 20-title.badgeCols() {
		t.Fatalf("field %+v overlaps badge parts reserved cols %d", r, title.badgeCols())
	}
}

func TestPaintTitleBadgePartsTrailLikeAgentCount(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	buf := surface.NewCellBuffer(24, 1)
	PaintTitle(buf, 0, 0, ui.Rect{W: 24, H: 1}, tokens, Title{
		Icon:       "◈",
		BadgeParts: []BadgePart{{Text: "M"}, {Text: "✕2"}},
	})
	row := strings.TrimRight(rowText(buf, 0), " ")
	if !strings.HasSuffix(row, "M ✕2") {
		t.Fatalf("header=%q want trailing git/diag chips like the Agents count", row)
	}
	if strings.Index(row, "◈") >= strings.Index(row, "M") {
		t.Fatalf("chips must sit after the icon: %q", row)
	}
}

func TestPaintTitleOmitsPanelName(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	buf := surface.NewCellBuffer(24, 2)
	PaintTitle(buf, 0, 0, ui.Rect{W: 24, H: 2}, tokens, Title{Icon: "◈", Badge: "3"})
	var b strings.Builder
	for x := 0; x < buf.Width(); x++ {
		if c := buf.Get(x, 0).Char; c != 0 {
			b.WriteRune(c)
		}
	}
	row := strings.TrimRight(b.String(), " ")
	if strings.Contains(row, "AGENTS") || strings.Contains(row, "GIT") {
		t.Fatalf("header painted a panel name: %q", row)
	}
	if !strings.Contains(row, "◈") || !strings.Contains(row, "3") {
		t.Fatalf("header=%q want icon and badge", row)
	}
	if strings.TrimSpace(rowText(buf, 1)) != "" {
		t.Fatalf("title must occupy a single row, row1=%q", rowText(buf, 1))
	}
}

func TestBodyWidthLeavesScrollbarTrack(t *testing.T) {
	if got := BodyWidth(20); got != 18 {
		t.Fatalf("BodyWidth(20)=%d want 18", got)
	}
}

func TestNarrowChromeNeverFabricatesWidth(t *testing.T) {
	for w := 0; w < 20; w++ {
		body := BodyWidth(w)
		r := (Title{Icon: "▦", Badge: "100"}).FieldRect(body)
		if body < 0 || body > w || r.X < 0 || r.W < 0 || r.X+r.W > body {
			t.Fatalf("width %d: body=%d field=%+v", w, body, r)
		}
	}
}

func rowText(buf *surface.CellBuffer, y int) string {
	var b strings.Builder
	for x := 0; x < buf.Width(); x++ {
		if c := buf.Get(x, y).Char; c != 0 {
			b.WriteRune(c)
		}
	}
	return b.String()
}
