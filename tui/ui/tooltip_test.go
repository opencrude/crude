package ui

import (
	"testing"

	"crude/tui/theme"
)

// Tabs answer a tooltip only for the tab under the pointer, with that tab's
// cell as the anchor, and ask the owner only when asked.
func TestTabsTooltipDescribesTheHoveredTab(t *testing.T) {
	tabs := NewTabs("tabs", NewTokenSet(theme.DefaultDark()))
	tabs.SetItems([]TabItem{{ID: "a", Label: "first.go"}, {ID: "b", Label: "second.go"}}, "a")
	tabs.Layout(LayoutContext{}, Rect{W: 60, H: StripHeight})
	asked := 0
	tabs.SetTooltipFunc(func(id string) string {
		asked++
		if id == "b" {
			return "/work/second.go"
		}
		return ""
	})
	if _, _, ok := tabs.Tooltip(); ok || asked != 0 {
		t.Fatal("a tooltip without a hovered tab")
	}
	second := tabs.ranges[1]
	tabs.Handle(EventContext{LocalX: second.start + 1}, Event{Kind: EventMouseMotion})
	text, anchor, ok := tabs.Tooltip()
	if !ok || text != "/work/second.go" || anchor.X != second.start || anchor.W != second.end-second.start || anchor.H != 1 {
		t.Fatalf("tooltip = %q %+v %v", text, anchor, ok)
	}
	first := tabs.ranges[0]
	tabs.Handle(EventContext{LocalX: first.start + 1}, Event{Kind: EventMouseMotion})
	if _, _, ok := tabs.Tooltip(); ok {
		t.Fatal("a tab without text produced a tooltip")
	}
}
