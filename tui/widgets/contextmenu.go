package widgets

import (
	"fmt"
	"strings"
)

// ContextMenuItem is one entry in a context menu. Disabled items
// appear in the list (dim italic) but cannot be activated; the
// user can still navigate to them to see the label.
type ContextMenuItem struct {
	Label    string
	Detail   string // optional right-aligned hint (e.g. "Ctrl+F")
	Disabled bool
	Action   func()
}

// ContextMenuSection groups related items separated by a thin
// divider. An empty SectionLabel suppresses the label row but still
// emits the divider.
type ContextMenuSection struct {
	SectionLabel string
	Items        []ContextMenuItem
}

// ContextMenuState tracks the popup's current selection and the
// flattened list of selectable items. The App owns this and feeds
// it to the renderer on each frame.
type ContextMenuState struct {
	Title     string // popup window title (e.g. "Editor")
	Sections  []ContextMenuSection
	Cursor    int // index into FlatItems
	FlatItems []flatItem
}

type flatItem struct {
	sectionIdx int
	itemIdx    int
}

// Build flattens the sections into a single list of selectable
// items, skipping dividers and disabled items for navigation
// purposes.
func (s *ContextMenuState) Build() {
	s.FlatItems = nil
	for si, sec := range s.Sections {
		for ii, it := range sec.Items {
			if it.Disabled {
				continue
			}
			s.FlatItems = append(s.FlatItems, flatItem{sectionIdx: si, itemIdx: ii})
		}
	}
	if s.Cursor >= len(s.FlatItems) {
		s.Cursor = len(s.FlatItems) - 1
	}
	if s.Cursor < 0 && len(s.FlatItems) > 0 {
		s.Cursor = 0
	}
}

// Activate runs the action for the currently-selected item, if any.
func (s *ContextMenuState) Activate() bool {
	if s.Cursor < 0 || s.Cursor >= len(s.FlatItems) {
		return false
	}
	flat := s.FlatItems[s.Cursor]
	item := s.Sections[flat.sectionIdx].Items[flat.itemIdx]
	if item.Disabled || item.Action == nil {
		return false
	}
	item.Action()
	return true
}

// Next moves the cursor down by one selectable item (wraps).
func (s *ContextMenuState) Next() {
	if len(s.FlatItems) == 0 {
		return
	}
	s.Cursor = (s.Cursor + 1) % len(s.FlatItems)
}

// Prev moves the cursor up by one selectable item (wraps).
func (s *ContextMenuState) Prev() {
	if len(s.FlatItems) == 0 {
		return
	}
	s.Cursor--
	if s.Cursor < 0 {
		s.Cursor = len(s.FlatItems) - 1
	}
}

// contentWidth returns the exact inner content-area width (the
// chrome wrapper adds border + padding). Computed from the longest
// label and the longest detail in the menu, with minimum 18.
func (s *ContextMenuState) contentWidth() (maxLabel, maxDetail, contentW int) {
	maxLabel, maxDetail = 0, 0
	for _, sec := range s.Sections {
		for _, item := range sec.Items {
			if lw := len(item.Label); lw > maxLabel {
				maxLabel = lw
			}
			if item.Detail != "" {
				if dw := len(item.Detail); dw > maxDetail {
					maxDetail = dw
				}
			}
		}
	}
	// Layout: 1 left pad + label + 2 gap + detail + 1 right pad.
	if maxLabel < 12 {
		maxLabel = 12
	}
	contentW = 1 + maxLabel + 2 + maxDetail + 1
	for _, sec := range s.Sections {
		if sec.SectionLabel != "" && len(sec.SectionLabel)+2 > contentW {
			contentW = len(sec.SectionLabel) + 2
		}
	}
	return
}

// ContextMenuPopupWidth returns the floating window total width.
func ContextMenuPopupWidth(st *ContextMenuState) int {
	_, _, cw := st.contentWidth()
	w := cw + 6
	if w < 26 {
		w = 26
	}
	if w > 60 {
		w = 60
	}
	return w
}

// ContextMenuPopupHeight returns the floating window total height.
// Just the chrome (2 rows) plus the content rows. Context menus
// are tight — no extra padding at the bottom.
func ContextMenuPopupHeight(st *ContextMenuState) int {
	h := 0
	for i, sec := range st.Sections {
		if i > 0 {
			h++ // divider
		}
		if sec.SectionLabel != "" {
			h++ // section label
		}
		h += len(sec.Items)
	}
	if h == 0 {
		h = 1
	}
	return h + 2 // just the chrome (top + bottom border)
}

// QuickMenu builds a non-sectioned menu. Useful for simple yes/no
// prompts or when a full sectioned menu is overkill.
func QuickMenu(title string, items ...ContextMenuItem) *ContextMenuState {
	st := &ContextMenuState{Title: title}
	if len(items) > 0 {
		st.Sections = []ContextMenuSection{{Items: items}}
	}
	st.Build()
	return st
}

// FormatKeyHint is a no-op currently. Reserved for future platform
// normalization (e.g. "⌘F" on macOS vs "Ctrl+F" elsewhere).
func FormatKeyHint(s string) string { return s }

// FlatItemAt returns the (sectionIdx, itemIdx) for the i-th
// selectable (non-disabled) item. Returns false if i is out of
// range.
func (s *ContextMenuState) FlatItemAt(i int) (sectionIdx, itemIdx int, ok bool) {
	if i < 0 || i >= len(s.FlatItems) {
		return 0, 0, false
	}
	flat := s.FlatItems[i]
	return flat.sectionIdx, flat.itemIdx, true
}

// String returns a printable summary of the menu for debugging.
func (s *ContextMenuState) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ContextMenu{title=%q, items=%d, cursor=%d}\n", s.Title, len(s.FlatItems), s.Cursor)
	for i, sec := range s.Sections {
		fmt.Fprintf(&b, "  [%d] %s\n", i, sec.SectionLabel)
		for _, it := range sec.Items {
			fmt.Fprintf(&b, "    - %s\n", it.Label)
		}
	}
	return b.String()
}
