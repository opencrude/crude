package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

func TestTabsManualWheelScrollSurvivesLayoutAndProjection(t *testing.T) {
	for _, button := range []tea.MouseButton{tea.MouseWheelLeft, tea.MouseWheelUp} {
		t.Run(fmt.Sprint(button), func(t *testing.T) {
			items := make([]TabItem, 16)
			for index := range items {
				items[index] = TabItem{ID: fmt.Sprint(index), Label: fmt.Sprintf("Terminal %d", index+1)}
			}
			tabs := NewTabs("tabs", NewTokenSet(theme.DefaultDark()))
			tabs.SetItems(items, "15")
			host := NewHost(theme.DefaultDark(), tabs)
			host.Resize(60, StripHeight)
			host.Layout()
			before := tabs.ScrollOffset()
			initial := surface.NewCellBuffer(60, StripHeight)
			host.Paint(initial)
			result := host.Handle(tea.MouseWheelMsg{X: 10, Y: 0, Button: button})
			if !result.Handled || tabs.ScrollOffset() >= before || tabs.ActiveID() != "15" || result.Action != nil {
				t.Fatalf("wheel must browse without selecting: offset=%d before=%d result=%+v", tabs.ScrollOffset(), before, result)
			}
			offset := tabs.ScrollOffset()
			tabs.SetItems(items, "15")
			host.MarkLayout()
			host.Layout()
			painted := surface.NewCellBuffer(60, StripHeight)
			host.Paint(painted)
			if tabs.ScrollOffset() != offset || stripRow(initial, 0, 60) == stripRow(painted, 0, 60) {
				t.Fatal("same-active projection/layout reset scrolling or did not move painted tabs")
			}
			for range 30 {
				host.Handle(tea.MouseWheelMsg{X: 10, Y: 0, Button: button})
			}
			if tabs.ScrollOffset() != 0 {
				t.Fatal("left scrolling did not reach the first tab")
			}
		})
	}
}

func TestTabsOverflowControlsBrowseWithoutActivating(t *testing.T) {
	items := make([]TabItem, 16)
	for index := range items {
		items[index] = TabItem{ID: fmt.Sprint(index), Label: fmt.Sprintf("Terminal %d", index+1)}
	}
	tabs := NewTabs("tabs", NewTokenSet(theme.DefaultDark()))
	tabs.SetItems(items, "15")
	host := NewHost(theme.DefaultDark(), tabs)
	host.Resize(60, StripHeight)
	host.Layout()
	buffer := surface.NewCellBuffer(60, StripHeight)
	host.Paint(buffer)
	if row := stripRow(buffer, 0, 60); !strings.Contains(row, "‹") || !strings.Contains(row, "›") {
		t.Fatalf("overflow has no discoverable scroll controls: %q", row)
	}
	left := -1
	for column := range 60 {
		if buffer.Get(column, 0).Char == '‹' {
			left = column
		}
	}
	before := tabs.ScrollOffset()
	result := host.Handle(tea.MouseClickMsg{X: left, Y: 0, Button: tea.MouseLeft})
	if !result.Handled || result.Action != nil || tabs.ScrollOffset() >= before || tabs.ActiveID() != "15" {
		t.Fatalf("scroll arrow selected a tab or did not scroll: %+v", result)
	}
	host.Handle(tea.MouseReleaseMsg{X: left, Y: 0, Button: tea.MouseLeft})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyHome})
	if tabs.ActiveID() != "0" || tabs.ScrollOffset() != 0 {
		t.Fatal("Home did not activate and reveal the first tab")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnd})
	if tabs.ActiveID() != "15" || tabs.ranges[15].start < 0 {
		t.Fatal("End did not activate and reveal the last tab")
	}
}

func TestTabsCompactNumberLabelsStayContentSizedAndWhole(t *testing.T) {
	items := make([]TabItem, 16)
	for index := range items {
		items[index] = TabItem{ID: fmt.Sprint(index), Label: fmt.Sprintf("Terminal %d", index+1), CompactLabel: fmt.Sprintf("T %d", index+1)}
	}
	tabs := NewTabs("tabs", NewTokenSet(theme.DefaultDark()))
	tabs.SetItems(items, "15")
	tabs.Layout(LayoutContext{}, Rect{W: 60, H: StripHeight})
	tabs.SetScroll(0)
	buffer := surface.NewCellBuffer(60, StripHeight)
	tabs.Paint(buffer, 0, 0, PaintContext{})
	if row := stripRow(buffer, 0, 60); !strings.Contains(row, "T 1  T 2") || strings.Contains(row, "…") {
		t.Fatalf("compact numbered labels are padded or truncated: %q", row)
	}
	for index, hit := range tabs.ranges {
		if hit.start >= 0 && hit.end-hit.start != len(items[index].CompactLabel) {
			t.Fatalf("compact tab %d range=%+v lost content sizing", index+1, hit)
		}
	}
	tabs.Layout(LayoutContext{}, Rect{W: 240, H: StripHeight})
	buffer = surface.NewCellBuffer(240, StripHeight)
	tabs.Paint(buffer, 0, 0, PaintContext{})
	if row := stripRow(buffer, 0, 240); !strings.Contains(row, "Terminal 1") || !strings.Contains(row, "Terminal 16") || strings.Contains(row, "‹") {
		t.Fatalf("wide strip did not restore full names without overflow: %q", row)
	}
}
