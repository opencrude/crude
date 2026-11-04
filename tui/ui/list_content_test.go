package ui

import (
	"strings"
	"testing"

	"crude/tui/theme"
)

func TestListContentSizeTracksItemsButNotSelection(t *testing.T) {
	list := NewList("intrinsic-list", NewTokenSet(theme.DefaultDark()))
	cs := Constraints{MaxW: 90, MaxH: 12}
	list.SetItems([]ListItem{{ID: "one", Label: "main.go:3:6"}, {ID: "two", Label: "use.go:3:13"}})
	if got := list.ContentSize(cs, 12); got.W != 32 || got.H != 2 {
		t.Fatalf("two short rows produced an oversized picker: %+v", got)
	}
	list.Selection().SetIndex(1)
	if !list.measured {
		t.Fatal("selection invalidated intrinsic content width")
	}
	list.SetItemsStable([]ListItem{{ID: "one", Label: strings.Repeat("界", 24), Detail: "folder"}})
	if list.measured {
		t.Fatal("new items retained old intrinsic width")
	}
	got := list.ContentSize(cs, 12)
	if got.W < 56 || got.H != 1 {
		t.Fatalf("Unicode/detail width did not contribute to content measurement: %+v", got)
	}
	if tiny := list.ContentSize(Constraints{MaxW: 9, MaxH: 1}, 12); tiny.W > 9 || tiny.H > 1 {
		t.Fatalf("intrinsic size escaped parent constraints: %+v", tiny)
	}
}
