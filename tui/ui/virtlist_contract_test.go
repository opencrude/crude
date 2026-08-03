package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestVirtualListEndKeyStickToBottomContract is the keyboard interaction contract
// for End on VirtualList: scroll to MaxOffset and arm auto-scroll (chat gravity).
func TestVirtualListEndKeyStickToBottomContract(t *testing.T) {
	tests := []struct {
		name      string
		items     []VirtItem
		viewportH int
		preScroll int // rows scrolled up before End; 0 = start at top
	}{
		{
			name: "single_row_fits",
			items: []VirtItem{
				{ID: "a", Height: 1, Label: "only"},
			},
			viewportH: 4,
		},
		{
			name: "multi_height_overflow",
			items: []VirtItem{
				{ID: "a", Height: 2, Label: "a"},
				{ID: "b", Height: 3, Label: "b"},
				{ID: "c", Height: 2, Label: "c"},
			},
			viewportH: 3,
			preScroll: 0,
		},
		{
			name: "scrolled_up_then_end",
			items: []VirtItem{
				{ID: "a", Height: 1, Label: "a"},
				{ID: "b", Height: 1, Label: "b"},
				{ID: "c", Height: 1, Label: "c"},
				{ID: "d", Height: 1, Label: "d"},
				{ID: "e", Height: 1, Label: "e"},
			},
			viewportH: 2,
			preScroll: 2,
		},
		{
			name: "with_bottom_padding",
			items: []VirtItem{
				{ID: "a", Height: 2, Label: "a"},
				{ID: "b", Height: 2, Label: "b"},
			},
			viewportH: 3,
			preScroll: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			list := NewVirtualList("contract", nil)
			list.SetItems(tc.items)
			if tc.name == "with_bottom_padding" {
				list.SetBottomPadding(2)
			}
			list.Layout(LayoutContext{}, Rect{W: 20, H: tc.viewportH})

			if tc.preScroll > 0 {
				list.Scroll().ScrollTo(tc.preScroll)
				list.Scroll().SetAutoScroll(false)
				if list.Scroll().AutoScroll() {
					t.Fatal("pre-scroll setup failed to disarm auto-scroll")
				}
			}

			res := list.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnd}))
			if !res.Handled {
				t.Fatal("End key must be handled")
			}

			max := list.Scroll().MaxOffset()
			if list.Scroll().Offset() != max {
				t.Fatalf("offset=%d want MaxOffset=%d after End", list.Scroll().Offset(), max)
			}
			if !list.Scroll().AutoScroll() {
				t.Fatal("End key must arm auto-scroll (StickToBottom)")
			}
			if !list.Scroll().ShouldAutoScroll() {
				t.Fatal("ShouldAutoScroll must be true at bottom after End")
			}

			lastIdx := len(tc.items) - 1
			if list.SelectedIndex() != lastIdx {
				t.Fatalf("selection=%d want last item %d", list.SelectedIndex(), lastIdx)
			}
		})
	}
}

// TestScrollModelShouldAutoScrollContract is the stick-to-bottom re-arm contract
// for ScrollModel near-bottom threshold (5 rows, matching virtlist migration).
func TestScrollModelShouldAutoScrollContract(t *testing.T) {
	const threshold = 5
	tests := []struct {
		name      string
		content   int
		viewport  int
		startOff  int
		armed     bool
		wantStick bool
		wantArmed bool
	}{
		{name: "fits_no_scroll", content: 3, viewport: 10, armed: false, wantStick: true, wantArmed: true},
		{name: "at_bottom", content: 20, viewport: 5, startOff: 15, armed: true, wantStick: true, wantArmed: true},
		{name: "near_bottom_within_threshold", content: 20, viewport: 5, startOff: 11, armed: false, wantStick: true, wantArmed: true},
		{name: "scrolled_up_disarms", content: 20, viewport: 5, startOff: 0, armed: true, wantStick: false, wantArmed: false},
		{name: "return_to_bottom_rearms", content: 20, viewport: 5, startOff: 15, armed: false, wantStick: true, wantArmed: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewScrollModel()
			s.SetContent(tc.content)
			s.SetViewport(tc.viewport)
			s.SetAutoScroll(tc.armed)
			s.ScrollTo(tc.startOff)

			got := s.ShouldAutoScroll()
			if got != tc.wantStick {
				t.Fatalf("ShouldAutoScroll()=%v want %v (off=%d max=%d)", got, tc.wantStick, s.Offset(), s.MaxOffset())
			}
			if s.AutoScroll() != tc.wantArmed {
				t.Fatalf("AutoScroll()=%v want %v after ShouldAutoScroll", s.AutoScroll(), tc.wantArmed)
			}
			_ = threshold // documented: NearBottom uses 5 rows internally
			if tc.wantStick && s.Offset() != s.MaxOffset() && s.MaxOffset() > 0 {
				near := s.Offset() >= s.MaxOffset()-5
				if !near {
					t.Fatalf("stick without being at/near bottom: off=%d max=%d", s.Offset(), s.MaxOffset())
				}
			}
		})
	}
}

