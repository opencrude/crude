package ui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/theme"
)

func TestTableRefreshAndPointerLeavePreserveReadingPosition(t *testing.T) {
	table := NewTable("table", NewTokenSet(theme.DefaultDark()))
	var rows []TableRow
	for i := range 30 {
		rows = append(rows, TableRow{ID: fmt.Sprint(i), Cells: []TableCell{{Text: fmt.Sprint(i)}}})
	}
	table.SetRows(rows)
	host := NewHost(theme.DefaultDark(), table)
	host.Resize(40, 6)
	host.Layout()
	host.Handle(tea.MouseWheelMsg{X: 3, Y: 3, Button: tea.MouseWheelDown})
	before := table.Scroll().Offset()
	if before == 0 {
		t.Fatal("wheel did not scroll the table")
	}
	table.SetRows(rows)
	host.Handle(tea.MouseMotionMsg{X: 45, Y: 8})
	host.MarkLayout()
	host.Layout()
	if got := table.Scroll().Offset(); got != before {
		t.Fatalf("refresh/pointer leave moved the reading position: %d -> %d", before, got)
	}
	host.Focus(table.ID())
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if table.SelectedIndex() != 1 || table.Scroll().Offset() > 1 {
		t.Fatal("keyboard navigation no longer reveals the selected row")
	}
}

func TestVirtualListRefreshAndPointerLeavePreserveReadingPosition(t *testing.T) {
	list := NewVirtualList("list", NewTokenSet(theme.DefaultDark()))
	var rows []VirtItem
	for i := range 30 {
		rows = append(rows, VirtItem{ID: fmt.Sprint(i), Label: fmt.Sprint(i), Height: 2})
	}
	list.SetItems(rows)
	host := NewHost(theme.DefaultDark(), list)
	host.Resize(40, 6)
	host.Layout()
	host.Handle(tea.MouseWheelMsg{X: 3, Y: 3, Button: tea.MouseWheelDown})
	before := list.Scroll().Offset()
	if before == 0 {
		t.Fatal("wheel did not scroll the list")
	}
	list.SetItems(rows)
	host.Handle(tea.MouseMotionMsg{X: 45, Y: 8})
	host.MarkLayout()
	host.Layout()
	if got := list.Scroll().Offset(); got != before {
		t.Fatalf("refresh/pointer leave moved the reading position: %d -> %d", before, got)
	}
	host.Focus(list.ID())
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if list.SelectedIndex() != 1 || list.Scroll().Offset() > 2 {
		t.Fatal("keyboard navigation no longer reveals the selected item")
	}
}
