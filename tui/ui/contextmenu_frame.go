package ui

import (
	"crude/tui/theme"
)

// ContextMenuListID is the list component id inside the context menu frame.
const ContextMenuListID = "context-menu/list"

// ContextMenuActivate is the action ID emitted when a menu item is activated.
const ContextMenuActivate = "contextmenu.activate"

// ContextMenuPayload carries section/item indices for activation.
type ContextMenuPayload struct {
	SectionIdx int
	ItemIdx    int
	FlatIdx    int
}

// BuildContextMenuFrame builds a WindowFrame + List context menu.
// Replaces PopupChromeLines for the product "context-menu" floating window.
// Title is empty (right-click menus are untitled).
func BuildContextMenuFrame(th *theme.Theme, st *ContextMenuState) *ComponentWidget {
	if th == nil {
		th = theme.DefaultDark()
	}
	if st == nil {
		st = &ContextMenuState{}
	}
	if st.FlatItems == nil {
		st.Build()
	}

	tokens := NewTokenSet(th)
	items := contextMenuListItems(st)
	list := NewList(ComponentID(ContextMenuListID), tokens)
	list.SetWrap(true)
	list.SetActivateAction(ContextMenuActivate)
	list.SetItems(items)
	// Restore cursor from state (flat index of selectable items → list index).
	if st.Cursor >= 0 && st.Cursor < len(st.FlatItems) {
		flat := st.FlatItems[st.Cursor]
		for i, it := range items {
			if p, ok := it.Payload.(ContextMenuPayload); ok &&
				p.SectionIdx == flat.sectionIdx && p.ItemIdx == flat.itemIdx {
				list.SelectIndex(i)
				break
			}
		}
	}

	w := ContextMenuPopupWidth(st)
	h := ContextMenuPopupHeight(st)
	// content height is h-2 (chrome); list fills content
	contentH := h - 2
	if contentH < 1 {
		contentH = 1
	}

	frame := NewWindowFrame(NewID(RootID, "context-menu"), FramePopover, tokens)
	frame.SetTitle("") // no title for context menus
	frame.SetClosable(false)
	frame.SetContent(list)

	cw := NewComponentWidget(th, frame, w, h)
	// Force preferred size to match computed chrome size.
	_ = contentH
	cw.Focus(ComponentID(ContextMenuListID))
	return cw
}

// contextMenuListItems flattens sections into ListItems with headers/separators.
func contextMenuListItems(st *ContextMenuState) []ListItem {
	var items []ListItem
	for si, sec := range st.Sections {
		if si > 0 {
			items = append(items, ListItem{Separator: true, Label: "─"})
		}
		if sec.SectionLabel != "" {
			items = append(items, ListItem{Header: true, Label: sec.SectionLabel})
		}
		for ii, item := range sec.Items {
			// Flat index among selectable-only items for Cursor sync.
			flatIdx := -1
			for fi, f := range st.FlatItems {
				if f.sectionIdx == si && f.itemIdx == ii {
					flatIdx = fi
					break
				}
			}
			items = append(items, ListItem{
				ID:       item.Label,
				Label:    item.Label,
				Detail:   item.Detail,
				Disabled: item.Disabled,
				Payload: ContextMenuPayload{
					SectionIdx: si,
					ItemIdx:    ii,
					FlatIdx:    flatIdx,
				},
			})
		}
	}
	return items
}

// ContextMenuList returns the list component from a framed menu, if present.
func ContextMenuList(cw *ComponentWidget) *List {
	if cw == nil {
		return nil
	}
	return findList(cw.Root(), ComponentID(ContextMenuListID))
}

// SyncContextMenuCursor writes the list selection back into st.Cursor (flat index).
func SyncContextMenuCursor(cw *ComponentWidget, st *ContextMenuState) {
	if st == nil {
		return
	}
	list := ContextMenuList(cw)
	if list == nil {
		return
	}
	item, ok := list.Selected()
	if !ok {
		return
	}
	p, ok := item.Payload.(ContextMenuPayload)
	if !ok || p.FlatIdx < 0 {
		return
	}
	st.Cursor = p.FlatIdx
}

// ApplyContextMenuCursor moves list selection to match st.Cursor.
func ApplyContextMenuCursor(cw *ComponentWidget, st *ContextMenuState) {
	if cw == nil || st == nil {
		return
	}
	list := ContextMenuList(cw)
	if list == nil {
		return
	}
	if st.Cursor < 0 || st.Cursor >= len(st.FlatItems) {
		return
	}
	flat := st.FlatItems[st.Cursor]
	for i, it := range list.Items() {
		if p, ok := it.Payload.(ContextMenuPayload); ok &&
			p.SectionIdx == flat.sectionIdx && p.ItemIdx == flat.itemIdx {
			list.SelectIndex(i)
			if h := cw.Host(); h != nil {
				h.MarkPaint()
			}
			return
		}
	}
}

func findList(c Component, id ComponentID) *List {
	if c == nil {
		return nil
	}
	if list, ok := c.(*List); ok && c.ID() == id {
		return list
	}
	if p, ok := c.(Parent); ok {
		for _, ch := range p.Children() {
			if found := findList(ch, id); found != nil {
				return found
			}
		}
	}
	return nil
}