// TestVirtualListSelectionAnchorsScrollContract documents default selection-anchored
// scroll vs paint-only lists that preserve user offset when auto-scroll is disarmed.
func TestVirtualListSelectionAnchorsScrollContract(t *testing.T) {
	items := []VirtItem{
		{ID: "a", Height: 1, Label: "a"},
		{ID: "b", Height: 1, Label: "b"},
		{ID: "c", Height: 1, Label: "c"},
		{ID: "d", Height: 1, Label: "d"},
		{ID: "e", Height: 1, Label: "e"},
		{ID: "f", Height: 1, Label: "f"},
		{ID: "g", Height: 1, Label: "g"},
		{ID: "h", Height: 1, Label: "h"},
		{ID: "i", Height: 1, Label: "i"},
		{ID: "j", Height: 1, Label: "j"},
	}

	t.Run("default_anchoring_setitems_reveals_selection", func(t *testing.T) {
		list := NewVirtualList("contract", nil)
		if !list.SelectionAnchorsScroll() {
			t.Fatal("default should anchor selection to scroll")
		}
		list.SetItems(items)
		list.Layout(LayoutContext{}, Rect{W: 20, H: 3})
		list.Scroll().SetAutoScroll(false)
		list.Scroll().ScrollTo(5)
		want := list.Scroll().Offset()
		if want == 0 {
			t.Fatal("setup: need non-zero offset")
		}
		list.SetItems(items)
		if list.Scroll().Offset() == want {
			t.Fatalf("default anchoring should reveal index 0, offset=%d want != %d", list.Scroll().Offset(), want)
		}
		if list.Scroll().Offset() != 0 {
			t.Fatalf("default anchoring should scroll to selected item start, offset=%d", list.Scroll().Offset())
		}
	})

	t.Run("disabled_preserves_offset_on_setitems_and_layout", func(t *testing.T) {
		list := NewVirtualList("contract", nil)
		list.SetSelectionAnchorsScroll(false)
		list.SetItems(items)
		list.Layout(LayoutContext{}, Rect{W: 20, H: 3})
		list.Scroll().SetAutoScroll(false)
		list.Scroll().ScrollTo(5)
		want := list.Scroll().Offset()

		grown := append(items, VirtItem{ID: "k", Height: 1, Label: "k"})
		list.SetItems(grown)
		if list.Scroll().Offset() != want {
			t.Fatalf("SetItems with disarmed scroll: offset=%d want %d", list.Scroll().Offset(), want)
		}
		if list.Scroll().AutoScroll() {
			t.Fatal("SetItems must not arm auto-scroll when disarmed")
		}

		list.Layout(LayoutContext{}, Rect{W: 20, H: 3})
		if list.Scroll().Offset() != want {
			t.Fatalf("Layout with disarmed scroll: offset=%d want %d", list.Scroll().Offset(), want)
		}
	})

	t.Run("disabled_clamps_when_content_shrinks", func(t *testing.T) {
		list := NewVirtualList("contract", nil)
		list.SetSelectionAnchorsScroll(false)
		list.SetItems(items)
		list.Layout(LayoutContext{}, Rect{W: 20, H: 3})
		list.Scroll().SetAutoScroll(false)
		list.Scroll().ScrollTo(8)
		shrunk := items[:3]
		list.SetItems(shrunk)
		max := list.Scroll().MaxOffset()
		if list.Scroll().Offset() != max {
			t.Fatalf("shrunk content should clamp offset=%d max=%d", list.Scroll().Offset(), max)
		}
	})

	t.Run("armed_stick_to_bottom_unchanged", func(t *testing.T) {
		list := NewVirtualList("contract", nil)
		list.SetSelectionAnchorsScroll(false)
		list.Scroll().SetAutoScroll(true)
		list.SetItems(items[:5])
		list.Layout(LayoutContext{}, Rect{W: 20, H: 3})
		grown := append(items[:5], VirtItem{ID: "k", Height: 2, Label: "k"})
		list.SetItems(grown)
		if list.Scroll().Offset() != list.Scroll().MaxOffset() || !list.Scroll().AutoScroll() {
			t.Fatalf("armed append should stick to bottom: off=%d max=%d auto=%v",
				list.Scroll().Offset(), list.Scroll().MaxOffset(), list.Scroll().AutoScroll())
		}
	})

	t.Run("re_enable_reveals_selection", func(t *testing.T) {
		list := NewVirtualList("contract", nil)
		list.SetSelectionAnchorsScroll(false)
		list.SetItems(items)
		list.Layout(LayoutContext{}, Rect{W: 20, H: 3})
		list.Scroll().SetAutoScroll(false)
		list.Scroll().ScrollTo(7)
		list.SetSelectionAnchorsScroll(true)
		if list.Scroll().Offset() == 7 {
			t.Fatal("re-enabling anchoring should reveal selected item")
		}
		if list.Scroll().Offset() != 0 {
			t.Fatalf("selected index 0 should be visible at offset 0, got %d", list.Scroll().Offset())
		}
	})
}
