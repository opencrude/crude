package ui

// Hover tracks mouse hover state for a single widget.
//
// Every hover-capable widget (file tree, tab bar, command palette, file
// picker, launcher, message list, form items, ...) needs the same two
// things: an "is something hovered" boolean and a "which item" index.
// Rather than re-declaring those fields on every widget, embed Hover:
//
//	type FileTreeModel struct {
//	    ...
//	    Hover ui.Hover
//	    ...
//	}
//
// And call Hover.Set(idx) when the mouse moves over an item, Hover.Clear()
// when the mouse leaves, and read Hover.Index at render time.
//
// Index == -1 means "no item hovered" — the same convention used by
// the existing popup hover fields (fr.HoveredToggle = -1 when nothing
// is hovered, m.refCursor = -1 when no reference is selected, etc.).
// Centralising this lets every widget agree on what "no hover" looks
// like and lets the renderer use a single branch to decide whether to
// paint the hover background.
//
// Because Go's struct zero value for an int field is 0 (not -1), widgets
// that embed Hover must initialise it explicitly — either by calling
// the NewHover constructor and assigning it back, or by calling
// h.Clear() at construction time. See FileTreeModel.NewFileTreeModel
// for the canonical pattern.
type Hover struct {
	// Index identifies which item is hovered. -1 means nothing.
	Index int
}

// NewHover returns a Hover in the "no item hovered" state. Widgets
// should use this in their constructors:
//
//	m := &MyWidget{Hover: ui.NewHover()}
//
// …or call m.Hover.Clear() right after creating the widget if they
// initialise it with a struct literal.
func NewHover() Hover {
	return Hover{Index: -1}
}

// IsActive reports whether something is currently hovered.
func (h *Hover) IsActive() bool {
	return h.Index >= 0
}

// Set updates the hover index. Returns true if the value changed —
// callers can use the return value to set the dirty flag without
// unconditionally marking the widget as needing a re-render on every
// motion event (which would burn cycles even when the mouse only
// jiggles inside the same item).
func (h *Hover) Set(idx int) bool {
	if idx < 0 {
		idx = -1
	}
	if h.Index == idx {
		return false
	}
	h.Index = idx
	return true
}

// Clear resets hover state to "no item". Returns true if the state
// changed (i.e. something was hovered before).
func (h *Hover) Clear() bool {
	if h.Index == -1 {
		return false
	}
	h.Index = -1
	return true
}
