package ui

import (
	"crude/tui/theme"
)

// ListPopupListID is the list id inside a generic list popup frame.
const ListPopupListID = "list-popup/list"

// ListPopupActivate is emitted when a row is activated.
const ListPopupActivate = "listpopup.activate"

// ListPopupSpec builds a WindowFrame + List popup (LSP references, etc.).
type ListPopupSpec struct {
	ID       string // floating window id (for component path only)
	Title    string
	Items    []ListItem
	Selected int
	Width    int
	Height   int
	Closable bool
}

// BuildListPopupFrame builds a unified WindowFrame list popup.
// Replaces hand-drawn ╭─╮ borders for product list popups.
func BuildListPopupFrame(th *theme.Theme, spec ListPopupSpec) *ComponentWidget {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := NewTokenSet(th)

	w := spec.Width
	if w < 28 {
		w = 28
	}
	if w > 80 {
		w = 80
	}
	h := spec.Height
	if h < 6 {
		h = 6
	}

	listID := ComponentID(ListPopupListID)
	if spec.ID != "" {
		listID = NewID(RootID, spec.ID+"/list")
	}
	list := NewList(listID, tokens)
	list.SetActivateAction(ListPopupActivate)
	list.SetItems(spec.Items)
	if spec.Selected >= 0 {
		list.SelectIndex(spec.Selected)
	}

	frameID := NewID(RootID, "list-popup")
	if spec.ID != "" {
		frameID = NewID(RootID, spec.ID)
	}
	frame := NewWindowFrame(frameID, FramePopover, tokens)
	frame.SetTitle(spec.Title)
	frame.SetClosable(spec.Closable)
	frame.SetContent(list)

	cw := NewComponentWidget(th, frame, w, h)
	cw.Focus(listID)
	return cw
}

// ListPopupList returns the list from a framed list popup.
func ListPopupList(cw *ComponentWidget) *List {
	if cw == nil {
		return nil
	}
	// Prefer generic id, then walk for any List.
	if list := findList(cw.Root(), ComponentID(ListPopupListID)); list != nil {
		return list
	}
	return findAnyList(cw.Root())
}

func findAnyList(c Component) *List {
	if c == nil {
		return nil
	}
	if list, ok := c.(*List); ok {
		return list
	}
	if p, ok := c.(Parent); ok {
		for _, ch := range p.Children() {
			if found := findAnyList(ch); found != nil {
				return found
			}
		}
	}
	return nil
}
