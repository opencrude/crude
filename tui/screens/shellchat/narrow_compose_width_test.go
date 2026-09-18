package shellchat

import (
	"testing"

	"crude/tui/chatprojection"
	"crude/tui/rendering"
	"crude/tui/theme"
)

func TestComposeGeometryStaysWithinNarrowRows(t *testing.T) {
	const padLR = 2
	raw := "界🧑‍💻abcdefghijklmnopqrstuvwxyz"
	rawW := rendering.VisibleWidth(raw)

	for _, innerW := range []int{-3, 0, 1, 2, 3, 5, 9, 10, 19} {
		for _, mainScrollbar := range []bool{false, true} {
			for _, nesting := range []int{0, 1, 3} {
				p := ComposeParams{
					InnerW:        innerW,
					PadLR:         padLR,
					Theme:         theme.DefaultDark(),
					NestingLevel:  nesting,
					MainSBVisible: mainScrollbar,
					HasPaneBg:     func(string) bool { return true },
				}
				pane := chatprojection.PaneLineInfo{}
				if nesting > 0 {
					pane.PaneID = "pane"
				}

				rowInnerW := max(0, innerW)
				if mainScrollbar && rowInnerW > 0 {
					rowInnerW--
				}
				line := ComposeRow(p, 0, 0, raw, pane)
				if got, limit := rendering.VisibleWidth(line), rowInnerW+2*padLR; got > limit {
					t.Fatalf(
						"inner=%d main=%v nesting=%d composed width=%d exceeds %d",
						innerW, mainScrollbar, nesting, got, limit,
					)
				}

				layout := LayoutRow(p, rawW, pane)
				if layout.RowInnerW != rowInnerW {
					t.Fatalf("inner=%d main=%v RowInnerW=%d want %d", innerW, mainScrollbar, layout.RowInnerW, rowInnerW)
				}
				if layout.ContentMax < 0 || layout.ContentMax > layout.RowInnerW {
					t.Fatalf("inner=%d nesting=%d invalid content max %+v", innerW, nesting, layout)
				}
				drawn := min(rawW, layout.ContentMax)
				if used := drawn + layout.PadRight + layout.GutterCols; used > layout.RowInnerW {
					t.Fatalf("inner=%d nesting=%d row geometry uses %d of %d columns: %+v", innerW, nesting, used, layout.RowInnerW, layout)
				}

				listW := max(0, innerW) + 2*padLR
				gutterX := NestedGutterX(p, listW)
				if nesting == 0 {
					if gutterX != -1 {
						t.Fatalf("inner=%d no nesting gutter X=%d, want -1", innerW, gutterX)
					}
				} else if gutterX >= listW {
					t.Fatalf("inner=%d nesting=%d gutter X=%d outside list width %d", innerW, nesting, gutterX, listW)
				}
			}
		}
	}
}

func TestComposeDoesNotRestoreLegacyTwentyColumnFloor(t *testing.T) {
	p := ComposeParams{InnerW: 5, PadLR: 0, Theme: theme.DefaultDark()}
	got := rendering.VisibleWidth(ComposeRow(p, 0, 0, "a very long row", chatprojection.PaneLineInfo{}))
	if got != 5 {
		t.Fatalf("five-column compose width=%d, want exactly 5", got)
	}
}

func TestComposeRowsFitEffectiveRealContentWidth(t *testing.T) {
	const requestedPad = 2
	raw := "界🧑‍💻abcdefghijklmnopqrstuvwxyz"

	for contentW := 0; contentW <= 2*requestedPad; contentW++ {
		effectivePad := min(requestedPad, contentW/2)
		innerW := contentW - 2*effectivePad
		p := ComposeParams{
			InnerW: innerW,
			PadLR:  effectivePad,
			Theme:  theme.DefaultDark(),
		}

		got := rendering.VisibleWidth(ComposeRow(p, 0, 0, raw, chatprojection.PaneLineInfo{}))
		if got > contentW {
			t.Fatalf(
				"content=%d effective pad=%d inner=%d composed width=%d",
				contentW, effectivePad, innerW, got,
			)
		}
	}
}
